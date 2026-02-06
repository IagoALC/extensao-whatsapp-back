package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var ErrOpenAIUnavailable = errors.New("openai client unavailable")

type TokenUsage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

type GenerateRequest struct {
	Model           string
	Instructions    string
	Input           string
	Temperature     float64
	MaxOutputTokens int
}

type GenerateResult struct {
	Text    string
	ModelID string
	Usage   TokenUsage
}

type TextGenerator interface {
	Generate(ctx context.Context, request GenerateRequest) (GenerateResult, error)
	Available() bool
}

type OpenAIClientConfig struct {
	APIKey       string
	BaseURL      string
	Timeout      time.Duration
	MaxRetries   int
	HTTPClient   *http.Client
	Organization string
}

type OpenAIClient struct {
	apiKey       string
	baseURL      string
	timeout      time.Duration
	maxRetries   int
	httpClient   *http.Client
	organization string
}

func NewOpenAIClient(config OpenAIClientConfig) *OpenAIClient {
	if strings.TrimSpace(config.BaseURL) == "" {
		config.BaseURL = "https://api.openai.com/v1"
	}
	if config.Timeout <= 0 {
		config.Timeout = 15 * time.Second
	}
	if config.MaxRetries <= 0 {
		config.MaxRetries = 2
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{}
	}

	return &OpenAIClient{
		apiKey:       strings.TrimSpace(config.APIKey),
		baseURL:      strings.TrimSuffix(config.BaseURL, "/"),
		timeout:      config.Timeout,
		maxRetries:   config.MaxRetries,
		httpClient:   config.HTTPClient,
		organization: strings.TrimSpace(config.Organization),
	}
}

func (c *OpenAIClient) Available() bool {
	return c.apiKey != ""
}

func (c *OpenAIClient) Generate(ctx context.Context, request GenerateRequest) (GenerateResult, error) {
	if !c.Available() {
		return GenerateResult{}, ErrOpenAIUnavailable
	}
	if strings.TrimSpace(request.Model) == "" {
		return GenerateResult{}, errors.New("model is required")
	}
	if strings.TrimSpace(request.Input) == "" {
		return GenerateResult{}, errors.New("input is required")
	}

	payload := map[string]any{
		"model":             request.Model,
		"input":             request.Input,
		"instructions":      request.Instructions,
		"temperature":       request.Temperature,
		"max_output_tokens": request.MaxOutputTokens,
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return GenerateResult{}, fmt.Errorf("marshal openai payload: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		result, callErr := c.callResponsesAPI(ctx, encoded, request.Model)
		if callErr == nil {
			return result, nil
		}
		lastErr = callErr

		if !isRetryableError(callErr) || attempt == c.maxRetries {
			break
		}

		backoff := time.Duration(350*(attempt+1)) * time.Millisecond
		select {
		case <-ctx.Done():
			return GenerateResult{}, ctx.Err()
		case <-time.After(backoff):
		}
	}

	if lastErr == nil {
		lastErr = errors.New("unknown openai error")
	}
	return GenerateResult{}, lastErr
}

func (c *OpenAIClient) callResponsesAPI(
	ctx context.Context,
	payload []byte,
	requestedModel string,
) (GenerateResult, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	httpRequest, err := http.NewRequestWithContext(timeoutCtx, http.MethodPost, c.baseURL+"/responses", bytes.NewReader(payload))
	if err != nil {
		return GenerateResult{}, fmt.Errorf("create openai request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	if c.organization != "" {
		httpRequest.Header.Set("OpenAI-Organization", c.organization)
	}

	httpResponse, err := c.httpClient.Do(httpRequest)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
			return GenerateResult{}, fmt.Errorf("openai timeout: %w", err)
		}
		return GenerateResult{}, fmt.Errorf("openai transport error: %w", err)
	}
	defer httpResponse.Body.Close()

	body, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		return GenerateResult{}, fmt.Errorf("read openai body: %w", err)
	}

	if httpResponse.StatusCode < 200 || httpResponse.StatusCode > 299 {
		message := strings.TrimSpace(string(body))
		if len(message) > 700 {
			message = message[:700]
		}
		return GenerateResult{}, &openaiHTTPError{
			StatusCode: httpResponse.StatusCode,
			Message:    message,
		}
	}

	var raw responsesAPIResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return GenerateResult{}, fmt.Errorf("decode openai response: %w", err)
	}

	text := extractResponseText(raw)
	if strings.TrimSpace(text) == "" {
		return GenerateResult{}, errors.New("openai response without text output")
	}

	return GenerateResult{
		Text:    text,
		ModelID: firstNonEmpty(raw.Model, requestedModel),
		Usage: TokenUsage{
			InputTokens:  raw.Usage.InputTokens,
			OutputTokens: raw.Usage.OutputTokens,
			TotalTokens:  raw.Usage.TotalTokens,
		},
	}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

type responsesAPIResponse struct {
	Model  string `json:"model"`
	Output []struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	OutputText string `json:"output_text"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

func extractResponseText(response responsesAPIResponse) string {
	if strings.TrimSpace(response.OutputText) != "" {
		return strings.TrimSpace(response.OutputText)
	}

	fragments := make([]string, 0)
	for _, output := range response.Output {
		for _, content := range output.Content {
			if content.Type != "output_text" && content.Type != "text" {
				continue
			}
			if strings.TrimSpace(content.Text) == "" {
				continue
			}
			fragments = append(fragments, strings.TrimSpace(content.Text))
		}
	}

	return strings.TrimSpace(strings.Join(fragments, "\n"))
}

type openaiHTTPError struct {
	StatusCode int
	Message    string
}

func (e *openaiHTTPError) Error() string {
	return fmt.Sprintf("openai status %d: %s", e.StatusCode, e.Message)
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *openaiHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == http.StatusTooManyRequests || httpErr.StatusCode >= 500
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "timeout") || strings.Contains(message, "tempor") {
		return true
	}
	return false
}
