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

var ErrOpenRouterUnavailable = ErrOpenAIUnavailable

type OpenRouterClientConfig struct {
	APIKey     string
	BaseURL    string
	Timeout    time.Duration
	MaxRetries int
	HTTPClient *http.Client
	SiteURL    string
	AppName    string
}

type OpenRouterClient struct {
	apiKey     string
	baseURL    string
	timeout    time.Duration
	maxRetries int
	httpClient *http.Client
	siteURL    string
	appName    string
}

func NewOpenRouterClient(config OpenRouterClientConfig) *OpenRouterClient {
	if strings.TrimSpace(config.BaseURL) == "" {
		config.BaseURL = "https://openrouter.ai/api/v1"
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
	if strings.TrimSpace(config.AppName) == "" {
		config.AppName = "WA Copilot"
	}

	return &OpenRouterClient{
		apiKey:     strings.TrimSpace(config.APIKey),
		baseURL:    strings.TrimSuffix(config.BaseURL, "/"),
		timeout:    config.Timeout,
		maxRetries: config.MaxRetries,
		httpClient: config.HTTPClient,
		siteURL:    strings.TrimSpace(config.SiteURL),
		appName:    strings.TrimSpace(config.AppName),
	}
}

func (c *OpenRouterClient) Available() bool {
	return c.apiKey != ""
}

func (c *OpenRouterClient) Generate(ctx context.Context, request GenerateRequest) (GenerateResult, error) {
	if !c.Available() {
		return GenerateResult{}, ErrOpenRouterUnavailable
	}
	if strings.TrimSpace(request.Model) == "" {
		return GenerateResult{}, errors.New("model is required")
	}
	if strings.TrimSpace(request.Input) == "" {
		return GenerateResult{}, errors.New("input is required")
	}

	messages := make([]map[string]string, 0, 2)
	if strings.TrimSpace(request.Instructions) != "" {
		messages = append(messages, map[string]string{
			"role":    "system",
			"content": strings.TrimSpace(request.Instructions),
		})
	}
	messages = append(messages, map[string]string{
		"role":    "user",
		"content": request.Input,
	})

	payload := map[string]any{
		"model":       request.Model,
		"messages":    messages,
		"temperature": request.Temperature,
		"max_tokens":  request.MaxOutputTokens,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return GenerateResult{}, fmt.Errorf("marshal openrouter payload: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		result, callErr := c.callChatCompletionsAPI(ctx, encoded, request.Model)
		if callErr == nil {
			return result, nil
		}
		lastErr = callErr

		if !isRetryableProviderError(callErr) || attempt == c.maxRetries {
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
		lastErr = errors.New("unknown openrouter error")
	}
	return GenerateResult{}, lastErr
}

func (c *OpenRouterClient) callChatCompletionsAPI(
	ctx context.Context,
	payload []byte,
	requestedModel string,
) (GenerateResult, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	httpRequest, err := http.NewRequestWithContext(
		timeoutCtx,
		http.MethodPost,
		c.baseURL+"/chat/completions",
		bytes.NewReader(payload),
	)
	if err != nil {
		return GenerateResult{}, fmt.Errorf("create openrouter request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	if c.siteURL != "" {
		httpRequest.Header.Set("HTTP-Referer", c.siteURL)
	}
	if c.appName != "" {
		httpRequest.Header.Set("X-Title", c.appName)
	}

	httpResponse, err := c.httpClient.Do(httpRequest)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
			return GenerateResult{}, fmt.Errorf("openrouter timeout: %w", err)
		}
		return GenerateResult{}, fmt.Errorf("openrouter transport error: %w", err)
	}
	defer httpResponse.Body.Close()

	body, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		return GenerateResult{}, fmt.Errorf("read openrouter body: %w", err)
	}

	if httpResponse.StatusCode < 200 || httpResponse.StatusCode > 299 {
		message := strings.TrimSpace(string(body))
		if len(message) > 700 {
			message = message[:700]
		}
		return GenerateResult{}, &providerHTTPError{
			Provider:   "openrouter",
			StatusCode: httpResponse.StatusCode,
			Message:    message,
		}
	}

	var raw openRouterChatCompletionsResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return GenerateResult{}, fmt.Errorf("decode openrouter response: %w", err)
	}

	text := extractOpenRouterText(raw)
	if strings.TrimSpace(text) == "" {
		return GenerateResult{}, errors.New("openrouter response without text output")
	}

	return GenerateResult{
		Text:    text,
		ModelID: providerFirstNonEmpty(raw.Model, requestedModel),
		Usage: TokenUsage{
			InputTokens:  raw.Usage.PromptTokens,
			OutputTokens: raw.Usage.CompletionTokens,
			TotalTokens:  raw.Usage.TotalTokens,
		},
	}, nil
}

type openRouterChatCompletionsResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

func extractOpenRouterText(response openRouterChatCompletionsResponse) string {
	if len(response.Choices) == 0 {
		return ""
	}
	content := response.Choices[0].Message.Content
	switch typed := content.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		fragments := make([]string, 0, len(typed))
		for _, item := range typed {
			fragment, ok := item.(map[string]any)
			if !ok {
				continue
			}
			textValue, _ := fragment["text"].(string)
			if strings.TrimSpace(textValue) == "" {
				continue
			}
			fragments = append(fragments, strings.TrimSpace(textValue))
		}
		return strings.TrimSpace(strings.Join(fragments, "\n"))
	default:
		return ""
	}
}

func providerFirstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

type providerHTTPError struct {
	Provider   string
	StatusCode int
	Message    string
}

func (e *providerHTTPError) Error() string {
	return fmt.Sprintf("%s status %d: %s", e.Provider, e.StatusCode, e.Message)
}

func isRetryableProviderError(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *providerHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == http.StatusTooManyRequests || httpErr.StatusCode >= 500
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "timeout") || strings.Contains(message, "tempor")
}
