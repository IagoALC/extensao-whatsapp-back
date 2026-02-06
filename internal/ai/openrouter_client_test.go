package ai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpenRouterClientGenerateSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not_found"}`))
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model":"openai/gpt-4.1-mini",
			"choices":[{"message":{"role":"assistant","content":"{\"suggestions\":[{\"content\":\"ok\"}]}"}}],
			"usage":{"prompt_tokens":123,"completion_tokens":22,"total_tokens":145}
		}`))
	}))
	defer server.Close()

	client := NewOpenRouterClient(OpenRouterClientConfig{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		Timeout:    2 * time.Second,
		MaxRetries: 1,
		AppName:    "WA Copilot Test",
	})

	result, err := client.Generate(context.Background(), GenerateRequest{
		Model:           "openai/gpt-4.1-mini",
		Instructions:    "Return JSON only",
		Input:           "test prompt",
		Temperature:     0.2,
		MaxOutputTokens: 500,
	})
	if err != nil {
		t.Fatalf("expected success, got err=%v", err)
	}
	if result.Text == "" {
		t.Fatalf("expected non-empty text")
	}
	if result.Usage.TotalTokens != 145 {
		t.Fatalf("expected total tokens 145, got %d", result.Usage.TotalTokens)
	}
}

func TestOpenRouterClientRetriesOnRateLimit(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		current := atomic.AddInt32(&calls, 1)
		if current == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate_limited"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model":"openai/gpt-4.1-mini",
			"choices":[{"message":{"role":"assistant","content":"{\"ok\":true}"}}],
			"usage":{"prompt_tokens":10,"completion_tokens":10,"total_tokens":20}
		}`))
	}))
	defer server.Close()

	client := NewOpenRouterClient(OpenRouterClientConfig{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		Timeout:    2 * time.Second,
		MaxRetries: 2,
	})
	result, err := client.Generate(context.Background(), GenerateRequest{
		Model:           "openai/gpt-4.1-mini",
		Instructions:    "Return JSON only",
		Input:           "test",
		Temperature:     0.2,
		MaxOutputTokens: 200,
	})
	if err != nil {
		t.Fatalf("expected success after retry, got err=%v", err)
	}
	if result.Text == "" {
		t.Fatalf("expected non-empty text after retry")
	}
	if atomic.LoadInt32(&calls) < 2 {
		t.Fatalf("expected at least 2 calls, got %d", calls)
	}
}

func TestOpenRouterClientParsesArrayContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model":"openai/gpt-4.1-mini",
			"choices":[{"message":{"role":"assistant","content":[{"type":"text","text":"linha 1"},{"type":"text","text":"linha 2"}]}}],
			"usage":{"prompt_tokens":5,"completion_tokens":5,"total_tokens":10}
		}`))
	}))
	defer server.Close()

	client := NewOpenRouterClient(OpenRouterClientConfig{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		Timeout:    2 * time.Second,
		MaxRetries: 1,
	})
	result, err := client.Generate(context.Background(), GenerateRequest{
		Model:           "openai/gpt-4.1-mini",
		Instructions:    "Return JSON only",
		Input:           "test",
		Temperature:     0.2,
		MaxOutputTokens: 200,
	})
	if err != nil {
		t.Fatalf("expected success, got err=%v", err)
	}
	if got := result.Text; got != "linha 1\nlinha 2" {
		t.Fatalf("unexpected parsed text: %q", got)
	}
}

func TestOpenRouterClientUnavailableWithoutKey(t *testing.T) {
	client := NewOpenRouterClient(OpenRouterClientConfig{
		APIKey: "",
	})
	_, err := client.Generate(context.Background(), GenerateRequest{
		Model:           "openai/gpt-4.1-mini",
		Instructions:    "Return JSON only",
		Input:           "test",
		Temperature:     0.2,
		MaxOutputTokens: 200,
	})
	if err == nil {
		t.Fatalf("expected unavailable error")
	}
	if err != ErrOpenRouterUnavailable {
		t.Fatalf("expected ErrOpenRouterUnavailable, got %v", err)
	}
}

func TestOpenRouterClientSendsOptionalHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("HTTP-Referer"); got != "https://example.com" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(fmt.Sprintf(`{"error":"unexpected referer %q"}`, got)))
			return
		}
		if got := r.Header.Get("X-Title"); got != "WA Copilot" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(fmt.Sprintf(`{"error":"unexpected title %q"}`, got)))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model":"openai/gpt-4.1-mini",
			"choices":[{"message":{"role":"assistant","content":"{\"ok\":true}"}}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`))
	}))
	defer server.Close()

	client := NewOpenRouterClient(OpenRouterClientConfig{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		Timeout:    2 * time.Second,
		MaxRetries: 1,
		SiteURL:    "https://example.com",
		AppName:    "WA Copilot",
	})
	_, err := client.Generate(context.Background(), GenerateRequest{
		Model:           "openai/gpt-4.1-mini",
		Instructions:    "Return JSON only",
		Input:           "test",
		Temperature:     0.2,
		MaxOutputTokens: 200,
	})
	if err != nil {
		t.Fatalf("expected success with optional headers, got err=%v", err)
	}
}
