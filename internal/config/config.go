package config

import (
	"os"
	"strconv"
)

// Config centralizes runtime settings for the API and workers.
type Config struct {
	Port string

	AuthToken string

	DatabaseURL string

	OpenAIAPIKey                  string
	OpenAIBaseURL                 string
	OpenAITimeoutMS               int
	OpenAIMaxRetries              int
	OpenAIModelSuggestionPrimary  string
	OpenAIModelSuggestionFallback string
	OpenAIModelSummaryPrimary     string
	OpenAIModelSummaryFallback    string
	OpenAIModelReportPrimary      string
	OpenAIModelReportFallback     string

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

		OpenAIAPIKey:                  getEnv("OPENAI_API_KEY", ""),
		OpenAIBaseURL:                 getEnv("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		OpenAITimeoutMS:               getEnvInt("OPENAI_TIMEOUT_MS", 15000),
		OpenAIMaxRetries:              getEnvInt("OPENAI_MAX_RETRIES", 2),
		OpenAIModelSuggestionPrimary:  getEnv("OPENAI_MODEL_SUGGESTION_PRIMARY", "gpt-4.1-mini"),
		OpenAIModelSuggestionFallback: getEnv("OPENAI_MODEL_SUGGESTION_FALLBACK", "gpt-4.1-nano"),
		OpenAIModelSummaryPrimary:     getEnv("OPENAI_MODEL_SUMMARY_PRIMARY", "gpt-4.1-mini"),
		OpenAIModelSummaryFallback:    getEnv("OPENAI_MODEL_SUMMARY_FALLBACK", "gpt-4.1-nano"),
		OpenAIModelReportPrimary:      getEnv("OPENAI_MODEL_REPORT_PRIMARY", "gpt-4.1"),
		OpenAIModelReportFallback:     getEnv("OPENAI_MODEL_REPORT_FALLBACK", "gpt-4.1-mini"),

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
