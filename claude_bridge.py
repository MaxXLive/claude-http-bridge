#!/usr/bin/env python3
"""Claude Bridge: makes the Claude Code CLI on this computer usable over HTTP.

Lets plugins on a server that can't run the CLI itself (e.g. a hosted Minecraft server)
use the account the CLI is logged in with here, such as a Claude Pro/Max subscription.
Clients sign every request with their own Ed25519 key; the bridge only knows their public
keys (authorized_keys), like SSH. See README.md for the endpoints.

    python claude_bridge.py --add-key "ed25519 MCowBQYDK2VwAyEA... claudechat@server"
    python claude_bridge.py
"""
import argparse
import base64
import binascii
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

from cryptography.exceptions import InvalidSignature
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PublicKey
from cryptography.hazmat.primitives.serialization import load_der_public_key

__version__ = "1.3.0"

MAX_BODY = 1_000_000
MODEL = re.compile(r"^(sonnet|opus|haiku|claude-[a-z0-9-]+)$")
SESSION_ID = re.compile(r"^[0-9a-fA-F-]{36}$")
WORKSPACE_PART = re.compile(r"^[A-Za-z0-9._-]{1,64}$")
# Sites Claude may read during web research (subdomains need their own entry): the wider Minecraft
# world, i.e. game, mods, plugins, datapacks, farms and tools. "*" in --web-domains allows every site.
WEB_DOMAINS = (
    # Game and wikis
    "minecraft.wiki", "minecraft.fandom.com", "minecraft.net", "www.minecraft.net", "feedback.minecraft.net",
    "help.minecraft.net", "bugs.mojang.com", "report.bugs.mojang.com",
    # Mods, plugins, datapacks, modpacks
    "modrinth.com", "docs.modrinth.com", "curseforge.com", "www.curseforge.com", "legacy.curseforge.com",
    "fabricmc.net", "docs.fabricmc.net", "wiki.fabricmc.net", "neoforged.net", "docs.neoforged.net",
    "minecraftforge.net", "files.minecraftforge.net", "papermc.io", "docs.papermc.io", "hangar.papermc.io",
    "spigotmc.org", "www.spigotmc.org", "bukkit.org", "dev.bukkit.org", "github.com", "gist.github.com",
    "raw.githubusercontent.com", "ftb.fandom.com", "ftbwiki.org", "wiki.createmod.net", "create.fandom.com",
    "guide.appliedenergistics.org", "wiki.aidancbrady.com",
    # Community: farms, builds, how-tos
    "reddit.com", "www.reddit.com", "old.reddit.com", "planetminecraft.com", "www.planetminecraft.com",
    "minecraftforum.net", "www.minecraftforum.net", "youtube.com", "www.youtube.com", "techmcdocs.github.io",
    # Tools
    "chunkbase.com", "www.chunkbase.com", "misode.github.io", "mcstacker.net",
)
WEB_DOMAINS = (
    "minecraft.wiki", "minecraft.net", "www.minecraft.net", "feedback.minecraft.net", "help.minecraft.net",
    "bugs.mojang.com", "report.bugs.mojang.com", "papermc.io", "docs.papermc.io", "hangar.papermc.io",
    "modrinth.com", "docs.modrinth.com", "curseforge.com", "www.curseforge.com", "fabricmc.net",
    "docs.fabricmc.net", "neoforged.net", "docs.neoforged.net", "github.com", "spigotmc.org", "www.spigotmc.org",
)
# Inherited from a parent Claude Code session, they would change how the child CLI behaves
# Token counts of a /v1/chat reply, as reported by the CLI
USAGE_KEYS = ("input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "output_tokens")
CLEARED_ENV = ("CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT")


class BadRequest(Exception):
    pass


class AuthorizedKeys:
    """Public keys allowed to use the bridge, one "ed25519 <base64 DER> <name>" per line.

    The file is re-read when it changes, so keys can be added or revoked without a restart.
    A request signs "METHOD\\nPATH\\nTIMESTAMP\\nNONCE\\nSHA256(BODY)"; timestamp and nonce
    stop a captured request from being replayed.
    """

    MAX_SKEW = 120

    def __init__(self, path):
        self.path = Path(path)
        self.keys = {}
        self.mtime = None
        self.seen_nonces = {}
        self.lock = threading.Lock()

    @staticmethod
    def parse(line):
        """Returns (base64 key, public key, name) or raises ValueError."""
        parts = line.split(None, 2)
        if len(parts) < 2 or parts[0] != "ed25519":
            raise ValueError("expected: ed25519 <base64 key> [name]")
        try:
            key = load_der_public_key(base64.b64decode(parts[1], validate=True))
        except (ValueError, binascii.Error) as error:
            raise ValueError(f"not a valid key: {error}")
        if not isinstance(key, Ed25519PublicKey):
            raise ValueError("not an Ed25519 key")
        return parts[1], key, parts[2].strip() if len(parts) > 2 else "unnamed"

    def reload_if_changed(self):
        mtime = self.path.stat().st_mtime if self.path.exists() else None
        if mtime == self.mtime:
            return
        keys = {}
        if mtime is not None:
            for number, line in enumerate(self.path.read_text(encoding="utf-8").splitlines(), 1):
                line = line.strip()
                if not line or line.startswith("#"):
                    continue
                try:
                    encoded, key, name = self.parse(line)
                    keys[encoded] = (key, name)
                except ValueError as error:
                    print(f"{self.path}:{number}: skipped, {error}", flush=True)
        self.keys, self.mtime = keys, mtime
        print(f"{len(keys)} authorized key(s): {', '.join(name for _, name in keys.values()) or '-'}", flush=True)

    def verify(self, headers, method, path, body):
        """Returns the client's name, or None when the request isn't validly signed by a known key."""
        with self.lock:
            self.reload_if_changed()
            entry = self.keys.get(headers.get("X-Bridge-Key", ""))
            if entry is None:
                return None
            key, name = entry
            try:
                timestamp = int(headers.get("X-Bridge-Timestamp", ""))
                nonce = headers.get("X-Bridge-Nonce", "")
                signature = base64.b64decode(headers.get("X-Bridge-Signature", ""), validate=True)
            except (ValueError, binascii.Error):
                return None
            now = time.time()
            if abs(now - timestamp) > self.MAX_SKEW or not 16 <= len(nonce) <= 64 or nonce in self.seen_nonces:
                return None
            payload = f"{method}\n{path}\n{timestamp}\n{nonce}\n{hashlib.sha256(body).hexdigest()}"
            try:
                key.verify(signature, payload.encode("utf-8"))
            except InvalidSignature:
                return None
            self.seen_nonces = {n: t for n, t in self.seen_nonces.items() if now - t <= 2 * self.MAX_SKEW}
            self.seen_nonces[nonce] = now
            return name

    def add(self, line):
        encoded, _, name = self.parse(line.strip())
        self.path.parent.mkdir(parents=True, exist_ok=True)
        existing = self.path.read_text(encoding="utf-8") if self.path.exists() else ""
        if encoded in existing:
            return f"{name} is already authorized"
        with self.path.open("a", encoding="utf-8") as file:
            if existing and not existing.endswith("\n"):
                file.write("\n")
            file.write(line.strip() + "\n")
        return f"authorized {name} in {self.path}"


def base_command(args, model, system, workdir, web=False):
    """CLI flags every request gets: no local tools, MCP servers or local config.

    With web, Claude may search the web and read pages of the allowlisted domains. Everything
    else would need a permission prompt, which dontAsk denies, so it can't send what it knows
    (e.g. player data) to arbitrary URLs.
    """
    command = [
        args.claude, "--print",
        "--model", model,
        "--strict-mcp-config",
        "--safe-mode",
    ]
    if web:
        command += ["--tools", "WebSearch,WebFetch", "--permission-mode", "dontAsk",
                    "--allowedTools", "WebSearch",
                    *(["WebFetch"] if "*" in args.web_domains
                      else (f"WebFetch(domain:{domain})" for domain in args.web_domains))]
    else:
        command += ["--tools", ""]
    if system:
        # As a file: Windows limits command lines to ~32k characters
        prompt_file = workdir / ".system-prompt.txt"
        prompt_file.write_text(system, encoding="utf-8")
        command += ["--system-prompt-file", str(prompt_file)]
    return command


def child_env():
    env = dict(os.environ)
    for name in CLEARED_ENV:
        env.pop(name, None)
    return env


def render_transcript(messages):
    """/v1/chat is stateless, so the conversation so far is written out as one prompt."""
    *history, last = messages
    parts = []
    if history:
        parts.append("Conversation so far:")
        for message in history:
            tag = "assistant" if message["role"] == "assistant" else "user"
            parts.append(f"<{tag}>\n{message['content']}\n</{tag}>")
        parts.append("")
        parts.append("New message, reply to it:")
    parts.append(last["content"])
    return "\n".join(parts)


class Bridge:
    def __init__(self, args):
        self.args = args
        self.slots = threading.BoundedSemaphore(args.parallel)
        self.workspaces = Path(args.workspaces)
        self.workspaces.mkdir(parents=True, exist_ok=True)

    def model(self, request):
        model = request.get("model") or self.args.model
        if not isinstance(model, str) or not MODEL.match(model):
            raise BadRequest(f"invalid model: {model!r}")
        return model

    def web(self, request):
        """Whether this request gets web research: the client asks for it and the bridge allows it."""
        return bool(request.get("web")) and not self.args.no_web

    def workspace(self, name):
        """Each client keeps its own folder; --resume only finds sessions started in the same folder."""
        if not name:
            path = self.workspaces / "_stateless"
        else:
            parts = str(name).replace("\\", "/").split("/")
            if not all(WORKSPACE_PART.match(part) and part not in (".", "..") for part in parts):
                raise BadRequest("invalid workspace, use segments of A-Z a-z 0-9 . _ - separated by /")
            path = self.workspaces.joinpath(*parts)
        path.mkdir(parents=True, exist_ok=True)
        return path

    def chat(self, request):
        """Stateless chat: full conversation in, reply text and token usage out."""
        messages = request.get("messages")
        if not isinstance(messages, list) or not messages or messages[-1].get("role") != "user":
            raise BadRequest("messages must be a non-empty list ending with a user message")
        workdir = Path(tempfile.mkdtemp(prefix="chat-", dir=self.workspace(None)))
        try:
            web = self.web(request)
            command = base_command(self.args, self.model(request), request.get("system"), workdir, web)
            command += ["--output-format", "json", "--no-session-persistence"]
            if web:
                # Room for a few searches and page reads before the answer
                command += ["--max-turns", "10"]
            result = subprocess.run(command, input=render_transcript(messages), capture_output=True, text=True,
                                    encoding="utf-8", cwd=workdir, env=child_env(), timeout=self.args.timeout)
        finally:
            shutil.rmtree(workdir, ignore_errors=True)
        try:
            output = json.loads(result.stdout)
        except json.JSONDecodeError:
            raise RuntimeError(f"claude exited with {result.returncode}: {(result.stderr or result.stdout).strip()}")
        if output.get("is_error"):
            raise RuntimeError(output.get("result") or "claude reported an error")
        usage = output.get("usage") or {}
        return {"text": output.get("result", ""), "usage": {key: usage.get(key, 0) for key in USAGE_KEYS}}

    def run_command(self, request):
        """The CLI call for /v1/run: stream-json output, optionally resuming a session."""
        prompt = request.get("prompt")
        if not isinstance(prompt, str) or not prompt:
            raise BadRequest("prompt is required")
        workdir = self.workspace(request.get("workspace"))
        command = base_command(self.args, self.model(request), request.get("system"), workdir, self.web(request))
        command += ["--output-format", "stream-json", "--verbose",
                    "--max-turns", str(max(1, min(int(request.get("max_turns", 1)), 50)))]
        session_id = request.get("session_id")
        if session_id:
            if not SESSION_ID.match(str(session_id)):
                raise BadRequest("invalid session_id")
            command += ["--resume", session_id]
        return command, prompt, workdir


class Server(ThreadingHTTPServer):
    daemon_threads = True

    def handle_error(self, request, client_address):
        # Clients closing idle keep-alive connections is normal, not worth a traceback
        if not isinstance(sys.exc_info()[1], (ConnectionResetError, BrokenPipeError)):
            super().handle_error(request, client_address)


def make_handler(bridge, authorized_keys):
    args = bridge.args

    class Handler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"
        server_version = f"ClaudeBridge/{__version__}"

        def send_json(self, status, payload):
            body = json.dumps(payload).encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            if status >= 400:
                # The request body may not have been read; on a kept-alive connection it would
                # otherwise be parsed as the next request
                self.send_header("Connection", "close")
                self.close_connection = True
            self.end_headers()
            self.wfile.write(body)

        def read_body(self):
            length = int(self.headers.get("Content-Length") or 0)
            if length <= 0 or length > MAX_BODY:
                raise BadRequest("missing or too large body")
            return self.rfile.read(length)

        @staticmethod
        def parse_json(body):
            try:
                request = json.loads(body)
            except json.JSONDecodeError as error:
                raise BadRequest(f"invalid json: {error}")
            if not isinstance(request, dict):
                raise BadRequest("body must be a json object")
            return request

        def do_GET(self):
            if self.path == "/health":
                self.send_json(200, {"ok": True, "version": __version__})
            else:
                self.send_json(404, {"error": "not found"})

        def do_POST(self):
            routes = {"/v1/chat": self.handle_chat, "/chat": self.handle_chat, "/v1/run": self.handle_run}
            route = routes.get(self.path)
            if route is None:
                return self.send_json(404, {"error": "not found"})
            try:
                body = self.read_body()
            except BadRequest as error:
                return self.send_json(400, {"error": str(error)})
            client = authorized_keys.verify(self.headers, "POST", self.path, body)
            if client is None:
                return self.send_json(401, {"error": "unauthorized: unknown key, bad signature or clock skew"})
            try:
                request = self.parse_json(body)
            except BadRequest as error:
                return self.send_json(400, {"error": str(error)})
            if not bridge.slots.acquire(timeout=args.queue_timeout):
                return self.send_json(503, {"error": "busy, too many requests at once"})
            started = time.monotonic()
            try:
                route(request)
            except BadRequest as error:
                self.send_json(400, {"error": str(error)})
            finally:
                bridge.slots.release()
                self.log_message("%s %s done in %.1fs", client, self.path, time.monotonic() - started)

        def handle_chat(self, request):
            try:
                self.send_json(200, bridge.chat(request))
            except subprocess.TimeoutExpired:
                self.send_json(504, {"error": "claude timed out"})
            except RuntimeError as error:
                self.log_error("chat failed: %s", error)
                self.send_json(502, {"error": str(error)})

        def handle_run(self, request):
            command, prompt, workdir = bridge.run_command(request)
            process = subprocess.Popen(command, cwd=workdir, env=child_env(), stdin=subprocess.PIPE,
                                       stdout=subprocess.PIPE, stderr=subprocess.PIPE, encoding="utf-8")
            stderr = []
            threading.Thread(target=lambda: stderr.extend(process.stderr), daemon=True).start()
            timer = threading.Timer(args.timeout, process.kill)
            timer.start()

            self.send_response(200)
            self.send_header("Content-Type", "application/x-ndjson")
            self.send_header("Transfer-Encoding", "chunked")
            self.send_header("Cache-Control", "no-cache")
            self.send_header("X-Accel-Buffering", "no")
            self.end_headers()
            try:
                process.stdin.write(prompt)
                process.stdin.close()
                for line in process.stdout:
                    self.send_chunk(line if line.endswith("\n") else line + "\n")
                code = process.wait()
                if code != 0 and stderr:
                    self.log_error("run exited with %d: %s", code, "".join(stderr).strip()[:500])
                # Last line: the CLI's exit code, which the client would otherwise not see
                self.send_chunk(json.dumps({"type": "bridge_exit", "code": code}) + "\n")
                self.wfile.write(b"0\r\n\r\n")
            except (BrokenPipeError, ConnectionResetError):
                # The client hung up (cancelled), so stop working for it
                process.kill()
            finally:
                timer.cancel()
                if process.poll() is None:
                    process.kill()

        def send_chunk(self, text):
            data = text.encode("utf-8")
            self.wfile.write(f"{len(data):x}\r\n".encode() + data + b"\r\n")
            self.wfile.flush()

    return Handler


def main():
    parser = argparse.ArgumentParser(description="HTTP bridge to the Claude Code CLI")
    parser.add_argument("--host", default=os.environ.get("CLAUDE_BRIDGE_HOST", "127.0.0.1"),
                        help="interface to listen on (default: only this computer, reach it through a tunnel)")
    parser.add_argument("--port", type=int, default=int(os.environ.get("CLAUDE_BRIDGE_PORT", 8787)))
    parser.add_argument("--authorized-keys", default=str(Path.home() / ".claude-bridge" / "authorized_keys"),
                        help="public keys of the clients allowed to connect, re-read on every change")
    parser.add_argument("--add-key", metavar="LINE",
                        help="authorize a client's public key line ('ed25519 <key> <name>') and exit")
    parser.add_argument("--model", default="sonnet", help="model when a request names none")
    parser.add_argument("--no-web", action="store_true", help="never allow web research, even when a client asks")
    parser.add_argument("--web-domains", default=",".join(WEB_DOMAINS),
                        help="comma separated domains Claude may read pages from (searching is always allowed)")
    parser.add_argument("--parallel", type=int, default=3, help="max claude processes at once")
    parser.add_argument("--queue-timeout", type=int, default=60, help="seconds a request waits for a free slot")
    parser.add_argument("--timeout", type=int, default=180, help="seconds a single claude call may take")
    parser.add_argument("--workspaces", default=str(Path.home() / ".claude-bridge" / "workspaces"),
                        help="folder for per-client working directories (session history lives with them)")
    parser.add_argument("--claude", default=shutil.which("claude"), help="path to the claude executable")
    args = parser.parse_args()

    args.web_domains = [domain.strip() for domain in args.web_domains.split(",") if domain.strip()]
    authorized_keys = AuthorizedKeys(args.authorized_keys)
    if args.add_key:
        try:
            print(authorized_keys.add(args.add_key))
        except ValueError as error:
            parser.error(str(error))
        return
    if not args.claude:
        parser.error("claude not found on PATH, pass --claude")

    bridge = Bridge(args)
    server = Server((args.host, args.port), make_handler(bridge, authorized_keys))
    print(f"Claude Bridge {__version__} on http://{args.host}:{args.port} using {args.claude}", flush=True)
    authorized_keys.reload_if_changed()
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
