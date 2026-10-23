package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const serviceName = "claude-bridge"

// install makes the bridge start at every login with the given serve options: a script in the Startup folder
// on Windows, a launchd agent on macOS, a systemd user service on Linux.
func install(args []string) error {
	o, err := parse(args)
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	serveArgs := append([]string{"serve"}, args...)
	if o.config.Claude == "" {
		// The autostart environment may lack the PATH entry of the CLI, so remember where it is now
		claude, err := exec.LookPath("claude")
		if err != nil {
			return errors.New("claude not found on PATH, install Claude Code or pass --claude <path>")
		}
		if absolute, err := filepath.Abs(claude); err == nil {
			claude = absolute
		}
		serveArgs = append(serveArgs, "--claude", claude)
	}
	portBusy := false
	if listener, err := net.Listen("tcp", net.JoinHostPort(o.config.Host, strconv.Itoa(o.config.Port))); err != nil {
		portBusy = true
	} else {
		listener.Close()
	}

	switch runtime.GOOS {
	case "windows":
		err = installWindows(executable, serveArgs, portBusy)
	case "darwin":
		err = installMac(executable, serveArgs)
	case "linux":
		err = installLinux(executable, serveArgs)
	default:
		err = fmt.Errorf("autostart isn't supported on %s, start the bridge yourself", runtime.GOOS)
	}
	if err == nil && portBusy && runtime.GOOS != "windows" {
		fmt.Printf("Port %d is busy: if an older bridge still runs, stop it; the service keeps retrying.\n", o.config.Port)
	}
	return err
}

func uninstall() error {
	switch runtime.GOOS {
	case "windows":
		path, err := windowsStartupScript()
		if err != nil {
			return err
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		fmt.Println("Removed " + path + ". A bridge that is running now keeps running until you close its window.")
	case "darwin":
		path := macAgentPath()
		_ = exec.Command("launchctl", "unload", "-w", path).Run()
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		fmt.Println("Stopped and removed " + path)
	case "linux":
		_ = exec.Command("systemctl", "--user", "disable", "--now", serviceName).Run()
		path := linuxUnitPath()
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
		fmt.Println("Stopped and removed " + path)
	default:
		return fmt.Errorf("autostart isn't supported on %s", runtime.GOOS)
	}
	return nil
}

// ---- Windows: a script in the Startup folder opens the bridge in a minimized window at login

func windowsStartupScript() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return "", errors.New("APPDATA is not set")
	}
	return filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Startup", serviceName+".cmd"), nil
}

func windowsQuote(arg string) string {
	if arg == "" || strings.ContainsAny(arg, " \t&|<>^%*\"") {
		return `"` + strings.ReplaceAll(arg, `"`, `""`) + `"`
	}
	return arg
}

func installWindows(executable string, args []string, portBusy bool) error {
	path, err := windowsStartupScript()
	if err != nil {
		return err
	}
	quoted := []string{windowsQuote(executable)}
	for _, arg := range args {
		quoted = append(quoted, windowsQuote(strings.ReplaceAll(arg, "%", "%%")))
	}
	script := "@echo off\r\n" +
		"rem Created by \"claude-bridge install\", remove with \"claude-bridge uninstall\"\r\n" +
		"start \"Claude Bridge\" /min " + strings.Join(quoted, " ") + "\r\n"
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		return err
	}
	fmt.Println("The bridge now starts at every login (minimized window): " + path)
	if portBusy {
		fmt.Println("A bridge already runs on this port, so none was started now. Close it and run the script once to switch.")
		return nil
	}
	if err := exec.Command("cmd", "/c", path).Run(); err != nil {
		return fmt.Errorf("installed, but starting it now failed: %w", err)
	}
	fmt.Println("Started it now as well.")
	return nil
}

// ---- macOS: a launchd agent, restarted when it stops

func macAgentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", "com.maxxlive."+serviceName+".plist")
}

func xmlEscape(text string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(text)
}

func installMac(executable string, args []string) error {
	home, _ := os.UserHomeDir()
	logFile := filepath.Join(home, "Library", "Logs", serviceName+".log")
	var arguments strings.Builder
	for _, arg := range append([]string{executable}, args...) {
		arguments.WriteString("\t\t<string>" + xmlEscape(arg) + "</string>\n")
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.maxxlive.` + serviceName + `</string>
	<key>ProgramArguments</key>
	<array>
` + arguments.String() + `	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>` + xmlEscape(os.Getenv("PATH")) + `</string>
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<key>StandardOutPath</key>
	<string>` + xmlEscape(logFile) + `</string>
	<key>StandardErrorPath</key>
	<string>` + xmlEscape(logFile) + `</string>
</dict>
</plist>
`
	path := macAgentPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	_ = exec.Command("launchctl", "unload", path).Run()
	if err := os.WriteFile(path, []byte(plist), 0o644); err != nil {
		return err
	}
	if output, err := exec.Command("launchctl", "load", "-w", path).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl load failed: %v %s", err, output)
	}
	fmt.Println("The bridge runs now and starts at every login: " + path)
	fmt.Println("Log: tail -f " + logFile)
	return nil
}

// ---- Linux: a systemd user service

func linuxUnitPath() string {
	config, err := os.UserConfigDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		config = filepath.Join(home, ".config")
	}
	return filepath.Join(config, "systemd", "user", serviceName+".service")
}

func systemdQuote(arg string) string {
	if arg == "" || strings.ContainsAny(arg, " \t\"'\\$%;") {
		escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$").Replace(arg)
		return `"` + escaped + `"`
	}
	return arg
}

func installLinux(executable string, args []string) error {
	quoted := []string{systemdQuote(executable)}
	for _, arg := range args {
		quoted = append(quoted, systemdQuote(arg))
	}
	unit := `[Unit]
Description=Claude HTTP Bridge
After=network-online.target

[Service]
ExecStart=` + strings.Join(quoted, " ") + `
Environment=` + systemdQuote("PATH="+os.Getenv("PATH")) + `
Restart=on-failure
RestartSec=10

[Install]
WantedBy=default.target
`
	path := linuxUnitPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
		return err
	}
	for _, command := range [][]string{{"daemon-reload"}, {"enable", "--now", serviceName}, {"restart", serviceName}} {
		if output, err := exec.Command("systemctl", append([]string{"--user"}, command...)...).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl --user %s failed: %v %s", strings.Join(command, " "), err, output)
		}
	}
	fmt.Println("The bridge runs now and starts at every login: " + path)
	fmt.Println("Log: journalctl --user -u " + serviceName + " -f")
	fmt.Println("To keep it running while you are logged out (e.g. a headless server): sudo loginctl enable-linger $USER")
	return nil
}
