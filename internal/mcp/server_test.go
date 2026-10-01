package mcp

import (
	"bytes"
	"io"
	"log/slog"
	"mcpdev/internal/config"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(root+"/web", 0700); err != nil {
		t.Fatal(err)
	}
	home := root + "/web/index.html"
	if err := osWrite(home, "ok"); err != nil {
		t.Fatal(err)
	}
	enabled := true
	c := &config.Config{Server: config.ServerConfig{Homepage: home, AllowedOrigins: []string{"https://chatgpt.com"}}, Security: config.SecurityConfig{Token: "01234567890123456789012345678901"}, Limits: config.LimitsConfig{MaxRequestBytes: 1 << 20, MaxReadBytes: 1 << 20, MaxWriteBytes: 1 << 20, MaxOutputBytes: 1 << 20, MaxSearchResult: 100, CommandTimeout: 5}, Commands: config.CommandsConfig{Git: true, Build: true, Test: true}, Workspaces: []config.WorkspaceConfig{{Name: "w", Path: root, Enabled: &enabled}}}
	return New(c, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestWhitelistedDownload(t *testing.T) {
	s := testServer(t)
	dir := filepath.Join(filepath.Dir(filepath.Dir(s.cfg.Server.Homepage)), "downloads")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	name := "mcp-server-linux-amd64"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("binary"), 0600); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest("GET", "/downloads/"+name, nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Body.String() != "binary" {
		t.Fatalf("download failed: %d %q", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Disposition"), name) {
		t.Fatalf("missing attachment header: %q", w.Header().Get("Content-Disposition"))
	}

	r = httptest.NewRequest("GET", "/downloads/not-allowed", nil)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unexpected non-whitelist status: %d", w.Code)
	}
}
func osWrite(path, body string) error { return os.WriteFile(path, []byte(body), 0600) }

func TestUnknownRouteDoesNotFallBackToHomepage(t *testing.T) {
	s := testServer(t)
	r := httptest.NewRequest("GET", "/config/config.yaml", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown route returned %d instead of 404", w.Code)
	}
}

func TestUnauthorizedMCPAccess(t *testing.T) {
	s := testServer(t)
	r := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("got %d", w.Code)
	}
}
func TestAuthenticatedInitializeAndDiscovery(t *testing.T) {
	s := testServer(t)
	for _, body := range []string{`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`} {
		r := httptest.NewRequest("POST", "/mcp", bytes.NewBufferString(body))
		r.Header.Set("Authorization", "Bearer 01234567890123456789012345678901")
		r.Header.Set("Accept", "application/json, text/event-stream")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("got %d: %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"jsonrpc":"2.0"`) {
			t.Fatalf("invalid response: %s", w.Body.String())
		}
	}
}
