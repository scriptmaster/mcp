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
		if payload["model"] != "test-model" || payload["instructions"] != "system rules" || payload["input"] != "hello" || payload["store"] != false {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		w.Header().Set("X-Request-Id", "upstream-request")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","model":"test-model","output":[{"type":"message","content":[{"type":"output_text","text":"hello back"}]}],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}`))
	}))
	defer upstream.Close()

	client := NewWithEndpoint("upstream-secret", "test-model", 200, time.Second, upstream.URL)
	result, err := client.Prompt(context.Background(), "system rules", "hello", "client-request")
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
	_, err := client.Prompt(context.Background(), "", "hello", "")
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.StatusCode != http.StatusTooManyRequests || !strings.Contains(apiErr.Error(), "rate limit") {
		t.Fatalf("unexpected error: %#v", err)
	}
}

func TestGeminiPrompt(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Goog-Api-Key"); got != "gemini-secret" {
			t.Fatalf("Gemini key header = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("unexpected authorization header = %q", got)
		}
		var payload struct {
			SystemInstruction struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"systemInstruction"`
			Contents []struct {
				Role  string `json:"role"`
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"contents"`
			GenerationConfig struct {
				MaxOutputTokens int64 `json:"maxOutputTokens"`
			} `json:"generationConfig"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Contents) != 1 || payload.Contents[0].Role != "user" || len(payload.Contents[0].Parts) != 1 || payload.Contents[0].Parts[0].Text != "hello" {
			t.Fatalf("unexpected Gemini payload: %#v", payload)
		}
		if len(payload.SystemInstruction.Parts) != 1 || payload.SystemInstruction.Parts[0].Text != "system rules" {
			t.Fatalf("unexpected Gemini system instruction: %#v", payload.SystemInstruction)
		}
		if payload.GenerationConfig.MaxOutputTokens != 200 {
			t.Fatalf("max output tokens = %d", payload.GenerationConfig.MaxOutputTokens)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"responseId":"gemini-response","modelVersion":"gemini-test","candidates":[{"content":{"parts":[{"text":"hello from Gemini"}]}}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":4,"totalTokenCount":7}}`))
	}))
	defer upstream.Close()

	client := NewGeminiWithEndpoint("gemini-secret", "gemini-test", 200, time.Second, upstream.URL)
	result, err := client.Prompt(context.Background(), "system rules", "hello", "ignored-request-id")
	if err != nil {
		t.Fatal(err)
	}
	if result.Provider != "gemini" || result.Text != "hello from Gemini" || result.Model != "gemini-test" || result.Usage.TotalTokens != 7 {
		t.Fatalf("unexpected Gemini result: %#v", result)
	}
}
