// Claude Bridge makes the Claude Code CLI on this computer usable over HTTP.
//
// It lets plugins on a server that can't run the CLI itself (e.g. a hosted Minecraft server) use the account
// the CLI is logged in with here, such as a Claude Pro/Max subscription. Clients sign every request with their
// own Ed25519 key; the bridge only knows their public keys (authorized_keys), like SSH.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const usage = `Claude Bridge %s (API %d): the Claude Code CLI over HTTP

Usage:
  claude-bridge [serve] [options]       start the bridge
  claude-bridge add-key "ed25519 ..."   authorize a client key (the line from the plugin's log)
  claude-bridge install [options]       start the bridge with these options at every login
  claude-bridge uninstall               remove that again
  claude-bridge version

Options:
`

func main() {
	args := os.Args[1:]
	command := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command, args = args[0], args[1:]
	}
	var err error
	switch command {
	case "serve":
		err = serve(args)
	case "add-key":
		err = addKey(args)
	case "install":
		err = install(args)
	case "uninstall":
		err = uninstall()
	case "version":
		fmt.Printf("claude-bridge %s (API %d, oldest supported %d)\n", version, apiVersion, apiMin)
	case "help":
		newFlags(&options{}).Usage()
	default:
		err = fmt.Errorf("unknown command %q, see claude-bridge help", command)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type options struct {
	config         Config
	domains        string
	queueSeconds   int
	timeoutSeconds int
	addKey         string
	funnel         bool
	logFile        string
}

func defaultPath(parts ...string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(append([]string{home, ".claude-bridge"}, parts...)...)
}

func newFlags(o *options) *flag.FlagSet {
	flags := flag.NewFlagSet("claude-bridge", flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), usage, version, apiVersion)
		flags.PrintDefaults()
	}
	host := os.Getenv("CLAUDE_BRIDGE_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port, err := strconv.Atoi(os.Getenv("CLAUDE_BRIDGE_PORT"))
	if err != nil {
		port = 8787
	}
	flags.StringVar(&o.config.Host, "host", host, "interface to listen on; the default is only this computer, reach it through a tunnel (CLAUDE_BRIDGE_HOST)")
	flags.IntVar(&o.config.Port, "port", port, "port (CLAUDE_BRIDGE_PORT)")
	flags.StringVar(&o.config.AuthorizedKeys, "authorized-keys", defaultPath("authorized_keys"), "public keys of the clients allowed to connect, read again on every change")
	flags.StringVar(&o.addKey, "add-key", "", "authorize a client's public key line and exit (same as the add-key command)")
	flags.StringVar(&o.config.Model, "model", "sonnet", "model when a request names none")
	flags.BoolVar(&o.config.NoWeb, "no-web", false, "never allow web research, even when a client asks")
	flags.StringVar(&o.domains, "web-domains", strings.Join(webDomains, ","), "comma separated domains Claude may read pages from (searching is always allowed), * = all")
	flags.IntVar(&o.config.Parallel, "parallel", 3, "max claude processes at once")
	flags.IntVar(&o.queueSeconds, "queue-timeout", 60, "seconds a request waits for a free slot")
	flags.IntVar(&o.timeoutSeconds, "timeout", 180, "seconds a single claude call may take")
	flags.StringVar(&o.config.Workspaces, "workspaces", defaultPath("workspaces"), "folder for per-client working directories (session history lives with them)")
	flags.StringVar(&o.config.Claude, "claude", "", "path to the claude executable (default: claude from PATH)")
	flags.BoolVar(&o.funnel, "funnel", false, "also publish the port with Tailscale Funnel (tailscale funnel --bg <port>)")
	flags.StringVar(&o.logFile, "log-file", "", "also write the log to this file")
	return flags
}

func parse(args []string) (*options, error) {
	o := &options{}
	flags := newFlags(o)
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if flags.NArg() > 0 {
		return nil, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	o.config.QueueTimeout = time.Duration(o.queueSeconds) * time.Second
	o.config.Timeout = time.Duration(o.timeoutSeconds) * time.Second
	for _, domain := range strings.Split(o.domains, ",") {
		if domain = strings.TrimSpace(domain); domain != "" {
			o.config.WebDomains = append(o.config.WebDomains, domain)
		}
	}
	return o, nil
}

func addKey(args []string) error {
	o := &options{}
	flags := newFlags(o)
	if err := flags.Parse(args); err != nil {
		return err
	}
	line := strings.Join(flags.Args(), " ")
	if line == "" {
		return errors.New(`give the key line, e.g. claude-bridge add-key "ed25519 MCowBQYDK2VwAyEA... claudechat@server"`)
	}
	message, err := NewAuthorizedKeys(o.config.AuthorizedKeys).Add(line)
	if err != nil {
		return err
	}
	fmt.Println(message)
	return nil
}

func serve(args []string) error {
	o, err := parse(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	keys := NewAuthorizedKeys(o.config.AuthorizedKeys)
	if o.addKey != "" {
		message, err := keys.Add(o.addKey)
		if err != nil {
			return err
		}
		fmt.Println(message)
		return nil
	}
	if o.logFile != "" {
		file, err := os.OpenFile(o.logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer file.Close()
		log.SetOutput(io.MultiWriter(os.Stderr, file))
	}
	if o.config.Claude == "" {
		o.config.Claude, err = exec.LookPath("claude")
		if err != nil {
			return errors.New("claude not found on PATH, install Claude Code or pass --claude <path>")
		}
	}
	bridge, err := NewBridge(o.config)
	if err != nil {
		return err
	}

	address := net.JoinHostPort(o.config.Host, strconv.Itoa(o.config.Port))
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("can't listen on %s (is another bridge already running?): %w", address, err)
	}
	log.Printf("Claude Bridge %s (API %d) on http://%s using %s", version, apiVersion, address, o.config.Claude)
	keys.Load()
	if o.funnel {
		startFunnel(o.config.Port)
	}

	server := &http.Server{Handler: newHandler(bridge, keys), ReadHeaderTimeout: 15 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	if err := server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// startFunnel publishes the port with Tailscale Funnel. The setting persists (--bg), so this only has to work once.
func startFunnel(port int) {
	tailscale, err := exec.LookPath("tailscale")
	if err != nil {
		log.Printf("--funnel: tailscale not found, start the tunnel yourself")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, tailscale, "funnel", "--bg", strconv.Itoa(port)).CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		log.Printf("--funnel: tailscale funnel failed: %v\n%s", err, text)
		return
	}
	log.Printf("Tailscale Funnel:\n%s", text)
}
