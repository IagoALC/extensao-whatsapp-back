package config

import (
	"os"
	"strconv"
	"strings"
)

// Config centralizes runtime settings for the API and workers.
type Config struct {
	Port string

	AuthToken string

	DatabaseURL string

	OpenRouterAPIKey                  string
	OpenRouterBaseURL                 string
	OpenRouterTimeoutMS               int
	OpenRouterMaxRetries              int
	OpenRouterSiteURL                 string
	OpenRouterAppName                 string
	OpenRouterModelSuggestionPrimary  string
	OpenRouterModelSuggestionFallback string
	OpenRouterModelSummaryPrimary     string
	OpenRouterModelSummaryFallback    string
	OpenRouterModelReportPrimary      string
	OpenRouterModelReportFallback     string

	SemanticCacheTTLSeconds int
	SemanticCacheMaxEntries int
	PromptsDir              string

	RedisAddr     string
	RedisPassword string
	RedisDB       int
	RedisStream   string
	RedisDLQ      string
	RedisGroup    string
	RedisConsumer string

	RateLimitRPS   float64
	RateLimitBurst int

	CORSAllowedOrigins []string

	QueueBatchingEnabled     bool
	QueueBatchSize           int
	QueueBatchFlushMS        int
	QueueBatchFlushTimeoutMS int
	QueueBatchQueueCapacity  int
	QueueBatchMaxInFlight    int

	WorkerEnabled bool
}

func Load() Config {
	return Config{
		Port: getEnv("PORT", "8080"),

		AuthToken: getEnv("API_AUTH_TOKEN", ""),

		DatabaseURL: getEnv("DATABASE_URL", ""),

		OpenRouterAPIKey:                  getEnvOr("OPENROUTER_API_KEY", getEnv("OPENAI_API_KEY", "")),
		OpenRouterBaseURL:                 getEnvOr("OPENROUTER_BASE_URL", getEnv("OPENAI_BASE_URL", "https://openrouter.ai/api/v1")),
		OpenRouterTimeoutMS:               getEnvIntOr("OPENROUTER_TIMEOUT_MS", getEnvInt("OPENAI_TIMEOUT_MS", 15000)),
		OpenRouterMaxRetries:              getEnvIntOr("OPENROUTER_MAX_RETRIES", getEnvInt("OPENAI_MAX_RETRIES", 2)),
		OpenRouterSiteURL:                 getEnv("OPENROUTER_SITE_URL", ""),
		OpenRouterAppName:                 getEnv("OPENROUTER_APP_NAME", "WA Copilot"),
		OpenRouterModelSuggestionPrimary:  getEnvOr("OPENROUTER_MODEL_SUGGESTION_PRIMARY", getEnv("OPENAI_MODEL_SUGGESTION_PRIMARY", "openai/gpt-4o-mini")),
		OpenRouterModelSuggestionFallback: getEnvOr("OPENROUTER_MODEL_SUGGESTION_FALLBACK", getEnv("OPENAI_MODEL_SUGGESTION_FALLBACK", "openai/gpt-4o-mini")),
		OpenRouterModelSummaryPrimary:     getEnvOr("OPENROUTER_MODEL_SUMMARY_PRIMARY", getEnv("OPENAI_MODEL_SUMMARY_PRIMARY", "openai/gpt-4o-mini")),
		OpenRouterModelSummaryFallback:    getEnvOr("OPENROUTER_MODEL_SUMMARY_FALLBACK", getEnv("OPENAI_MODEL_SUMMARY_FALLBACK", "openai/gpt-4o-mini")),
		OpenRouterModelReportPrimary:      getEnvOr("OPENROUTER_MODEL_REPORT_PRIMARY", getEnv("OPENAI_MODEL_REPORT_PRIMARY", "openai/gpt-4o-mini")),
		OpenRouterModelReportFallback:     getEnvOr("OPENROUTER_MODEL_REPORT_FALLBACK", getEnv("OPENAI_MODEL_REPORT_FALLBACK", "openai/gpt-4o-mini")),

		SemanticCacheTTLSeconds: getEnvInt("SEMANTIC_CACHE_TTL_SECONDS", 900),
		SemanticCacheMaxEntries: getEnvInt("SEMANTIC_CACHE_MAX_ENTRIES", 2000),
		PromptsDir:              getEnv("PROMPTS_DIR", "prompts"),

		RedisAddr:     getEnv("REDIS_ADDR", ""),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),
		RedisDB:       getEnvInt("REDIS_DB", 0),
		RedisStream:   getEnv("REDIS_STREAM", "wa_jobs"),
		RedisDLQ:      getEnv("REDIS_DLQ_STREAM", "wa_jobs_dlq"),
		RedisGroup:    getEnv("REDIS_GROUP", "wa_workers"),
		RedisConsumer: getEnv("REDIS_CONSUMER", "api-1"),

		RateLimitRPS:   getEnvFloat("RATE_LIMIT_RPS", 20),
		RateLimitBurst: getEnvInt("RATE_LIMIT_BURST", 40),

		CORSAllowedOrigins: getEnvCSV("CORS_ALLOWED_ORIGINS", []string{"https://web.whatsapp.com"}),

		QueueBatchingEnabled:     getEnvBool("QUEUE_BATCHING_ENABLED", true),
		QueueBatchSize:           getEnvInt("QUEUE_BATCH_SIZE", 32),
		QueueBatchFlushMS:        getEnvInt("QUEUE_BATCH_FLUSH_MS", 25),
		QueueBatchFlushTimeoutMS: getEnvInt("QUEUE_BATCH_FLUSH_TIMEOUT_MS", 3000),
		QueueBatchQueueCapacity:  getEnvInt("QUEUE_BATCH_QUEUE_CAPACITY", 2048),
		QueueBatchMaxInFlight:    getEnvInt("QUEUE_BATCH_MAX_IN_FLIGHT", 4),

		WorkerEnabled: getEnvBool("WORKER_ENABLED", true),
	}
}

func getEnv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func getEnvInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getEnvFloat(key string, fallback float64) float64 {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func getEnvBool(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getEnvOr(primary string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(primary))
	if value != "" {
		return value
	}
	return fallback
}

func getEnvIntOr(primary string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(primary))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getEnvCSV(key string, fallback []string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return append([]string(nil), fallback...)
	}

	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		result = append(result, trimmed)
	}
	if len(result) == 0 {
		return append([]string(nil), fallback...)
	}
	return result
}
