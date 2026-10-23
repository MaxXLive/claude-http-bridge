# Claude Bridge

Macht die Claude Code CLI per HTTP erreichbar, damit Plugins auf einem Server, der die CLI nicht selbst ausführen
kann (z.B. ein gemieteter Minecraft-Server), dein Claude-Abo nutzen können. Details zu Sicherheit, API und Plugins:
[README](https://github.com/MaxXLive/claude-http-bridge#readme).

## Einrichten

1. **Token erzeugen:** Auf einem Rechner mit installierter Claude CLI `claude setup-token` ausführen und dem Link
   folgen. Das ausgegebene Token (`sk-ant-oat01-…`) in der Konfiguration unter **OAuth-Token** eintragen.
   Es gilt ein Jahr.
2. **Plugin freischalten:** Das Plugin schreibt beim ersten Start eine Zeile ins Server-Log:
   ```
   claude-bridge add-key "ed25519 MCowBQYDK2VwAyEA... claudechat@server"
   ```
   Diese Zeile (oder nur den Teil in Anführungszeichen) unter **Zugelassene Schlüssel** eintragen.
   Zum Entziehen den Eintrag wieder löschen.
3. Add-on speichern und (neu) starten. Im Log stehen danach die zugelassenen Schlüssel.

## Erreichbarkeit

Die Bridge lauscht auf Port **8787** des Home-Assistant-Rechners. Im Heimnetz ist sie damit unter
`http://homeassistant.local:8787` erreichbar (Test: `/health` liefert `{"ok":true, ...}`).

Ein Server im Internet braucht eine feste öffentliche Adresse, z.B. über:

- **Cloudflared-Add-on** mit eigener Domain: einen zusätzlichen Host anlegen, der auf
  `http://<IP des Home-Assistant-Rechners>:8787` zeigt.
- **Portweiterleitung** am Router auf Port 8787, am besten mit HTTPS davor (z.B. NGINX-Add-on).

Diese Adresse (ohne Pfad) kommt in die Plugin-Config als `bridge.url`. Alle Anfragen außer `/health` müssen mit
einem zugelassenen Schlüssel signiert sein, eine offene Portfreigabe gibt also niemandem Zugriff auf dein Abo.
Die Uhren von Server und Home Assistant dürfen höchstens 2 Minuten auseinandergehen.

## Daten

Im Add-on-Ordner `/data` liegen die aus der Konfiguration erzeugte `authorized_keys`, die Arbeitsordner der Clients
(mit der Session-Historie von `/v1/run`) und die Einstellungen der CLI. Sie bleiben bei Updates erhalten und sind
Teil der Home-Assistant-Backups.

## Updates

Die Add-on-Version ist die Version der Bridge. Die Claude CLI wird beim Bauen des Add-ons in der aktuellen
`stable`-Version installiert und danach nicht selbst aktualisiert. Ein Update des Add-ons bringt beides auf
den neuesten Stand.
