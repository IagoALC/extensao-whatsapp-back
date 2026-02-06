package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCORSPreflightAllowedOrigin(t *testing.T) {
	nextCalled := false
	handler := CORS(CORSConfig{
		AllowedOrigins: []string{"https://web.whatsapp.com"},
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusTeapot)
	}))

	request := httptest.NewRequest(http.MethodOptions, "/v1/suggestions", nil)
	request.Header.Set("Origin", "https://web.whatsapp.com")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, recorder.Code)
	}
	if nextCalled {
		t.Fatalf("expected preflight to short-circuit chain")
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://web.whatsapp.com" {
		t.Fatalf("expected allow origin header, got %q", got)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, http.MethodPost) {
		t.Fatalf("expected POST in allow methods, got %q", got)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(strings.ToLower(got), "authorization") {
		t.Fatalf("expected authorization in allow headers, got %q", got)
	}
}

func TestCORSAllowsActualRequestFromAllowedOrigin(t *testing.T) {
	handler := CORS(CORSConfig{
		AllowedOrigins: []string{"https://web.whatsapp.com"},
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	request := httptest.NewRequest(http.MethodPost, "/v1/suggestions", nil)
	request.Header.Set("Origin", "https://web.whatsapp.com")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://web.whatsapp.com" {
		t.Fatalf("expected allow origin header, got %q", got)
	}
}

func TestCORSIgnoresDisallowedOrigin(t *testing.T) {
	nextCalled := false
	handler := CORS(CORSConfig{
		AllowedOrigins: []string{"https://web.whatsapp.com"},
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	request := httptest.NewRequest(http.MethodOptions, "/v1/suggestions", nil)
	request.Header.Set("Origin", "https://evil.example")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected passthrough status %d, got %d", http.StatusOK, recorder.Code)
	}
	if !nextCalled {
		t.Fatalf("expected disallowed origin preflight to pass through")
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no allow-origin header for disallowed origin, got %q", got)
	}
}
