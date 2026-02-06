package middleware

import (
	"net/http"
	"strconv"
	"strings"
)

const (
	defaultCORSMaxAgeSeconds = 600
)

var (
	defaultCORSAllowedMethods = []string{
		http.MethodGet,
		http.MethodPost,
		http.MethodOptions,
	}
	defaultCORSAllowedHeaders = []string{
		"Accept",
		"Authorization",
		"Content-Type",
		"Idempotency-Key",
		"X-Request-Id",
	}
)

type CORSConfig struct {
	AllowedOrigins []string
	AllowedMethods []string
	AllowedHeaders []string
	MaxAgeSeconds  int
}

func CORS(cfg CORSConfig) func(http.Handler) http.Handler {
	allowedOrigins := normalizeStringList(cfg.AllowedOrigins)
	allowAnyOrigin := false
	for _, origin := range allowedOrigins {
		if origin == "*" {
			allowAnyOrigin = true
			break
		}
	}

	allowedMethods := normalizeStringList(cfg.AllowedMethods)
	if len(allowedMethods) == 0 {
		allowedMethods = append([]string(nil), defaultCORSAllowedMethods...)
	}
	allowedHeaders := normalizeStringList(cfg.AllowedHeaders)
	if len(allowedHeaders) == 0 {
		allowedHeaders = append([]string(nil), defaultCORSAllowedHeaders...)
	}

	maxAgeSeconds := cfg.MaxAgeSeconds
	if maxAgeSeconds <= 0 {
		maxAgeSeconds = defaultCORSMaxAgeSeconds
	}

	allowMethodsValue := strings.Join(allowedMethods, ", ")
	allowHeadersValue := strings.Join(allowedHeaders, ", ")
	maxAgeValue := strconv.Itoa(maxAgeSeconds)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := strings.TrimSpace(r.Header.Get("Origin"))
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			if !allowAnyOrigin && !containsFold(allowedOrigins, origin) {
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Add("Vary", "Origin")
			if allowAnyOrigin {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}

			if r.Method == http.MethodOptions {
				w.Header().Add("Vary", "Access-Control-Request-Method")
				w.Header().Add("Vary", "Access-Control-Request-Headers")
				w.Header().Set("Access-Control-Allow-Methods", allowMethodsValue)
				w.Header().Set("Access-Control-Allow-Headers", allowHeadersValue)
				w.Header().Set("Access-Control-Max-Age", maxAgeValue)
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func normalizeStringList(values []string) []string {
	result := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		result = append(result, value)
	}
	return result
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}
