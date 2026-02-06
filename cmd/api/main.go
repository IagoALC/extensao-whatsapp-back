package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/iago/extensao-whatsapp-back/internal/ai"
	"github.com/iago/extensao-whatsapp-back/internal/cache"
	"github.com/iago/extensao-whatsapp-back/internal/config"
	contextbuilder "github.com/iago/extensao-whatsapp-back/internal/context"
	httpserver "github.com/iago/extensao-whatsapp-back/internal/http"
	"github.com/iago/extensao-whatsapp-back/internal/http/handlers"
	"github.com/iago/extensao-whatsapp-back/internal/queue"
	"github.com/iago/extensao-whatsapp-back/internal/repository"
	"github.com/iago/extensao-whatsapp-back/internal/service"
	"github.com/iago/extensao-whatsapp-back/internal/worker"
)

func main() {
	logger := log.New(os.Stdout, "[wa-back] ", log.LstdFlags|log.LUTC|log.Lmicroseconds)
	if err := config.LoadDotEnv(".env", ".env.local"); err != nil {
		logger.Printf("failed loading .env files: %v", err)
	}
	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	repo, repoCloser := setupRepository(ctx, cfg, logger)
	defer repoCloser()

	producer, consumer, queueCloser := setupQueue(ctx, cfg, logger)
	defer queueCloser()

	modelRouter := ai.NewModelRouter(ai.ModelRouterConfig{
		SuggestionPrimary:  cfg.OpenRouterModelSuggestionPrimary,
		SuggestionFallback: cfg.OpenRouterModelSuggestionFallback,
		SummaryPrimary:     cfg.OpenRouterModelSummaryPrimary,
		SummaryFallback:    cfg.OpenRouterModelSummaryFallback,
		ReportPrimary:      cfg.OpenRouterModelReportPrimary,
		ReportFallback:     cfg.OpenRouterModelReportFallback,
	})
	aiClient := ai.NewOpenRouterClient(ai.OpenRouterClientConfig{
		APIKey:     cfg.OpenRouterAPIKey,
		BaseURL:    cfg.OpenRouterBaseURL,
		Timeout:    time.Duration(cfg.OpenRouterTimeoutMS) * time.Millisecond,
		MaxRetries: cfg.OpenRouterMaxRetries,
		SiteURL:    cfg.OpenRouterSiteURL,
		AppName:    cfg.OpenRouterAppName,
	})
	contextBuilder := contextbuilder.NewBuilder(contextbuilder.NewBasicRetriever())
	semanticCache := cache.NewSemanticCache(cache.Config{
		TTL:        time.Duration(cfg.SemanticCacheTTLSeconds) * time.Second,
		MaxEntries: cfg.SemanticCacheMaxEntries,
	})
	aiGeneration := service.NewAIGenerationService(service.AIGenerationDependencies{
		Router:     modelRouter,
		Client:     aiClient,
		Builder:    contextBuilder,
		Cache:      semanticCache,
		PromptsDir: cfg.PromptsDir,
		Logger:     logger,
	})

	jobsService := service.NewJobsService(repo, producer)
	suggestionsService := service.NewSuggestionsService(aiGeneration)
	api := handlers.NewAPI(jobsService, suggestionsService)

	handler := httpserver.NewRouter(httpserver.RouterDependencies{
		API:            api,
		Logger:         logger,
		AuthToken:      cfg.AuthToken,
		CORSOrigins:    cfg.CORSAllowedOrigins,
		RateLimitRPS:   cfg.RateLimitRPS,
		RateLimitBurst: cfg.RateLimitBurst,
	})

	if cfg.WorkerEnabled {
		processor := worker.NewProcessor(consumer, repo, aiGeneration, logger)
		go processor.Start(ctx)
		logger.Printf("worker enabled and started")
	} else {
		logger.Printf("worker disabled by configuration")
	}

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errChan := make(chan error, 1)
	go func() {
		logger.Printf("api listening on :%s", cfg.Port)
		errChan <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		logger.Printf("shutdown signal received")
	case err := <-errChan:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Printf("server failed: %v", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Printf("graceful shutdown failed: %v", err)
	}
}

func setupRepository(
	ctx context.Context,
	cfg config.Config,
	logger *log.Logger,
) (repository.JobsRepository, func()) {
	if cfg.DatabaseURL == "" {
		logger.Printf("DATABASE_URL not configured, using in-memory repository")
		return repository.NewMemoryJobsRepository(), func() {}
	}

	pgRepo, err := repository.NewPostgresJobsRepository(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Printf("failed to initialize postgres repository, fallback to memory: %v", err)
		return repository.NewMemoryJobsRepository(), func() {}
	}
	logger.Printf("postgres repository initialized")
	return pgRepo, func() {
		pgRepo.Close()
	}
}

func setupQueue(
	ctx context.Context,
	cfg config.Config,
	logger *log.Logger,
) (queue.Producer, queue.Consumer, func()) {
	var (
		baseProducer queue.Producer
		consumer     queue.Consumer
		baseCloser   = func() {}
	)

	if cfg.RedisAddr == "" {
		logger.Printf("REDIS_ADDR not configured, using local queue fallback")
		local := queue.NewLocalQueue(512, 3, logger)
		baseProducer = local
		consumer = local
	} else {
		streams, err := queue.NewStreamsQueue(ctx, queue.StreamsConfig{
			Addr:        cfg.RedisAddr,
			Password:    cfg.RedisPassword,
			DB:          cfg.RedisDB,
			Stream:      cfg.RedisStream,
			DLQStream:   cfg.RedisDLQ,
			Group:       cfg.RedisGroup,
			Consumer:    cfg.RedisConsumer,
			MaxAttempts: 3,
		})
		if err != nil {
			logger.Printf("failed to initialize redis streams queue, fallback to local: %v", err)
			local := queue.NewLocalQueue(512, 3, logger)
			baseProducer = local
			consumer = local
		} else {
			logger.Printf("redis streams queue initialized")
			baseProducer = streams
			consumer = streams
			baseCloser = func() {
				_ = streams.Close()
			}
		}
	}

	producer := baseProducer
	batchingCloser := func() {}
	if cfg.QueueBatchingEnabled {
		batching := queue.NewBatchingProducer(ctx, baseProducer, queue.BatchingConfig{
			MaxBatchSize:       cfg.QueueBatchSize,
			FlushInterval:      time.Duration(cfg.QueueBatchFlushMS) * time.Millisecond,
			FlushTimeout:       time.Duration(cfg.QueueBatchFlushTimeoutMS) * time.Millisecond,
			QueueCapacity:      cfg.QueueBatchQueueCapacity,
			MaxInFlightBatches: cfg.QueueBatchMaxInFlight,
		})
		producer = batching
		batchingCloser = batching.Close
		logger.Printf(
			"queue batching enabled size=%d flush_ms=%d queue_capacity=%d max_in_flight=%d",
			cfg.QueueBatchSize,
			cfg.QueueBatchFlushMS,
			cfg.QueueBatchQueueCapacity,
			cfg.QueueBatchMaxInFlight,
		)
	}

	return producer, consumer, func() {
		batchingCloser()
		baseCloser()
	}
}
