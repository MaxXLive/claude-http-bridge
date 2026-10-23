package main

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestMain lets the test binary stand in for the claude CLI: with FAKE_CLAUDE=1 it answers like
// "claude --print" would, echoing the prompt and writing its arguments to FAKE_CLAUDE_ARGS.
func TestMain(m *testing.M) {
	if os.Getenv("FAKE_CLAUDE") == "1" {
		fakeClaude()
		return
	}
	os.Exit(m.Run())
}

func fakeClaude() {
	prompt, _ := io.ReadAll(os.Stdin)
	args := os.Args[1:]
	if file := os.Getenv("FAKE_CLAUDE_ARGS"); file != "" {
		_ = os.WriteFile(file, []byte(strings.Join(args, "\n")), 0o600)
	}
	if strings.Contains(string(prompt), "FAIL") {
		fmt.Fprintln(os.Stderr, "something broke")
		os.Exit(3)
	}
	if contains(args, "stream-json") {
		fmt.Println(`{"type":"system","session_id":"11111111-2222-3333-4444-555555555555"}`)
		result, _ := json.Marshal(map[string]any{"type": "result", "result": "echo: " + string(prompt)})
		fmt.Println(string(result))
		return
	}
	result, _ := json.Marshal(map[string]any{
		"result": "echo: " + string(prompt), "is_error": false,
		"usage": map[string]any{"input_tokens": 5, "cache_read_input_tokens": 100, "output_tokens": 2},
	})
	fmt.Println(string(result))
}

type client struct {
	key     ed25519.PrivateKey
	encoded string
}

func newClient(t *testing.T) client {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	return client{private, base64.StdEncoding.EncodeToString(der)}
}

func (c client) sign(request *http.Request, body []byte, timestamp int64, nonce string) {
	sum := sha256.Sum256(body)
	payload := fmt.Sprintf("POST\n%s\n%d\n%s\n%s", request.URL.Path, timestamp, nonce, hex.EncodeToString(sum[:]))
	request.Header.Set("X-Bridge-Key", c.encoded)
	request.Header.Set("X-Bridge-Timestamp", strconv.FormatInt(timestamp, 10))
	request.Header.Set("X-Bridge-Nonce", nonce)
	request.Header.Set("X-Bridge-Signature", base64.StdEncoding.EncodeToString(ed25519.Sign(c.key, []byte(payload))))
}

func setup(t *testing.T) (*httptest.Server, client, string) {
	t.Helper()
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	t.Setenv("FAKE_CLAUDE", "1")
	t.Setenv("FAKE_CLAUDE_ARGS", argsFile)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	keys := NewAuthorizedKeys(filepath.Join(dir, "authorized_keys"))
	c := newClient(t)
	if _, err := keys.Add("ed25519 " + c.encoded + " test client"); err != nil {
		t.Fatal(err)
	}
	bridge, err := NewBridge(Config{
		Model: "sonnet", WebDomains: []string{"minecraft.wiki"}, Parallel: 2, QueueTimeout: time.Second,
		Timeout: 30 * time.Second, Workspaces: filepath.Join(dir, "workspaces"), Claude: executable,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(newHandler(bridge, keys))
	t.Cleanup(server.Close)
	return server, c, argsFile
}

var nonceCounter int

func post(t *testing.T, server *httptest.Server, c client, path string, payload any, change func(*http.Request)) *http.Response {
	t.Helper()
	body, _ := json.Marshal(payload)
	request, _ := http.NewRequest(http.MethodPost, server.URL+path, bytes.NewReader(body))
	nonceCounter++
	c.sign(request, body, time.Now().Unix(), fmt.Sprintf("nonce-%016d", nonceCounter))
	if change != nil {
		change(request)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

func TestHealth(t *testing.T) {
	server, _, _ := setup(t)
	response, err := http.Get(server.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var health map[string]any
	_ = json.NewDecoder(response.Body).Decode(&health)
	if health["ok"] != true || health["api"] != float64(apiVersion) || health["api_min"] != float64(apiMin) {
		t.Fatalf("unexpected health %v", health)
	}
	if response.Header.Get("X-Bridge-Api") != strconv.Itoa(apiVersion) {
		t.Fatalf("missing X-Bridge-Api header")
	}
}

func TestChat(t *testing.T) {
	server, c, argsFile := setup(t)
	response := post(t, server, c, "/v1/chat", map[string]any{
		"system": "be brief", "web": true,
		"messages": []map[string]string{{"role": "user", "content": "hi"}, {"role": "assistant", "content": "hello"},
			{"role": "user", "content": "how are you"}},
	}, nil)
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status %d: %s", response.StatusCode, body)
	}
	var reply chatResponse
	_ = json.NewDecoder(response.Body).Decode(&reply)
	if !strings.Contains(reply.Text, "Conversation so far:") || !strings.HasSuffix(strings.TrimSpace(reply.Text), "how are you") {
		t.Fatalf("transcript not passed on: %q", reply.Text)
	}
	if reply.Usage["input_tokens"] != 5 || reply.Usage["cache_read_input_tokens"] != 100 || reply.Usage["output_tokens"] != 2 {
		t.Fatalf("usage %v", reply.Usage)
	}
	args, _ := os.ReadFile(argsFile)
	for _, expected := range []string{"--safe-mode", "WebFetch(domain:minecraft.wiki)", "--system-prompt-file", "--max-turns"} {
		if !strings.Contains(string(args), expected) {
			t.Errorf("missing %s in %s", expected, args)
		}
	}
}

func TestChatErrors(t *testing.T) {
	server, c, _ := setup(t)
	if status := post(t, server, c, "/v1/chat", map[string]any{"messages": []map[string]string{{"role": "assistant", "content": "x"}}}, nil).StatusCode; status != 400 {
		t.Errorf("last message not from user: %d", status)
	}
	if status := post(t, server, c, "/v1/chat", map[string]any{"model": "gpt-4", "messages": []map[string]string{{"role": "user", "content": "x"}}}, nil).StatusCode; status != 400 {
		t.Errorf("invalid model: %d", status)
	}
	if status := post(t, server, c, "/v1/chat", map[string]any{"messages": []map[string]string{{"role": "user", "content": "FAIL"}}}, nil).StatusCode; status != 502 {
		t.Errorf("failing CLI: %d", status)
	}
}

func TestRunStreamsAndEndsWithExitCode(t *testing.T) {
	server, c, argsFile := setup(t)
	response := post(t, server, c, "/v1/run", map[string]any{
		"prompt": "hello", "workspace": "neurofolk/agents/Bob", "session_id": "7a11dfbe-0000-0000-0000-000000000000", "max_turns": 99,
	}, nil)
	if response.StatusCode != 200 {
		t.Fatalf("status %d", response.StatusCode)
	}
	var lines []map[string]any
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		var line map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("not json: %s", scanner.Text())
		}
		lines = append(lines, line)
	}
	if len(lines) != 3 || lines[1]["result"] != "echo: hello" || lines[2]["type"] != "bridge_exit" || lines[2]["code"] != float64(0) {
		t.Fatalf("unexpected stream %v", lines)
	}
	args, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(args), "--resume\n7a11dfbe") || !strings.Contains(string(args), "--max-turns\n50") {
		t.Fatalf("args %s", args)
	}
}

func TestRunRejectsBadWorkspace(t *testing.T) {
	server, c, _ := setup(t)
	for _, workspace := range []string{"../escape", "a/../../b", "with space"} {
		if status := post(t, server, c, "/v1/run", map[string]any{"prompt": "x", "workspace": workspace}, nil).StatusCode; status != 400 {
			t.Errorf("workspace %q: %d", workspace, status)
		}
	}
}

func TestSignatures(t *testing.T) {
	server, c, _ := setup(t)
	message := map[string]any{"messages": []map[string]string{{"role": "user", "content": "x"}}}
	cases := map[string]func(*http.Request){
		"unknown key": func(r *http.Request) { r.Header.Set("X-Bridge-Key", newClient(t).encoded) },
		"tampered": func(r *http.Request) {
			r.Body = io.NopCloser(strings.NewReader(`{"messages":[{"role":"user","content":"y"}]}`))
		},
		"old": func(r *http.Request) {
			r.Header.Set("X-Bridge-Timestamp", strconv.FormatInt(time.Now().Unix()-600, 10))
		},
		"no signature": func(r *http.Request) {
			r.Header.Del("X-Bridge-Signature")
		},
	}
	for name, change := range cases {
		if status := post(t, server, c, "/v1/chat", message, change).StatusCode; status != 401 {
			t.Errorf("%s: %d", name, status)
		}
	}

	// The same signed request twice: the second is a replay
	body, _ := json.Marshal(message)
	first, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/chat", bytes.NewReader(body))
	c.sign(first, body, time.Now().Unix(), "replayed-nonce-0001")
	second := first.Clone(first.Context())
	second.Body = io.NopCloser(bytes.NewReader(body))
	for i, request := range []*http.Request{first, second} {
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if expected := []int{200, 401}[i]; response.StatusCode != expected {
			t.Errorf("request %d: %d, want %d", i+1, response.StatusCode, expected)
		}
	}
}

func TestClientApiVersion(t *testing.T) {
	server, c, _ := setup(t)
	message := map[string]any{"messages": []map[string]string{{"role": "user", "content": "x"}}}
	for header, expected := range map[string]int{strconv.Itoa(apiVersion): 200, strconv.Itoa(apiVersion + 1): 426, "0": 426} {
		status := post(t, server, c, "/v1/chat", message, func(r *http.Request) { r.Header.Set("X-Bridge-Api", header) }).StatusCode
		if status != expected {
			t.Errorf("client API %s: %d, want %d", header, status, expected)
		}
	}
}

func TestAddKey(t *testing.T) {
	keys := NewAuthorizedKeys(filepath.Join(t.TempDir(), "sub", "authorized_keys"))
	c := newClient(t)
	if _, err := keys.Add("ed25519 " + c.encoded + " a"); err != nil {
		t.Fatal(err)
	}
	if message, _ := keys.Add("ed25519 " + c.encoded + " a"); !strings.Contains(message, "already") {
		t.Errorf("second add: %s", message)
	}
	if _, err := keys.Add("ssh-rsa AAAA x"); err == nil {
		t.Error("accepted a non-ed25519 line")
	}
}

func TestClientAddress(t *testing.T) {
	cases := []struct {
		remote, forwarded, want string
	}{
		{"203.0.113.5:4000", "", "203.0.113.5:4000"},
		{"127.0.0.1:4000", "", "127.0.0.1:4000"},
		{"127.0.0.1:4000", "203.0.113.5", "203.0.113.5"},
		{"[::1]:4000", "2001:db8::1", "2001:db8::1"},
		{"127.0.0.1:4000", "198.51.100.1, 203.0.113.5", "203.0.113.5"},
		{"192.168.1.20:4000", "203.0.113.5", "192.168.1.20:4000"},
		{"127.0.0.1:4000", "not an address", "127.0.0.1:4000"},
	}
	for _, c := range cases {
		request := httptest.NewRequest("GET", "/health", nil)
		request.RemoteAddr = c.remote
		if c.forwarded != "" {
			request.Header.Set("X-Forwarded-For", c.forwarded)
		}
		if got := clientAddress(request); got != c.want {
			t.Errorf("clientAddress(%s, %q) = %s, want %s", c.remote, c.forwarded, got, c.want)
		}
	}
}
