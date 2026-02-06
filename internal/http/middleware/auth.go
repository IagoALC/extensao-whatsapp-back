package middleware

import (
	"net/http"
	"strings"
)

func Auth(requiredToken string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/v1/") {
				next.ServeHTTP(w, r)
				return
			}

			if requiredToken == "" {
				next.ServeHTTP(w, r)
				return
			}

			authorization := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(authorization, prefix) {
				writeUnauthorized(w, r)
				return
			}

			token := strings.TrimSpace(strings.TrimPrefix(authorization, prefix))
			if token == "" || token != requiredToken {
				writeUnauthorized(w, r)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func writeUnauthorized(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"authentication required"},"request_id":"` + GetRequestID(r.Context()) + `"}`))
}
