# claude-http-bridge

Macht die [Claude Code](https://claude.com/claude-code) CLI auf deinem Rechner per HTTP erreichbar.
Damit können Plugins auf einem Server, der die CLI selbst nicht ausführen kann (z.B. ein gemieteter
Nitrado-Minecraft-Server), das Konto nutzen, mit dem die CLI hier eingeloggt ist, etwa dein Claude-Abo.
Eine einzelne Datei für Windows, macOS und Linux, ohne Installation und ohne Abhängigkeiten.

```
Server (Plugin, privater Schlüssel)  --HTTPS, signiert-->  Tunnel  -->  claude-bridge  -->  claude --print
                                                                         authorized_keys
```

**Benutzt von:** [claude-chat-plugin](https://github.com/MaxXLive/claude-chat-plugin) (`/v1/chat`),
einem Claude-Chat für den Minecraft-Chat, sowie von Neurofolk, KI-Dorfbewohnern für Minecraft (`/v1/run`).

## Schnellstart

1. Die Claude CLI installieren und einmal mit `claude` → `/login` anmelden.
2. Die Datei für dein System aus den [Releases](https://github.com/MaxXLive/claude-http-bridge/releases) laden:

   | System | Datei |
   |---|---|
   | Windows | `claude-bridge-windows-amd64.exe` (ARM-Geräte: `…-arm64.exe`) |
   | macOS mit Apple-Chip (M1 …) | `claude-bridge-darwin-arm64` |
   | macOS mit Intel | `claude-bridge-darwin-amd64` |
   | Linux | `claude-bridge-linux-amd64` (Raspberry Pi & Co.: `…-arm64`) |

   Am besten in `claude-bridge` (Windows: `claude-bridge.exe`) umbenennen und an einen festen Ort legen,
   z.B. `C:\Tools\` oder `~/bin/`.
3. Starten und als Autostart einrichten:
   ```sh
   claude-bridge install
   ```
   Die Bridge läuft jetzt und startet bei jeder Anmeldung von selbst. Optionen hinter `install` gelten auch für
   den Autostart, z.B. `claude-bridge install --port 8800 --parallel 5`.
4. Einen [Tunnel](#tunnel) einrichten, damit der Server die Bridge erreicht.
5. Den Schlüssel des Plugins eintragen, siehe [Plugin freischalten](#plugin-freischalten).

Nur ausprobieren, ohne Autostart: `claude-bridge` startet sie im Terminal, `Strg+C` beendet sie.

**Beim ersten Start:**
- **Windows** warnt eventuell („Der Computer wurde durch Windows geschützt“), weil die Datei nicht signiert ist:
  „Weitere Informationen“ → „Trotzdem ausführen“.
- **macOS** blockiert heruntergeladene, unsignierte Programme. Einmal ausführen:
  `chmod +x claude-bridge && xattr -d com.apple.quarantine claude-bridge`
- **Linux:** `chmod +x claude-bridge`

## Home Assistant

Die Bridge gibt es auch als Home-Assistant-Add-on (amd64 und aarch64, z.B. Home Assistant Green/Yellow oder
Raspberry Pi 4/5). Dann läuft sie rund um die Uhr, ohne dass ein PC an sein muss.

1. **Einstellungen → Add-ons → Add-on Store → ⋮ → Repositories** und
   `https://github.com/MaxXLive/claude-http-bridge` hinzufügen.
2. **Claude Bridge** installieren. Das Bauen dauert beim ersten Mal ein paar Minuten.
3. Auf einem Rechner mit Claude CLI `claude setup-token` ausführen und das Token in der Konfiguration unter
   **OAuth-Token** eintragen. Die Schlüssel der Plugins kommen unter **Zugelassene Schlüssel** in die Liste
   (statt `add-key`).
4. Starten. Die Bridge lauscht auf Port 8787 des Home-Assistant-Rechners. Für Server im Internet braucht es
   wie oben eine feste Adresse, z.B. über das Cloudflared-Add-on.

Alles Weitere steht in der [Add-on-Doku](claude_bridge/DOCS.md). Die Add-on-Version ist die Bridge-Version
(`version` in [`claude_bridge/config.yaml`](claude_bridge/config.yaml)), das Add-on lädt beim Bauen das passende
Release.

## Befehle

| Befehl | |
|---|---|
| `claude-bridge` oder `claude-bridge serve` | Bridge starten (mit den [Optionen](#optionen)) |
| `claude-bridge add-key "ed25519 … name"` | Schlüssel eines Plugins eintragen (die Zeile aus dessen Log) |
| `claude-bridge install [optionen]` | bei jeder Anmeldung starten und gleich jetzt starten |
| `claude-bridge uninstall` | Autostart entfernen |
| `claude-bridge version` | Version und API-Version anzeigen |

`install` richtet je nach System ein:

| System | Autostart | Log |
|---|---|---|
| Windows | Skript im Autostart-Ordner, öffnet ein minimiertes Fenster | im Fenster, oder mit `--log-file` in eine Datei |
| macOS | launchd-Agent, startet bei Absturz neu | `~/Library/Logs/claude-bridge.log` |
| Linux | systemd-User-Service, startet bei Absturz neu | `journalctl --user -u claude-bridge -f` |

Unter Linux läuft der Service nur, solange du angemeldet bist. Für einen Server ohne Anmeldung einmal
`sudo loginctl enable-linger $USER` ausführen.

`install` merkt sich den Pfad der Claude CLI, damit der Autostart sie auch ohne passenden `PATH` findet.
Nach einem Update der Bridge die neue Datei über die alte kopieren und die Bridge neu starten (Windows:
Fenster schließen, dann einmal abmelden oder `claude-bridge install` erneut ausführen).

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

## Plugin freischalten

1. Plugin auf dem Server starten. Es legt `plugins/<Plugin>/bridge_key` und `bridge_key.pub` an und
   schreibt die fertige Zeile ins Server-Log:
   ```
   claude-bridge add-key "ed25519 MCowBQYDK2VwAyEA... claudechat@server"
   ```
2. Diese Zeile auf dem Bridge-Rechner ausführen. Die laufende Bridge übernimmt den Schlüssel sofort.

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
   Einstellung, sie bleibt auch nach Neustarts aktiv. Alternativ startet `claude-bridge --funnel` den
   Befehl beim Start der Bridge selbst.
3. Der Befehl zeigt deine feste Adresse an, z.B. `https://mein-pc.tail1234.ts.net`. Diese Adresse
   (ohne Pfad) kommt in die Plugin-Config als `bridge.url`.
4. Test von außen, z.B. am Handy ohne WLAN: `https://mein-pc.tail1234.ts.net/health` liefert
   `{"ok":true, ...}`.

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
- Für ständige Erreichbarkeit: `claude-bridge install` und den Standby des Rechners abschalten.

## Fehlersuche

| Symptom | Ursache |
|---|---|
| Plugin-Log: `401 unauthorized` | Schlüssel nicht mit `add-key` eingetragen, oder die Uhr von Server und Rechner liegt mehr als 2 Minuten auseinander |
| Plugin-Log: Verbindungsfehler oder `502` | Bridge nicht gestartet, Rechner im Standby oder Tunnel aus (`tailscale funnel status`) |
| Plugin-Log: `offers API 1, … needs API 2` | Bridge zu alt, neue Version aus den Releases laden (siehe [Versionen](#versionen-und-api)) |
| Plugin-Log: `426` oder `update the client` | Plugin zu alt für diese Bridge, Plugin aktualisieren |
| `/health` von außen nicht erreichbar | Funnel nicht freigeschaltet oder `tailscale funnel --bg 8787` nicht ausgeführt |
| Bridge zeigt `0 authorized key(s)` | `add-key` mit einer anderen `--authorized-keys`-Datei oder einer unvollständigen Zeile ausgeführt |
| `can't listen on 127.0.0.1:8787` | Es läuft schon eine Bridge auf dem Port, etwa die alte Python-Version oder der Autostart |
| `503 busy` | mehr gleichzeitige Anfragen als `--parallel`, länger als `--queue-timeout` gewartet |

## Optionen

| Option | Standard | |
|---|---|---|
| `--host` | `127.0.0.1` | Interface (`CLAUDE_BRIDGE_HOST`) |
| `--port` | `8787` | (`CLAUDE_BRIDGE_PORT`) |
| `--authorized-keys` | `~/.claude-bridge/authorized_keys` | zugelassene Schlüssel, wird bei Änderung neu gelesen |
| `--model` | `sonnet` | wenn die Anfrage keins nennt |
| `--no-web` | aus | Web-Recherche nie erlauben, auch wenn ein Client sie anfragt |
| `--web-domains` | Minecraft-Seiten, siehe oben | kommagetrennt, Subdomains brauchen eigene Einträge, `*` = alle |
| `--parallel` | `3` | gleichzeitige CLI-Prozesse |
| `--queue-timeout` | `60` | Sekunden, die eine Anfrage auf einen freien Platz wartet, danach 503 |
| `--timeout` | `180` | Sekunden pro CLI-Aufruf |
| `--workspaces` | `~/.claude-bridge/workspaces` | Arbeitsordner der Clients, dort liegt auch die Session-Historie |
| `--claude` | `claude` aus dem PATH | Pfad zur CLI |
| `--funnel` | aus | beim Start `tailscale funnel --bg <port>` ausführen |
| `--log-file` | | das Log zusätzlich in diese Datei schreiben |

## Versionen und API

Die Bridge hat zwei Versionsnummern:

- **Release** (`2.0.3`): wie die Datei gebaut wurde. Steht in `claude-bridge version`, unter `/health` und im
  Header `X-Bridge-Version` jeder Antwort.
- **API** (`2`): was Clients von der Bridge erwarten können. Sie steigt, wenn etwas dazukommt, das Clients
  brauchen, und steht unter `/health` (`api`) und im Header `X-Bridge-Api`. Dazu kommt `api_min`: die älteste API,
  die die Bridge noch annimmt. Sie steigt nur bei Änderungen, die alte Clients brechen.

| API | ab Bridge | |
|---|---|---|
| 1 | 1.0 | `/v1/chat`, `/v1/run`, `/health` |
| 2 | 1.3 | `/v1/chat` liefert zusätzlich den Token-Verbrauch |

Clients kennen die API, die sie selbst sprechen (ClaudeChat: 2, Neurofolk: 1), und vergleichen sie beim Start
und danach regelmäßig mit `/health`:

- Client-API größer als `api`: Die **Bridge ist zu alt**. Der Client sagt das im Log, mit Link zu den Releases.
- Client-API kleiner als `api_min`: Der **Client ist zu alt**.

Zusätzlich schicken Clients ihre API im Header `X-Bridge-Api`. Kann die Bridge sie nicht bedienen, antwortet sie
mit `426` und erklärt im Fehlertext, was zu aktualisieren ist. Ältere Bridges ohne `api`-Feld ordnen Clients über
die Release-Nummer ein (1.3 = API 2, älter = API 1).

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

Ohne Anmeldung:
```json
{"ok": true, "name": "claude-http-bridge", "version": "2.0.0", "api": 2, "api_min": 1,
 "features": ["chat", "chat.usage", "run", "web"]}
```

### Anmeldung für eigene Clients

Schlüsselformat: Ed25519, öffentlicher Schlüssel als Base64 von DER/X.509 (`SubjectPublicKeyInfo`).
In Java ist das `publicKey.getEncoded()`.

Jede Anfrage trägt diese Header:

| Header | Inhalt |
|---|---|
| `X-Bridge-Key` | öffentlicher Schlüssel (Base64, wie in `authorized_keys`) |
| `X-Bridge-Timestamp` | Unix-Zeit in Sekunden |
| `X-Bridge-Nonce` | Zufallswert, 16–64 Zeichen, pro Anfrage neu |
| `X-Bridge-Signature` | Base64 der Ed25519-Signatur über den folgenden Text |
| `X-Bridge-Api` | optional: die API-Version, die der Client spricht (siehe oben) |

```
POST\n<pfad, z.B. /v1/chat>\n<timestamp>\n<nonce>\n<sha256 des bodys als hex, kleingeschrieben>
```

Fertige Java-Implementierung (nur JDK, keine Abhängigkeiten):
[`BridgeAuth.java`](https://github.com/MaxXLive/claude-chat-plugin/blob/main/src/main/java/com/maxxlive/claudechat/backend/BridgeAuth.java)
erzeugt und speichert das Schlüsselpaar und signiert `HttpRequest`s,
[`BridgeVersion.java`](https://github.com/MaxXLive/claude-chat-plugin/blob/main/src/main/java/com/maxxlive/claudechat/backend/BridgeVersion.java)
prüft die API-Version. Ein Anwendungsbeispiel für `/v1/chat` ist
[`BridgeBackend.java`](https://github.com/MaxXLive/claude-chat-plugin/blob/main/src/main/java/com/maxxlive/claudechat/backend/BridgeBackend.java).
Für `/v1/run`: Antwort als Stream lesen (`BodyHandlers.ofInputStream()`), Zeile für Zeile parsen und zum
Abbrechen den Stream schließen.

## Releases

Releases baut [GitHub Actions](.github/workflows/build.yml) bei jedem Push auf `main`: Tests, Binaries für
Windows, macOS und Linux (x64 und ARM), `SHA256SUMS.txt` und ein GitHub-Release mit Changelog. Major und Minor
stehen in [`VERSION`](VERSION), die Patch-Nummer zählt das Skript
[`.github/scripts/release-info.sh`](.github/scripts/release-info.sh) aus den Tags hoch (`v2.0.0`, `v2.0.1`, …).
Pull Requests werden nur gebaut, Pushes mit ausschließlich `docs:`/`ci:`/`test:`/`style:`-Commits ergeben kein
Release, Änderungen nur an `*.md`-Dateien gar keinen Build.

Selbst bauen (Go 1.25):
```sh
go test ./...
go build -o claude-bridge .
```

## Python-Version

Bis 1.3 war die Bridge ein Python-Skript. Es liegt weiter im Branch
[`python`](https://github.com/MaxXLive/claude-http-bridge/tree/python) und unter den Tags `python-1.3.0` und
`python-1.3.1`. Die Go-Version ist ein vollständiger Ersatz: gleiche Endpunkte, gleiches Schlüsselformat, gleiche
`authorized_keys` und gleiche Optionen. Zum Umstieg die alte Bridge beenden (Autostart-Verknüpfung, launchd-plist
bzw. systemd-Unit entfernen) und `claude-bridge install` ausführen. Die eingetragenen Schlüssel bleiben gültig.

## Lizenz

[MIT](LICENSE)
