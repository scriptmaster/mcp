package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPrompt(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer upstream-secret" {
			t.Fatalf("authorization = %q", got)
		}
		if got := r.Header.Get("X-Client-Request-Id"); got != "client-request" {
			t.Fatalf("client request ID = %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != "test-model" || payload["input"] != "hello" || payload["store"] != false {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		w.Header().Set("X-Request-Id", "upstream-request")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","model":"test-model","output":[{"type":"message","content":[{"type":"output_text","text":"hello back"}]}],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}`))
	}))
	defer upstream.Close()

	client := NewWithEndpoint("upstream-secret", "test-model", 200, time.Second, upstream.URL)
	result, err := client.Prompt(context.Background(), "hello", "client-request")
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "hello back" || result.RequestID != "upstream-request" || result.Usage.TotalTokens != 5 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestPromptUpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id", "req_failed")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limit"}}`))
	}))
	defer upstream.Close()

	client := NewWithEndpoint("secret", "test-model", 200, time.Second, upstream.URL)
	_, err := client.Prompt(context.Background(), "hello", "")
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.StatusCode != http.StatusTooManyRequests || !strings.Contains(apiErr.Error(), "rate limit") {
		t.Fatalf("unexpected error: %#v", err)
	}
}
