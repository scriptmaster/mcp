package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mcpdev/internal/auth"
	openaiservice "mcpdev/internal/openai"
)

const testClientSecret = "11111111-1111-4111-8111-111111111111.22222222-2222-4222-8222-222222222222"

func configuredOpenAIServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Instructions string `json:"instructions"`
			Input        string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Instructions != "system rules" || payload.Input != "hello" {
			t.Fatalf("unexpected upstream payload: %#v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "upstream-id")
		_, _ = w.Write([]byte(`{"id":"resp_test","model":"test-model","output":[{"type":"message","content":[{"type":"output_text","text":"MCP OpenAI integration OK"}]}],"usage":{"input_tokens":7,"output_tokens":5,"total_tokens":12}}`))
	}))
	s := testServer(t)
	if err := os.WriteFile(filepath.Join(filepath.Dir(s.cfg.Server.Homepage), "openai-test.html"), []byte("test page"), 0600); err != nil {
		t.Fatal(err)
	}
	s.cfg.OpenAI.Enabled = true
	s.cfg.OpenAI.Model = "test-model"
	s.cfg.OpenAI.ClientKey = "DevSpectra"
	s.cfg.OpenAI.ClientSecret = testClientSecret
	s.cfg.OpenAI.MaxPromptBytes = 1024
	s.cfg.OpenAI.AllowedOrigins = []string{"https://mcp.ai.msheriff.com", "https://*.app.ai.msheriff.com"}
	s.openAI = openaiservice.NewWithEndpoint("upstream-secret", "test-model", 100, time.Second, upstream.URL)
	s.openAIAuth = auth.NewHeaderPair("DevSpectra", testClientSecret)
	s.openAILimiter = newRequestLimiter(30)
	return s, upstream
}

func TestOpenAIPromptAuthenticationAndResponse(t *testing.T) {
	s, upstream := configuredOpenAIServer(t)
	defer upstream.Close()

	r := httptest.NewRequest(http.MethodPost, "/openai/prompt", strings.NewReader(`{"prompt":"hello"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, body = %s", w.Code, w.Body.String())
	}

	r = httptest.NewRequest(http.MethodPost, "/openai/prompt", strings.NewReader(`{"system_prompt":"system rules","prompt":"hello"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-API-Key", "DevSpectra")
	r.Header.Set("X-API-Secret", testClientSecret)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "MCP OpenAI integration OK") {
		t.Fatalf("authenticated response = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestOpenAIPromptRejectsOversizedSystemPrompt(t *testing.T) {
	s, upstream := configuredOpenAIServer(t)
	defer upstream.Close()

	body, err := json.Marshal(map[string]string{
		"system_prompt": strings.Repeat("s", 1025),
		"prompt":        "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/openai/prompt", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-API-Key", "DevSpectra")
	r.Header.Set("X-API-Secret", testClientSecret)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized system prompt status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestOpenAIPromptCORS(t *testing.T) {
	s, upstream := configuredOpenAIServer(t)
	defer upstream.Close()

	r := httptest.NewRequest(http.MethodOptions, "/openai/prompt", nil)
	r.Header.Set("Origin", "https://client.app.ai.msheriff.com")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != "https://client.app.ai.msheriff.com" {
		t.Fatalf("preflight response = %d, headers = %#v", w.Code, w.Header())
	}

	r = httptest.NewRequest(http.MethodOptions, "/openai/prompt", nil)
	r.Header.Set("Origin", "https://app.ai.msheriff.com.evil.example")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("disallowed origin status = %d", w.Code)
	}
}

func TestAITestPageRouteCSPAndLegacyRedirect(t *testing.T) {
	s, upstream := configuredOpenAIServer(t)
	defer upstream.Close()
	r := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatalf("test page response = %d, CSP = %q", w.Code, w.Header().Get("Content-Security-Policy"))
	}

	r = httptest.NewRequest(http.MethodGet, "/openai/test", nil)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusPermanentRedirect || w.Header().Get("Location") != "/test" {
		t.Fatalf("legacy test redirect = %d, location = %q", w.Code, w.Header().Get("Location"))
	}

	r = httptest.NewRequest(http.MethodGet, "/test/", nil)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("non-exact test route status = %d", w.Code)
	}
}
