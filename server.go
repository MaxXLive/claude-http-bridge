package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const maxBody = 1_000_000

type server struct {
	bridge *Bridge
	keys   *AuthorizedKeys
}

func newHandler(bridge *Bridge, keys *AuthorizedKeys) http.Handler {
	s := &server{bridge: bridge, keys: keys}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.health)
	mux.HandleFunc("/v1/chat", s.signed(s.chat))
	mux.HandleFunc("/chat", s.signed(s.chat))
	mux.HandleFunc("/v1/run", s.signed(s.run))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
	})
	return logged(versioned(mux))
}

// versioned tells every response which bridge and API version answered, so clients can warn about an old bridge.
func versioned(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "claude-http-bridge/"+version)
		w.Header().Set("X-Bridge-Version", version)
		w.Header().Set("X-Bridge-Api", strconv.Itoa(apiVersion))
		w.Header().Set("X-Bridge-Api-Min", strconv.Itoa(apiMin))
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func logged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		log.Printf("%s %q %d", clientAddress(r), r.Method+" "+r.URL.RequestURI(), recorder.status)
	})
}

// clientAddress is the address to log for a request. Behind a tunnel on this computer (Tailscale Funnel, cloudflared)
// every request comes from loopback, so it is the client address the tunnel added to X-Forwarded-For. Only the last
// entry is the tunnel's own, earlier ones come from the client. Direct requests can't set it, they could fake it.
func clientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return r.RemoteAddr
	}
	forwarded := r.Header.Values("X-Forwarded-For")
	if len(forwarded) == 0 {
		return r.RemoteAddr
	}
	entries := strings.Split(forwarded[len(forwarded)-1], ",")
	if client := strings.TrimSpace(entries[len(entries)-1]); net.ParseIP(client) != nil {
		return client
	}
	return r.RemoteAddr
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	body, _ := json.Marshal(payload)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if status >= 400 {
		// The request body may not have been read; don't let it be parsed as the next request
		w.Header().Set("Connection", "close")
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "name": "claude-http-bridge", "version": version,
		"api": apiVersion, "api_min": apiMin, "features": features,
	})
}

type signedHandler func(w http.ResponseWriter, r *http.Request, client string, body []byte)

// signed checks method, body, signature and API version, waits for a free slot and then runs the handler.
func (s *server) signed(handle signedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
		if err != nil || len(body) == 0 || len(body) > maxBody {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing or too large body"})
			return
		}
		client, ok := s.keys.Verify(r.Header, http.MethodPost, r.URL.Path, body)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized: unknown key, bad signature or clock skew"})
			return
		}
		if problem := apiProblem(r.Header.Get("X-Bridge-Api")); problem != "" {
			writeJSON(w, http.StatusUpgradeRequired, map[string]any{
				"error": problem, "version": version, "api": apiVersion, "api_min": apiMin,
			})
			return
		}

		timer := time.NewTimer(s.bridge.config.QueueTimeout)
		defer timer.Stop()
		select {
		case s.bridge.slots <- struct{}{}:
		case <-timer.C:
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "busy, too many requests at once"})
			return
		case <-r.Context().Done():
			return
		}
		started := time.Now()
		defer func() {
			<-s.bridge.slots
			log.Printf("%s %s done in %.1fs", client, r.URL.Path, time.Since(started).Seconds())
		}()
		handle(w, r, client, body)
	}
}

// apiProblem explains why a client speaking this API version can't be served, or returns "" if it can.
// Clients that don't send the header are served as before.
func apiProblem(header string) string {
	if header == "" {
		return ""
	}
	clientAPI, err := strconv.Atoi(header)
	if err != nil {
		return "invalid X-Bridge-Api header"
	}
	if clientAPI > apiVersion {
		return fmt.Sprintf("the client needs bridge API %d, this bridge (%s) offers %d: update the bridge", clientAPI, version, apiVersion)
	}
	if clientAPI < apiMin {
		return fmt.Sprintf("the client speaks bridge API %d, this bridge (%s) needs at least %d: update the client", clientAPI, version, apiMin)
	}
	return ""
}

func decode(body []byte, target any) error {
	if err := json.Unmarshal(body, target); err != nil {
		return badRequest("invalid json: %v", err)
	}
	return nil
}

func (s *server) chat(w http.ResponseWriter, r *http.Request, client string, body []byte) {
	var request chatRequest
	if err := decode(body, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	response, err := s.bridge.Chat(r.Context(), request)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, response)
	case errors.Is(err, errBadRequest):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
	case errors.Is(err, errTimeout):
		writeJSON(w, http.StatusGatewayTimeout, map[string]any{"error": err.Error()})
	default:
		log.Printf("%s: chat failed: %v", client, err)
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
	}
}

// run streams the CLI's stream-json output line by line (NDJSON, chunked). The last line is
// {"type": "bridge_exit", "code": N}. A client hanging up kills the CLI process.
func (s *server) run(w http.ResponseWriter, r *http.Request, client string, body []byte) {
	var request runRequest
	if err := decode(body, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	command, workdir, err := s.bridge.runCommand(request)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errBadRequest) {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]any{"error": err.Error()})
		return
	}

	// The request context ends when the client disconnects, which kills the process
	ctx, cancel := context.WithTimeout(r.Context(), s.bridge.config.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = workdir
	cmd.Env = childEnv()
	cmd.Stdin = strings.NewReader(request.Prompt)
	var stderr strings.Builder
	cmd.Stderr = &limitedWriter{builder: &stderr, limit: 4000}
	stdout, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "could not start claude: " + err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	reader := bufio.NewReaderSize(stdout, 64*1024)
	for {
		line, readErr := reader.ReadString('\n')
		if line != "" {
			if !strings.HasSuffix(line, "\n") {
				line += "\n"
			}
			if _, writeErr := io.WriteString(w, line); writeErr != nil {
				// The client hung up (cancelled), so stop working for it
				cancel()
				break
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			break
		}
	}
	_ = cmd.Wait()
	code := cmd.ProcessState.ExitCode()
	if code != 0 && stderr.Len() > 0 {
		log.Printf("%s: run exited with %d: %s", client, code, strings.TrimSpace(stderr.String()))
	}
	// Last line: the CLI's exit code, which the client would otherwise not see
	exit, _ := json.Marshal(map[string]any{"type": "bridge_exit", "code": code})
	_, _ = w.Write(append(exit, '\n'))
	if flusher != nil {
		flusher.Flush()
	}
}

// limitedWriter keeps the first bytes of the CLI's error output for the log.
type limitedWriter struct {
	builder *strings.Builder
	limit   int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if room := l.limit - l.builder.Len(); room > 0 {
		l.builder.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}
