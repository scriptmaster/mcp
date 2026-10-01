package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const responsesEndpoint = "https://api.openai.com/v1/responses"

type Client struct {
	apiKey          string
	model           string
	maxOutputTokens int64
	endpoint        string
	http            *http.Client
}

type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
}

type Result struct {
	ID        string `json:"response_id"`
	Model     string `json:"model"`
	Text      string `json:"text"`
	RequestID string `json:"request_id,omitempty"`
	Usage     Usage  `json:"usage"`
}

type APIError struct {
	StatusCode int
	Message    string
	RequestID  string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("OpenAI returned HTTP %d: %s", e.StatusCode, e.Message)
}

func New(apiKey, model string, maxOutputTokens int64, timeout time.Duration) *Client {
	return NewWithEndpoint(apiKey, model, maxOutputTokens, timeout, responsesEndpoint)
}

func NewWithEndpoint(apiKey, model string, maxOutputTokens int64, timeout time.Duration, endpoint string) *Client {
	return &Client{
		apiKey:          apiKey,
		model:           model,
		maxOutputTokens: maxOutputTokens,
		endpoint:        endpoint,
		http:            &http.Client{Timeout: timeout},
	}
}

func (c *Client) Prompt(ctx context.Context, prompt, clientRequestID string) (*Result, error) {
	payload := struct {
		Model           string `json:"model"`
		Input           string `json:"input"`
		Store           bool   `json:"store"`
		MaxOutputTokens int64  `json:"max_output_tokens"`
	}{Model: c.model, Input: prompt, Store: false, MaxOutputTokens: c.maxOutputTokens}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode OpenAI request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create OpenAI request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	if clientRequestID != "" {
		req.Header.Set("X-Client-Request-Id", clientRequestID)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call OpenAI: %w", err)
	}
	defer resp.Body.Close()
	requestID := resp.Header.Get("X-Request-Id")
	limited := io.LimitReader(resp.Body, 2<<20)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiResponse struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		data, _ := io.ReadAll(io.LimitReader(limited, 32<<10))
		_ = json.Unmarshal(data, &apiResponse)
		message := strings.TrimSpace(apiResponse.Error.Message)
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return nil, &APIError{StatusCode: resp.StatusCode, Message: message, RequestID: requestID}
	}

	var apiResponse struct {
		ID     string `json:"id"`
		Model  string `json:"model"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage Usage `json:"usage"`
	}
	if err := json.NewDecoder(limited).Decode(&apiResponse); err != nil {
		return nil, fmt.Errorf("decode OpenAI response: %w", err)
	}
	var textParts []string
	for _, output := range apiResponse.Output {
		for _, content := range output.Content {
			if content.Type == "output_text" && content.Text != "" {
				textParts = append(textParts, content.Text)
			}
		}
	}
	return &Result{
		ID:        apiResponse.ID,
		Model:     apiResponse.Model,
		Text:      strings.Join(textParts, "\n"),
		RequestID: requestID,
		Usage:     apiResponse.Usage,
	}, nil
}
