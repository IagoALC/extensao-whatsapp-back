package httpserver

import (
	"log"
	"net/http"

	"github.com/iago/extensao-whatsapp-back/internal/http/handlers"
	"github.com/iago/extensao-whatsapp-back/internal/http/middleware"
)

type RouterDependencies struct {
	API            *handlers.API
	Logger         *log.Logger
	AuthToken      string
	CORSOrigins    []string
	RateLimitRPS   float64
	RateLimitBurst int
}

func NewRouter(deps RouterDependencies) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", deps.API.Health)
	mux.HandleFunc("/v1/suggestions", deps.API.Suggestions)
	mux.HandleFunc("/v1/summaries", deps.API.Summaries)
	mux.HandleFunc("/v1/reports", deps.API.Reports)
	mux.HandleFunc("/v1/jobs/", deps.API.JobStatus)

	handler := http.Handler(mux)
	handler = middleware.Auth(deps.AuthToken)(handler)
	handler = middleware.RateLimit(deps.RateLimitRPS, deps.RateLimitBurst)(handler)
	handler = middleware.CORS(middleware.CORSConfig{
		AllowedOrigins: deps.CORSOrigins,
	})(handler)
	handler = middleware.Trace(deps.Logger)(handler)
	handler = middleware.RequestID(handler)

	return handler
}
