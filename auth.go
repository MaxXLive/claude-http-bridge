package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// maxSkew is how far the clocks of client and bridge may be apart.
const maxSkew = 120 * time.Second

type authorizedKey struct {
	key  ed25519.PublicKey
	name string
}

// AuthorizedKeys holds the public keys allowed to use the bridge, one "ed25519 <base64 DER> <name>" per line.
//
// The file is read again when it changes, so keys can be added or revoked without a restart. A request signs
// "METHOD\nPATH\nTIMESTAMP\nNONCE\nSHA256(BODY)"; timestamp and nonce stop a captured request from being replayed.
type AuthorizedKeys struct {
	path    string
	mu      sync.Mutex
	keys    map[string]authorizedKey
	modTime time.Time
	loaded  bool
	nonces  map[string]time.Time
	now     func() time.Time
}

func NewAuthorizedKeys(path string) *AuthorizedKeys {
	return &AuthorizedKeys{path: path, keys: map[string]authorizedKey{}, nonces: map[string]time.Time{}, now: time.Now}
}

// parseKeyLine returns the base64 key as written, the key and the name.
func parseKeyLine(line string) (string, ed25519.PublicKey, string, error) {
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[0] != "ed25519" {
		return "", nil, "", errors.New("expected: ed25519 <base64 key> [name]")
	}
	der, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return "", nil, "", fmt.Errorf("not a valid key: %v", err)
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return "", nil, "", fmt.Errorf("not a valid key: %v", err)
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return "", nil, "", errors.New("not an Ed25519 key")
	}
	name := "unnamed"
	if len(fields) > 2 {
		name = strings.Join(fields[2:], " ")
	}
	return fields[1], key, name, nil
}

// reloadIfChanged reads the file again when its modification time changed. Callers hold mu.
func (a *AuthorizedKeys) reloadIfChanged() {
	var modTime time.Time
	info, err := os.Stat(a.path)
	if err == nil {
		modTime = info.ModTime()
	}
	if a.loaded && modTime.Equal(a.modTime) {
		return
	}
	keys := map[string]authorizedKey{}
	if err == nil {
		content, readErr := os.ReadFile(a.path)
		if readErr != nil {
			log.Printf("%s: %v", a.path, readErr)
			return
		}
		for number, line := range strings.Split(string(content), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			encoded, key, name, parseErr := parseKeyLine(line)
			if parseErr != nil {
				log.Printf("%s:%d: skipped, %v", a.path, number+1, parseErr)
				continue
			}
			keys[encoded] = authorizedKey{key, name}
		}
	}
	a.keys, a.modTime, a.loaded = keys, modTime, true
	names := make([]string, 0, len(keys))
	for _, entry := range keys {
		names = append(names, entry.name)
	}
	if len(names) == 0 {
		names = append(names, "-")
	}
	log.Printf("%d authorized key(s): %s", len(keys), strings.Join(names, ", "))
}

// Load reads the file now, so the key count shows up at start.
func (a *AuthorizedKeys) Load() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reloadIfChanged()
}

// Verify returns the client's name, or false when the request isn't validly signed by a known key.
func (a *AuthorizedKeys) Verify(header http.Header, method, path string, body []byte) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reloadIfChanged()
	entry, ok := a.keys[header.Get("X-Bridge-Key")]
	if !ok {
		return "", false
	}
	timestamp, err := strconv.ParseInt(header.Get("X-Bridge-Timestamp"), 10, 64)
	if err != nil {
		return "", false
	}
	nonce := header.Get("X-Bridge-Nonce")
	signature, err := base64.StdEncoding.DecodeString(header.Get("X-Bridge-Signature"))
	if err != nil {
		return "", false
	}
	now := a.now()
	skew := now.Sub(time.Unix(timestamp, 0))
	if skew < 0 {
		skew = -skew
	}
	if skew > maxSkew || len(nonce) < 16 || len(nonce) > 64 {
		return "", false
	}
	if _, seen := a.nonces[nonce]; seen {
		return "", false
	}
	sum := sha256.Sum256(body)
	payload := fmt.Sprintf("%s\n%s\n%d\n%s\n%s", method, path, timestamp, nonce, hex.EncodeToString(sum[:]))
	if !ed25519.Verify(entry.key, []byte(payload), signature) {
		return "", false
	}
	for seenNonce, at := range a.nonces {
		if now.Sub(at) > 2*maxSkew {
			delete(a.nonces, seenNonce)
		}
	}
	a.nonces[nonce] = now
	return entry.name, true
}

// Add authorizes a key line, unless it is already there.
func (a *AuthorizedKeys) Add(line string) (string, error) {
	line = strings.TrimSpace(line)
	encoded, _, name, err := parseKeyLine(line)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(a.path), 0o700); err != nil {
		return "", err
	}
	existing, err := os.ReadFile(a.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if strings.Contains(string(existing), encoded) {
		return name + " is already authorized", nil
	}
	file, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		if _, err := file.WriteString("\n"); err != nil {
			return "", err
		}
	}
	if _, err := file.WriteString(line + "\n"); err != nil {
		return "", err
	}
	return fmt.Sprintf("authorized %s in %s", name, a.path), nil
}
