package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	modelPattern     = regexp.MustCompile(`^(sonnet|opus|haiku|claude-[a-z0-9-]+)$`)
	sessionPattern   = regexp.MustCompile(`^[0-9a-fA-F-]{36}$`)
	workspacePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
)

// webDomains are the sites Claude may read during web research (subdomains need their own entry): the wider
// Minecraft world, i.e. game, mods, plugins, datapacks, farms and tools. "*" in --web-domains allows every site.
var webDomains = []string{
	// Game and wikis
	"minecraft.wiki", "minecraft.fandom.com", "minecraft.net", "www.minecraft.net", "feedback.minecraft.net",
	"help.minecraft.net", "bugs.mojang.com", "report.bugs.mojang.com",
	// Mods, plugins, datapacks, modpacks
	"modrinth.com", "docs.modrinth.com", "curseforge.com", "www.curseforge.com", "legacy.curseforge.com",
	"fabricmc.net", "docs.fabricmc.net", "wiki.fabricmc.net", "neoforged.net", "docs.neoforged.net",
	"minecraftforge.net", "files.minecraftforge.net", "papermc.io", "docs.papermc.io", "hangar.papermc.io",
	"spigotmc.org", "www.spigotmc.org", "bukkit.org", "dev.bukkit.org", "github.com", "gist.github.com",
	"raw.githubusercontent.com", "ftb.fandom.com", "ftbwiki.org", "wiki.createmod.net", "create.fandom.com",
	"guide.appliedenergistics.org", "wiki.aidancbrady.com",
	// Community: farms, builds, how-tos
	"reddit.com", "www.reddit.com", "old.reddit.com", "planetminecraft.com", "www.planetminecraft.com",
	"minecraftforum.net", "www.minecraftforum.net", "youtube.com", "www.youtube.com", "techmcdocs.github.io",
	// Tools
	"chunkbase.com", "www.chunkbase.com", "misode.github.io", "mcstacker.net",
}

// usageKeys are the token counts of a /v1/chat reply, as reported by the CLI.
var usageKeys = []string{"input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "output_tokens"}

// clearedEnv is inherited from a parent Claude Code session and would change how the child CLI behaves.
var clearedEnv = []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT"}

// errBadRequest marks errors caused by the request, answered with 400.
var errBadRequest = errors.New("bad request")

var errTimeout = errors.New("claude timed out")

func badRequest(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errBadRequest, fmt.Sprintf(format, args...))
}

type Config struct {
	Host           string
	Port           int
	AuthorizedKeys string
	Model          string
	NoWeb          bool
	WebDomains     []string
	Parallel       int
	QueueTimeout   time.Duration
	Timeout        time.Duration
	Workspaces     string
	Claude         string
}

type Bridge struct {
	config Config
	slots  chan struct{}
}

func NewBridge(config Config) (*Bridge, error) {
	if err := os.MkdirAll(config.Workspaces, 0o700); err != nil {
		return nil, err
	}
	return &Bridge{config: config, slots: make(chan struct{}, max(1, config.Parallel))}, nil
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string    `json:"model"`
	System   string    `json:"system"`
	Web      bool      `json:"web"`
	Messages []message `json:"messages"`
}

type chatResponse struct {
	Text  string           `json:"text"`
	Usage map[string]int64 `json:"usage"`
}

type runRequest struct {
	Prompt    string   `json:"prompt"`
	System    string   `json:"system"`
	Model     string   `json:"model"`
	MaxTurns  *float64 `json:"max_turns"`
	Web       bool     `json:"web"`
	Workspace string   `json:"workspace"`
	SessionID string   `json:"session_id"`
}

func (b *Bridge) model(requested string) (string, error) {
	model := requested
	if model == "" {
		model = b.config.Model
	}
	if !modelPattern.MatchString(model) {
		return "", badRequest("invalid model: %q", model)
	}
	return model, nil
}

// web says whether a request gets web research: the client asks for it and the bridge allows it.
func (b *Bridge) web(requested bool) bool {
	return requested && !b.config.NoWeb
}

// workspace is a client's own folder; --resume only finds sessions started in the same folder.
func (b *Bridge) workspace(name string) (string, error) {
	path := filepath.Join(b.config.Workspaces, "_stateless")
	if name != "" {
		parts := strings.Split(strings.ReplaceAll(name, `\`, "/"), "/")
		for _, part := range parts {
			if !workspacePattern.MatchString(part) || part == "." || part == ".." {
				return "", badRequest("invalid workspace, use segments of A-Z a-z 0-9 . _ - separated by /")
			}
		}
		path = filepath.Join(append([]string{b.config.Workspaces}, parts...)...)
	}
	return path, os.MkdirAll(path, 0o700)
}

// baseCommand has the flags every request gets: no local tools, MCP servers or local config.
//
// With web, Claude may search the web and read pages of the allowed domains. Everything else would need a
// permission prompt, which dontAsk denies, so it can't send what it knows (e.g. player data) to arbitrary URLs.
func (b *Bridge) baseCommand(model, system, workdir string, web bool) ([]string, error) {
	command := []string{b.config.Claude, "--print", "--model", model, "--strict-mcp-config", "--safe-mode"}
	if web {
		command = append(command, "--tools", "WebSearch,WebFetch", "--permission-mode", "dontAsk", "--allowedTools", "WebSearch")
		if contains(b.config.WebDomains, "*") {
			command = append(command, "WebFetch")
		} else {
			for _, domain := range b.config.WebDomains {
				command = append(command, "WebFetch(domain:"+domain+")")
			}
		}
	} else {
		command = append(command, "--tools", "")
	}
	if system != "" {
		// As a file: Windows limits command lines to about 32k characters
		promptFile := filepath.Join(workdir, ".system-prompt.txt")
		if err := os.WriteFile(promptFile, []byte(system), 0o600); err != nil {
			return nil, err
		}
		command = append(command, "--system-prompt-file", promptFile)
	}
	return command, nil
}

func childEnv() []string {
	var env []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !contains(clearedEnv, name) {
			env = append(env, entry)
		}
	}
	return env
}

// renderTranscript writes the conversation so far into one prompt, because /v1/chat is stateless.
func renderTranscript(messages []message) string {
	history, last := messages[:len(messages)-1], messages[len(messages)-1]
	var parts []string
	if len(history) > 0 {
		parts = append(parts, "Conversation so far:")
		for _, entry := range history {
			tag := "user"
			if entry.Role == "assistant" {
				tag = "assistant"
			}
			parts = append(parts, fmt.Sprintf("<%s>\n%s\n</%s>", tag, entry.Content, tag))
		}
		parts = append(parts, "", "New message, reply to it:")
	}
	parts = append(parts, last.Content)
	return strings.Join(parts, "\n")
}

// Chat runs one stateless turn: the whole conversation in, the reply text and token usage out.
func (b *Bridge) Chat(ctx context.Context, request chatRequest) (chatResponse, error) {
	if len(request.Messages) == 0 || request.Messages[len(request.Messages)-1].Role != "user" {
		return chatResponse{}, badRequest("messages must be a non-empty list ending with a user message")
	}
	model, err := b.model(request.Model)
	if err != nil {
		return chatResponse{}, err
	}
	base, err := b.workspace("")
	if err != nil {
		return chatResponse{}, err
	}
	workdir, err := os.MkdirTemp(base, "chat-")
	if err != nil {
		return chatResponse{}, err
	}
	defer os.RemoveAll(workdir)

	web := b.web(request.Web)
	command, err := b.baseCommand(model, request.System, workdir, web)
	if err != nil {
		return chatResponse{}, err
	}
	command = append(command, "--output-format", "json", "--no-session-persistence")
	if web {
		// Room for a few searches and page reads before the answer
		command = append(command, "--max-turns", "10")
	}

	ctx, cancel := context.WithTimeout(ctx, b.config.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = workdir
	cmd.Env = childEnv()
	cmd.Stdin = strings.NewReader(renderTranscript(request.Messages))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return chatResponse{}, errTimeout
	}

	var output struct {
		Result  string         `json:"result"`
		IsError bool           `json:"is_error"`
		Usage   map[string]any `json:"usage"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		if detail == "" && runErr != nil {
			detail = runErr.Error()
		}
		return chatResponse{}, fmt.Errorf("claude exited with %d: %s", cmd.ProcessState.ExitCode(), detail)
	}
	if output.IsError {
		if output.Result == "" {
			output.Result = "claude reported an error"
		}
		return chatResponse{}, errors.New(output.Result)
	}
	usage := map[string]int64{}
	for _, key := range usageKeys {
		if value, ok := output.Usage[key].(float64); ok {
			usage[key] = int64(value)
		} else {
			usage[key] = 0
		}
	}
	return chatResponse{Text: output.Result, Usage: usage}, nil
}

// runCommand builds the CLI call for /v1/run: stream-json output, optionally resuming a session.
func (b *Bridge) runCommand(request runRequest) ([]string, string, error) {
	if request.Prompt == "" {
		return nil, "", badRequest("prompt is required")
	}
	model, err := b.model(request.Model)
	if err != nil {
		return nil, "", err
	}
	workdir, err := b.workspace(request.Workspace)
	if err != nil {
		return nil, "", err
	}
	command, err := b.baseCommand(model, request.System, workdir, b.web(request.Web))
	if err != nil {
		return nil, "", err
	}
	turns := 1
	if request.MaxTurns != nil {
		turns = min(max(int(*request.MaxTurns), 1), 50)
	}
	command = append(command, "--output-format", "stream-json", "--verbose", "--max-turns", fmt.Sprint(turns))
	if request.SessionID != "" {
		if !sessionPattern.MatchString(request.SessionID) {
			return nil, "", badRequest("invalid session_id")
		}
		command = append(command, "--resume", request.SessionID)
	}
	return command, workdir, nil
}

func contains(list []string, value string) bool {
	for _, entry := range list {
		if entry == value {
			return true
		}
	}
	return false
}
