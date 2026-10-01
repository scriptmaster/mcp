package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const responsesEndpoint = "https://api.openai.com/v1/responses"
const geminiEndpoint = "https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent"

type Client struct {
	provider        string
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
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Text      string `json:"text"`
	RequestID string `json:"request_id,omitempty"`
	Usage     Usage  `json:"usage"`
}

type APIError struct {
	Provider   string
	StatusCode int
	Message    string
	RequestID  string
}

func (e *APIError) Error() string {
	provider := e.Provider
	if provider == "" {
		provider = "OpenAI"
	}
	return fmt.Sprintf("%s returned HTTP %d: %s", provider, e.StatusCode, e.Message)
}

func New(apiKey, model string, maxOutputTokens int64, timeout time.Duration) *Client {
	return newClient("openai", apiKey, model, maxOutputTokens, timeout, responsesEndpoint)
}

func NewWithEndpoint(apiKey, model string, maxOutputTokens int64, timeout time.Duration, endpoint string) *Client {
	return newClient("openai", apiKey, model, maxOutputTokens, timeout, endpoint)
}

func NewGemini(apiKey, model string, maxOutputTokens int64, timeout time.Duration) *Client {
	endpoint := fmt.Sprintf(geminiEndpoint, url.PathEscape(model))
	return newClient("gemini", apiKey, model, maxOutputTokens, timeout, endpoint)
}

func NewGeminiWithEndpoint(apiKey, model string, maxOutputTokens int64, timeout time.Duration, endpoint string) *Client {
	return newClient("gemini", apiKey, model, maxOutputTokens, timeout, endpoint)
}

func newClient(provider, apiKey, model string, maxOutputTokens int64, timeout time.Duration, endpoint string) *Client {
	return &Client{
		provider:        provider,
		apiKey:          apiKey,
		model:           model,
		maxOutputTokens: maxOutputTokens,
		endpoint:        endpoint,
		http:            &http.Client{Timeout: timeout},
	}
}

func (c *Client) Prompt(ctx context.Context, systemPrompt, prompt, clientRequestID string) (*Result, error) {
	if c.provider == "gemini" {
		return c.promptGemini(ctx, systemPrompt, prompt)
	}
	return c.promptOpenAI(ctx, systemPrompt, prompt, clientRequestID)
}

func (c *Client) promptOpenAI(ctx context.Context, systemPrompt, prompt, clientRequestID string) (*Result, error) {
	payload := struct {
		Model           string `json:"model"`
		Instructions    string `json:"instructions,omitempty"`
		Input           string `json:"input"`
		Store           bool   `json:"store"`
		MaxOutputTokens int64  `json:"max_output_tokens"`
	}{Model: c.model, Instructions: systemPrompt, Input: prompt, Store: false, MaxOutputTokens: c.maxOutputTokens}
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
		return nil, &APIError{Provider: "OpenAI", StatusCode: resp.StatusCode, Message: message, RequestID: requestID}
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
		Provider:  "openai",
		Model:     apiResponse.Model,
		Text:      strings.Join(textParts, "\n"),
		RequestID: requestID,
		Usage:     apiResponse.Usage,
	}, nil
}

func (c *Client) promptGemini(ctx context.Context, systemPrompt, prompt string) (*Result, error) {
	type part struct {
		Text string `json:"text"`
	}
	type content struct {
		Role  string `json:"role,omitempty"`
		Parts []part `json:"parts"`
	}
	payload := struct {
		Contents          []content `json:"contents"`
		SystemInstruction *content  `json:"systemInstruction,omitempty"`
		GenerationConfig  struct {
			MaxOutputTokens int64 `json:"maxOutputTokens"`
		} `json:"generationConfig"`
	}{Contents: []content{{Role: "user", Parts: []part{{Text: prompt}}}}}
	if strings.TrimSpace(systemPrompt) != "" {
		payload.SystemInstruction = &content{Parts: []part{{Text: systemPrompt}}}
	}
	payload.GenerationConfig.MaxOutputTokens = c.maxOutputTokens
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode Gemini request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create Gemini request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goog-Api-Key", c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call Gemini: %w", err)
	}
	defer resp.Body.Close()
	requestID := resp.Header.Get("X-Request-Id")
	if requestID == "" {
		requestID = resp.Header.Get("X-Google-Request-ID")
	}
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
		return nil, &APIError{Provider: "Gemini", StatusCode: resp.StatusCode, Message: message, RequestID: requestID}
	}
	var apiResponse struct {
		ResponseID   string `json:"responseId"`
		ModelVersion string `json:"modelVersion"`
		Candidates   []struct {
			Content struct {
				Parts []part `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		Usage struct {
			InputTokens  int64 `json:"promptTokenCount"`
			OutputTokens int64 `json:"candidatesTokenCount"`
			TotalTokens  int64 `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.NewDecoder(limited).Decode(&apiResponse); err != nil {
		return nil, fmt.Errorf("decode Gemini response: %w", err)
	}
	var textParts []string
	for _, candidate := range apiResponse.Candidates {
		for _, responsePart := range candidate.Content.Parts {
			if responsePart.Text != "" {
				textParts = append(textParts, responsePart.Text)
			}
		}
	}
	model := apiResponse.ModelVersion
	if model == "" {
		model = c.model
	}
	return &Result{
		ID:        apiResponse.ResponseID,
		Provider:  "gemini",
		Model:     model,
		Text:      strings.Join(textParts, "\n"),
		RequestID: requestID,
		Usage: Usage{
			InputTokens:  apiResponse.Usage.InputTokens,
			OutputTokens: apiResponse.Usage.OutputTokens,
			TotalTokens:  apiResponse.Usage.TotalTokens,
		},
	}, nil
}
