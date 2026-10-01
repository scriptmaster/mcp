package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	openaiservice "mcpdev/internal/openai"
)

type requestLimiter struct {
	mu       sync.Mutex
	window   time.Time
	requests int
	limit    int
}

func newRequestLimiter(limit int) *requestLimiter {
	return &requestLimiter{window: time.Now(), limit: limit}
}

func (l *requestLimiter) allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.window) >= time.Minute {
		l.window = now
		l.requests = 0
	}
	if l.requests >= l.limit {
		return false
	}
	l.requests++
	return true
}

func (s *Server) openAITestPage(w http.ResponseWriter, r *http.Request) {
	s.serveOpenAIAsset(w, r, "openai-test.html", "text/html; charset=utf-8")
}

func (s *Server) openAITestScript(w http.ResponseWriter, r *http.Request) {
	s.serveOpenAIAsset(w, r, "openai-test.js", "text/javascript; charset=utf-8")
}

func (s *Server) serveOpenAIAsset(w http.ResponseWriter, r *http.Request, name, contentType string) {
	if s.openAI == nil {
		http.NotFound(w, r)
		return
	}
	root, err := os.OpenRoot(filepath.Dir(s.cfg.Server.Homepage))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	b, err := root.ReadFile(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b)
}

func (s *Server) handleOpenAIPrompt(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	w.Header().Set("Cache-Control", "no-store")
	if s.openAI == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if !s.setOpenAICORS(w, r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "origin not allowed"})
		return
	}
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-API-Key, X-API-Secret")
		w.Header().Set("Access-Control-Max-Age", "600")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !s.openAIAuth.Authorized(r) {
		w.Header().Set("WWW-Authenticate", `API-Key realm="openai-prompt"`)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		s.logOpenAIPrompt(r, started, http.StatusUnauthorized, false)
		return
	}
	if !s.openAILimiter.allow(time.Now()) {
		w.Header().Set("Retry-After", "60")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "request rate limit exceeded"})
		s.logOpenAIPrompt(r, started, http.StatusTooManyRequests, false)
		return
	}
	if mediaType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0])); mediaType != "application/json" {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be application/json"})
		s.logOpenAIPrompt(r, started, http.StatusUnsupportedMediaType, false)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.OpenAI.MaxPromptBytes+1024)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var request struct {
		Prompt string `json:"prompt"`
	}
	if err := dec.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON request"})
		s.logOpenAIPrompt(r, started, http.StatusBadRequest, false)
		return
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request must contain one JSON object"})
		s.logOpenAIPrompt(r, started, http.StatusBadRequest, false)
		return
	}
	request.Prompt = strings.TrimSpace(request.Prompt)
	if request.Prompt == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "prompt is required"})
		s.logOpenAIPrompt(r, started, http.StatusBadRequest, false)
		return
	}
	if int64(len(request.Prompt)) > s.cfg.OpenAI.MaxPromptBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "prompt is too large"})
		s.logOpenAIPrompt(r, started, http.StatusRequestEntityTooLarge, false)
		return
	}

	clientRequestID := fmt.Sprintf("mcp-%d", time.Now().UnixNano())
	result, err := s.openAI.Prompt(r.Context(), request.Prompt, clientRequestID)
	if err != nil {
		status := http.StatusBadGateway
		var apiErr *openaiservice.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusTooManyRequests {
			status = http.StatusTooManyRequests
		} else if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, r.Context().Err()) {
			status = http.StatusGatewayTimeout
		}
		writeJSON(w, status, map[string]string{"error": "AI request failed"})
		s.logOpenAIPrompt(r, started, status, false)
		return
	}
	response := struct {
		*openaiservice.Result
		DurationMS int64 `json:"duration_ms"`
	}{Result: result, DurationMS: time.Since(started).Milliseconds()}
	writeJSON(w, http.StatusOK, response)
	s.logOpenAIPrompt(r, started, http.StatusOK, true)
}

func (s *Server) setOpenAICORS(w http.ResponseWriter, r *http.Request) bool {
	origin := strings.TrimRight(r.Header.Get("Origin"), "/")
	if origin == "" {
		return true
	}
	for _, allowed := range s.cfg.OpenAI.AllowedOrigins {
		allowed = strings.TrimRight(allowed, "/")
		if origin == allowed || wildcardOriginMatch(origin, allowed) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			return true
		}
	}
	return false
}

func wildcardOriginMatch(origin, pattern string) bool {
	if !strings.Contains(pattern, "*.") {
		return false
	}
	o, err := url.Parse(origin)
	if err != nil || o.Path != "" || o.RawQuery != "" || o.Fragment != "" || o.User != nil {
		return false
	}
	p, err := url.Parse(pattern)
	if err != nil || o.Scheme != p.Scheme || o.Port() != p.Port() {
		return false
	}
	suffix := strings.TrimPrefix(p.Hostname(), "*")
	host := strings.ToLower(o.Hostname())
	return strings.HasSuffix(host, strings.ToLower(suffix)) && host != strings.TrimPrefix(strings.ToLower(suffix), ".")
}

func (s *Server) logOpenAIPrompt(r *http.Request, started time.Time, status int, success bool) {
	s.logger.Info("openai prompt", "method", r.Method, "path", r.URL.Path, "status", status, "duration_ms", time.Since(started).Milliseconds(), "success", success)
}
