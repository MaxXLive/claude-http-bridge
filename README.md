# claude-http-bridge

Macht die [Claude Code](https://claude.com/claude-code) CLI auf deinem Rechner per HTTP erreichbar.
Damit können Plugins auf einem Server, der die CLI selbst nicht ausführen kann (z.B. ein gemieteter
Nitrado-Minecraft-Server), das Konto nutzen, mit dem die CLI hier eingeloggt ist, etwa dein Claude-Abo.
Läuft unter Windows, macOS und Linux.

```
Server (Plugin, privater Schlüssel)  --HTTPS, signiert-->  Tunnel  -->  claude_bridge.py  -->  claude --print
                                                                         authorized_keys
```

**Benutzt von:** [claude-chat-plugin](https://github.com/MaxXLive/claude-chat-plugin) (`/v1/chat`),
einem Claude-Chat für den Minecraft-Chat, sowie von Neurofolk, KI-Dorfbewohnern für Minecraft (`/v1/run`).

## Sicherheit

- **Anmeldung wie bei SSH:** Jedes Plugin erzeugt beim ersten Start ein eigenes Ed25519-Schlüsselpaar.
  Der private Schlüssel (`bridge_key`) bleibt auf dem Server. Die Bridge kennt nur die öffentlichen Schlüssel
  in ihrer `authorized_keys`. Jede Anfrage ist signiert, inklusive Zeitstempel, Einmalwert und Hash des Inhalts.
  Eine mitgeschnittene Anfrage lässt sich also weder verändern noch wiederholen.
  Gelangt die `authorized_keys` in falsche Hände, bekommt damit trotzdem niemand Zugriff.
- **Zugriff entziehen:** Zeile aus `authorized_keys` löschen. Änderungen gelten sofort, ohne Neustart.
- Claude läuft **ohne lokale Tools** (`--tools ""`), ohne MCP-Server und im `--safe-mode` (keine CLAUDE.md,
  Plugins, Hooks oder Skills). Es kann auf deinem Rechner also nichts ausführen, lesen oder schreiben,
  sondern nur Text zurückgeben.
- **Web-Recherche** (nur wenn der Client `"web": true` schickt und die Bridge nicht mit `--no-web` läuft):
  Dann darf Claude im Web suchen. Seiten lesen darf es aber nur von Domains der Liste `--web-domains`.
  Standard ist die erweiterte Minecraft-Welt: Minecraft Wiki, minecraft.net, Mojang-Bugtracker,
  Mod-, Plugin- und Datapack-Seiten (Modrinth, CurseForge, Fabric, NeoForge, Forge, PaperMC/Hangar,
  SpigotMC, Bukkit, GitHub), Mod-Wikis (FTB, Create, AE2, Mekanism), Reddit, Planet Minecraft,
  Minecraft Forum, YouTube und Tools wie Chunkbase und Misode. Andere Seiten lehnt die CLI ab
  (`--permission-mode dontAsk`). So kann Claude Daten aus dem Gespräch nicht an beliebige Adressen schicken.
  `--web-domains "*"` erlaubt alle Seiten. Das ist bequemer, die Schutzwirkung fällt dann aber weg.
- Die Bridge lauscht standardmäßig nur auf `127.0.0.1`. Von außen erreichbar ist sie nur über einen
  Tunnel, den du bewusst startest.
- Höchstens `--parallel` CLI-Prozesse laufen gleichzeitig, jeder darf maximal `--timeout` Sekunden laufen.
- Die Uhren von Server und Bridge-Rechner dürfen höchstens 2 Minuten auseinandergehen (NTP reicht).

Alle Anfragen verbrauchen dein Abo-Kontingent, dasselbe, das du selbst in Claude nutzt. Das Abo ist für
deine persönliche Nutzung gedacht. Lässt du andere Spieler darüber chatten, ist das mindestens eine
Grauzone. Für einen öffentlichen Server ist ein API-Key die saubere Lösung.

## Voraussetzungen (alle Systeme)

- Python 3.10 oder neuer
- Die Claude Code CLI, einmal mit `claude` → `/login` eingeloggt (auf demselben Benutzerkonto, unter
  dem die Bridge läuft)
- Ein Tunnel-Programm, siehe [Tunnel](#tunnel)

## Installation

### Windows

```powershell
cd C:\Users\<du>\Projects\ClaudeBridge
python -m venv .venv
.venv\Scripts\pip install -r requirements.txt
.venv\Scripts\python claude_bridge.py
```

**Autostart:** `Win+R` → `shell:startup` öffnen und dort eine Verknüpfung zu `start_bridge.bat` anlegen.
Die Bridge startet dann beim Anmelden in einem eigenen Fenster.

### macOS

```sh
cd ~/ClaudeBridge
python3 -m venv .venv
.venv/bin/pip install -r requirements.txt
./start_bridge.sh
```

**Autostart (launchd):**
```sh
sed "s/YOURNAME/$(whoami)/g" service/com.claudebridge.plist > ~/Library/LaunchAgents/com.claudebridge.plist
launchctl load ~/Library/LaunchAgents/com.claudebridge.plist
tail -f ~/Library/Logs/claude-bridge.log    # Log
launchctl unload ~/Library/LaunchAgents/com.claudebridge.plist   # stoppen
```
Die plist geht davon aus, dass das Projekt unter `~/ClaudeBridge` liegt und `claude` in `~/.local/bin`
oder Homebrew installiert ist. Sonst die Pfade in der Datei anpassen (`which claude`).

### Linux

```sh
cd ~/ClaudeBridge
python3 -m venv .venv        # Debian/Ubuntu ggf. vorher: sudo apt install python3-venv
.venv/bin/pip install -r requirements.txt
./start_bridge.sh
```

**Autostart (systemd, als dein Benutzer):**
```sh
mkdir -p ~/.config/systemd/user
cp service/claude-bridge.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now claude-bridge
journalctl --user -u claude-bridge -f      # Log
sudo loginctl enable-linger $USER          # auch ohne Anmeldung laufen lassen (z.B. Headless-Server)
```
Der Service geht davon aus, dass das Projekt unter `~/ClaudeBridge` liegt und `claude` in `~/.local/bin`
installiert ist. Sonst die Pfade in der Datei anpassen (`which claude`).

## Plugin freischalten

1. Plugin auf dem Server starten. Es legt `plugins/<Plugin>/bridge_key` und `bridge_key.pub` an und
   schreibt die fertige Zeile ins Server-Log:
   ```
   python claude_bridge.py --add-key "ed25519 MCowBQYDK2VwAyEA... claudechat@server"
   ```
2. Diese Zeile auf dem Bridge-Rechner im Bridge-Ordner ausführen (mit der venv: `.venv/bin/python` bzw.
   `.venv\Scripts\python`). Die laufende Bridge übernimmt den Schlüssel sofort.

Die Schlüssel stehen in `~/.claude-bridge/authorized_keys` (Windows: `C:\Users\<du>\.claude-bridge\authorized_keys`),
ein Schlüssel pro Zeile: `ed25519 <schlüssel> <name>`. Zeilen mit `#` sind Kommentare.

Wird ein Server neu aufgesetzt, entsteht ein neuer Schlüssel. Dann die alte Zeile löschen und die neue eintragen.

## Tunnel

Die Bridge selbst lauscht nur lokal. Erreichbar wird sie über einen Tunnel. Für den Dauerbetrieb
brauchst du eine **feste Adresse**: Sonst zeigt die Plugin-Config nach jedem Neustart ins Leere.

| | feste URL | Windows | macOS | Linux |
|---|---|---|---|---|
| **Tailscale Funnel** (empfohlen, kostenlos, keine Domain nötig) | ja | `winget install Tailscale.Tailscale` | `brew install tailscale` oder App Store | `curl -fsSL https://tailscale.com/install.sh \| sh` |
| **Cloudflare Named Tunnel** (eigene Domain bei Cloudflare nötig) | ja | `winget install Cloudflare.cloudflared` | `brew install cloudflared` | [Paket von Cloudflare](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/) |
| **Cloudflare Quick Tunnel** (nur zum Ausprobieren) | nein, neu bei jedem Start | wie oben | wie oben | wie oben |

### Tailscale Funnel einrichten

1. Tailscale installieren, starten und mit einem (kostenlosen) Account anmelden.
2. Funnel dauerhaft einschalten:
   ```sh
   tailscale funnel --bg 8787
   ```
   Beim ersten Mal gibt der Befehl einen Link aus, über den du HTTPS und Funnel für dein Tailnet
   freischaltest. Link öffnen, bestätigen, den Befehl nochmal ausführen. `--bg` speichert die
   Einstellung, sie bleibt auch nach Neustarts aktiv.
3. Der Befehl zeigt deine feste Adresse an, z.B. `https://mein-pc.tail1234.ts.net`. Diese Adresse
   (ohne Pfad) kommt in die Plugin-Config als `bridge.url`.
4. Test von außen, z.B. am Handy ohne WLAN: `https://mein-pc.tail1234.ts.net/health` liefert
   `{"ok": true, ...}`.

`tailscale funnel status` zeigt, ob der Funnel läuft. `tailscale funnel --https=443 off` schaltet ihn ab.

### Cloudflare

```sh
cloudflared tunnel --url http://localhost:8787   # Quick Tunnel -> https://….trycloudflare.com (zufällig)
```
Für eine feste Adresse unter deiner Domain: `cloudflared tunnel login`, `cloudflared tunnel create claude-bridge`,
`cloudflared tunnel route dns claude-bridge bridge.deine-domain.de` und anschließend
`cloudflared tunnel run --url http://localhost:8787 claude-bridge`.

## Dauerbetrieb und Ausfälle

- Jede Anfrage ist eine eigene HTTP-Verbindung. Ist dein Rechner aus oder im Standby, bekommen die Plugins
  einen Fehler (ClaudeChat zeigt „Claude ist gerade nicht erreichbar“). Sobald Bridge und Tunnel wieder
  laufen, funktioniert die nächste Anfrage automatisch, ohne Neustart oder Reload auf dem Server.
- Eine Nachricht, die während des Ausfalls kam, wird nicht wiederholt. Der Spieler muss sie nochmal schreiben.
- Für ständige Erreichbarkeit: Autostart einrichten (siehe Installation) und den Standby des Rechners abschalten.

## Fehlersuche

| Symptom | Ursache |
|---|---|
| Plugin-Log: `401 unauthorized` | Schlüssel nicht mit `--add-key` eingetragen, oder die Uhr von Server und Rechner liegt mehr als 2 Minuten auseinander |
| Plugin-Log: Verbindungsfehler oder `502` | Bridge nicht gestartet, Rechner im Standby oder Tunnel aus (`tailscale funnel status`) |
| `/health` von außen nicht erreichbar | Funnel nicht freigeschaltet oder `tailscale funnel --bg 8787` nicht ausgeführt |
| Bridge zeigt `0 authorized key(s)` | `--add-key` mit einer anderen `--authorized-keys`-Datei oder einer unvollständigen Zeile ausgeführt |
| `503 busy` | mehr gleichzeitige Anfragen als `--parallel`, länger als `--queue-timeout` gewartet |

## Optionen

| Option | Standard | |
|---|---|---|
| `--host` | `127.0.0.1` | Interface (`CLAUDE_BRIDGE_HOST`) |
| `--port` | `8787` | (`CLAUDE_BRIDGE_PORT`) |
| `--authorized-keys` | `~/.claude-bridge/authorized_keys` | zugelassene Schlüssel, wird bei Änderung neu gelesen |
| `--add-key "<zeile>"` | | Schlüssel eintragen und beenden |
| `--model` | `sonnet` | wenn die Anfrage keins nennt |
| `--no-web` | aus | Web-Recherche nie erlauben, auch wenn ein Client sie anfragt |
| `--web-domains` | Minecraft-Seiten, siehe oben | kommagetrennt, Subdomains brauchen eigene Einträge, `*` = alle |
| `--parallel` | `3` | gleichzeitige CLI-Prozesse |
| `--queue-timeout` | `60` | Sekunden, die eine Anfrage auf einen freien Platz wartet, danach 503 |
| `--timeout` | `180` | Sekunden pro CLI-Aufruf |
| `--workspaces` | `~/.claude-bridge/workspaces` | Arbeitsordner der Clients, dort liegt auch die Session-Historie |
| `--claude` | `claude` aus dem PATH | Pfad zur CLI |

## API

Alle Endpunkte außer `/health` brauchen eine Signatur (siehe [Anmeldung](#anmeldung-für-eigene-clients)).
Modelle: `sonnet`, `opus`, `haiku` oder eine volle ID wie `claude-sonnet-5-5`.

### `POST /v1/chat`: zustandsloser Chat

Für Clients, die den Verlauf selbst verwalten. Der Verlauf wird als Transkript in einen Prompt geschrieben.
Es bleibt keine Session zurück.

```json
{"model": "sonnet", "system": "Du bist ...", "web": true, "messages": [
  {"role": "user", "content": "..."}, {"role": "assistant", "content": "..."}, {"role": "user", "content": "..."}
]}
```
→ `200 {"text": "...", "usage": {"input_tokens": 0, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0, "output_tokens": 0}}`
(Token-Verbrauch laut CLI, über alle Recherche-Schritte), sonst `4xx/5xx {"error": "..."}`. Die letzte Nachricht muss vom `user` sein.
`web` ist optional (siehe Sicherheit). Mit Recherche dauert eine Antwort meist 10–30 s.

### `POST /v1/run`: CLI mit Streaming und Sessions

Gibt die `stream-json`-Ausgabe der CLI (`--output-format stream-json --verbose`) Zeile für Zeile
unverändert zurück (NDJSON, chunked). Clients, die bisher `claude` selbst gestartet haben, können
deshalb ihren Parser behalten.

```json
{"prompt": "...", "system": "...", "model": "haiku", "max_turns": 1, "web": false,
 "workspace": "neurofolk/agents/Bob", "session_id": "7a11dfbe-..."}
```

- `workspace`: Ordner unter `--workspaces`, Segmente aus `A-Z a-z 0-9 . _ -`, getrennt durch `/`.
  Die CLI speichert Sessions pro Ordner. Ein `--resume` funktioniert darum nur im selben Workspace.
- `session_id`: optional, setzt die Session fort (`session_id` steht in der `system`- und `result`-Zeile).
- `max_turns`: 1–50, Standard 1. Für `web` braucht es mehrere Turns (Suche, Lesen, Antwort).
- Die letzte Zeile ist immer `{"type": "bridge_exit", "code": <exit code der CLI>}`.
- Trennt der Client die Verbindung, beendet die Bridge den CLI-Prozess (Abbrechen).

### `GET /health`

`{"ok": true, "version": "1.3.0"}`, ohne Anmeldung.

### Anmeldung für eigene Clients

Schlüsselformat: Ed25519, öffentlicher Schlüssel als Base64 von DER/X.509 (`SubjectPublicKeyInfo`).
In Java ist das `publicKey.getEncoded()`.

Jede Anfrage trägt vier Header:

| Header | Inhalt |
|---|---|
| `X-Bridge-Key` | öffentlicher Schlüssel (Base64, wie in `authorized_keys`) |
| `X-Bridge-Timestamp` | Unix-Zeit in Sekunden |
| `X-Bridge-Nonce` | Zufallswert, 16–64 Zeichen, pro Anfrage neu |
| `X-Bridge-Signature` | Base64 der Ed25519-Signatur über den folgenden Text |

```
POST\n<pfad, z.B. /v1/chat>\n<timestamp>\n<nonce>\n<sha256 des bodys als hex, kleingeschrieben>
```

Fertige Java-Implementierung (nur JDK, keine Abhängigkeiten):
[`BridgeAuth.java`](https://github.com/MaxXLive/claude-chat-plugin/blob/main/src/main/java/com/maxxlive/claudechat/backend/BridgeAuth.java).
Sie erzeugt und speichert das Schlüsselpaar und signiert `HttpRequest`s. Ein Anwendungsbeispiel für `/v1/chat` ist
[`BridgeBackend.java`](https://github.com/MaxXLive/claude-chat-plugin/blob/main/src/main/java/com/maxxlive/claudechat/backend/BridgeBackend.java).
Für `/v1/run`: Antwort als Stream lesen (`BodyHandlers.ofInputStream()`), Zeile für Zeile parsen und zum
Abbrechen den Stream schließen.

## Lizenz

[MIT](LICENSE)
