// Package vaulttest is an in-memory fake of the Vault HTTP API parts claims
// uses (KV v2 data/metadata, AppRole login, token lookup-self, capabilities
// -self), for tests. It is not a Vault: no policies, one token.
package vaulttest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
)

// Server is the fake.
type Server struct {
	URL      string
	Token    string // the only accepted token
	RoleID   string // AppRole credentials that return Token
	SecretID string
	TTL      int      // seconds, for lookup-self
	Caps     []string // capabilities-self answer (default read, list)

	mu     sync.Mutex
	data   map[string]map[string]any // "<mount>/<path>" -> latest data
	writes int
}

// New starts a fake with token "test-token" and the KV v2 mount "kv".
func New(t *testing.T) *Server {
	t.Helper()
	s := &Server{Token: "test-token", RoleID: "role", SecretID: "secret", TTL: 3600, data: map[string]map[string]any{}}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	s.URL = srv.URL
	return s
}

// Put stores data at mount/path directly.
func (s *Server) Put(mountPath string, data map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[strings.Trim(mountPath, "/")] = data
}

// Delete removes mount/path.
func (s *Server) Delete(mountPath string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, strings.Trim(mountPath, "/"))
}

// Get returns the data at mount/path (nil if missing).
func (s *Server) Get(mountPath string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[strings.Trim(mountPath, "/")]
}

// Writes counts the KV writes through the API.
func (s *Server) Writes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/v1/")
	if p == "auth/approle/login" && (r.Method == http.MethodPut || r.Method == http.MethodPost) {
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["role_id"] != s.RoleID || in["secret_id"] != s.SecretID {
			writeJSON(w, http.StatusBadRequest, map[string]any{"errors": []string{"invalid role or secret ID"}})
			return
		}
		writeJSON(w, 200, map[string]any{"auth": map[string]any{"client_token": s.Token, "lease_duration": s.TTL}})
		return
	}
	if r.Header.Get("X-Vault-Token") != s.Token {
		writeJSON(w, http.StatusForbidden, map[string]any{"errors": []string{"permission denied"}})
		return
	}
	switch p {
	case "auth/token/lookup-self":
		writeJSON(w, 200, map[string]any{"data": map[string]any{
			"display_name": "token-test", "ttl": s.TTL, "renewable": true, "policies": []string{"default", "secrets"},
		}})
		return
	case "sys/capabilities-self":
		var in struct {
			Paths []string `json:"paths"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &in)
		caps := s.Caps
		if caps == nil {
			caps = []string{"read", "list"}
		}
		out := map[string]any{"capabilities": caps}
		for _, pp := range in.Paths {
			out[pp] = caps
		}
		writeJSON(w, 200, out)
		return
	}
	mount, rest, ok := strings.Cut(p, "/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	kind, sub, _ := strings.Cut(rest, "/")
	s.mu.Lock()
	defer s.mu.Unlock()
	key := mount + "/" + strings.Trim(sub, "/")
	switch {
	case kind == "data" && r.Method == http.MethodGet:
		d, ok := s.data[key]
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"errors": []string{}})
			return
		}
		writeJSON(w, 200, map[string]any{"data": map[string]any{"data": d, "metadata": map[string]any{"version": 1}}})
	case kind == "data" && (r.Method == http.MethodPut || r.Method == http.MethodPost):
		var in struct {
			Data map[string]any `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, 400, map[string]any{"errors": []string{err.Error()}})
			return
		}
		s.data[key] = in.Data
		s.writes++
		writeJSON(w, 200, map[string]any{"data": map[string]any{"version": 1}})
	case kind == "metadata" && (r.Method == "LIST" || r.URL.Query().Get("list") == "true"):
		prefix := strings.Trim(sub, "/")
		if prefix != "" {
			prefix += "/"
		}
		seen := map[string]bool{}
		for k := range s.data {
			km, kp, _ := strings.Cut(k, "/")
			if km != mount || !strings.HasPrefix(kp, prefix) {
				continue
			}
			first, _, nested := strings.Cut(strings.TrimPrefix(kp, prefix), "/")
			if nested {
				first += "/"
			}
			seen[first] = true
		}
		if len(seen) == 0 {
			writeJSON(w, http.StatusNotFound, map[string]any{"errors": []string{}})
			return
		}
		keys := make([]string, 0, len(seen))
		for k := range seen {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		writeJSON(w, 200, map[string]any{"data": map[string]any{"keys": keys}})
	default:
		http.NotFound(w, r)
	}
}
