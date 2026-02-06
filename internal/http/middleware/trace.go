package middleware

import (
	"log"
	"net/http"
	"time"
)

func Trace(logger *log.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			next.ServeHTTP(w, r)
			if logger != nil {
				logger.Printf(
					"trace request_id=%s method=%s path=%s duration_ms=%d",
					GetRequestID(r.Context()),
					r.Method,
					r.URL.Path,
					time.Since(start).Milliseconds(),
				)
			}
		})
	}
}
