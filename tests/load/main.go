package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/iago/extensao-whatsapp-back/internal/ai"
	"github.com/iago/extensao-whatsapp-back/internal/cache"
	contextbuilder "github.com/iago/extensao-whatsapp-back/internal/context"
	httpserver "github.com/iago/extensao-whatsapp-back/internal/http"
	"github.com/iago/extensao-whatsapp-back/internal/http/handlers"
	"github.com/iago/extensao-whatsapp-back/internal/queue"
	"github.com/iago/extensao-whatsapp-back/internal/repository"
	"github.com/iago/extensao-whatsapp-back/internal/service"
	"github.com/iago/extensao-whatsapp-back/internal/worker"
)

type scenarioResult struct {
	Name          string   `json:"name"`
	Total         int      `json:"total"`
	Success       int      `json:"success"`
	Errors        int      `json:"errors"`
	P50MS         float64  `json:"p50_ms"`
	P95MS         float64  `json:"p95_ms"`
	P99MS         float64  `json:"p99_ms"`
	MaxMS         float64  `json:"max_ms"`
	ThroughputRPS float64  `json:"throughput_rps"`
	ErrorSamples  []string `json:"error_samples,omitempty"`
}

type tokenResult struct {
	LegacyTokens    int     `json:"legacy_tokens"`
	OptimizedTokens int     `json:"optimized_tokens"`
	ReductionPct    float64 `json:"reduction_pct"`
}

type runResult struct {
	GeneratedAtUTC string           `json:"generated_at_utc"`
	Environment    string           `json:"environment"`
	Results        []scenarioResult `json:"results"`
	TokenTuning    tokenResult      `json:"token_tuning"`
	SLOEvaluation  map[string]bool  `json:"slo_evaluation"`
}

type benchmarkEnv struct {
	server *httptest.Server
	cancel context.CancelFunc
}

func main() {
	suggestionsTotal := flag.Int("suggestions-total", 260, "total suggestion requests")
	suggestionsConcurrency := flag.Int("suggestions-concurrency", 24, "concurrency for suggestion requests")
	summariesTotal := flag.Int("summaries-total", 180, "total summary enqueue requests")
	summariesConcurrency := flag.Int("summaries-concurrency", 28, "concurrency for summary enqueue requests")
	reportsTotal := flag.Int("reports-total", 180, "total report enqueue requests")
	reportsConcurrency := flag.Int("reports-concurrency", 28, "concurrency for report enqueue requests")
	reportsListTotal := flag.Int("reports-list-total", 120, "total report list requests")
	reportsListConcurrency := flag.Int("reports-list-concurrency", 20, "concurrency for report list requests")
	outputPath := flag.String("output", "", "optional path to persist benchmark results JSON")
	flag.Parse()

	env, err := startBenchmarkEnvironment()
	if err != nil {
		log.Fatalf("failed to start local benchmark environment: %v", err)
	}
	defer env.cancel()
	defer env.server.Close()

	client := &http.Client{Timeout: 10 * time.Second}
	var idCounter int64

	suggestionsScenario := runScenario("suggestions_sync", *suggestionsTotal, *suggestionsConcurrency, func(index int) error {
		payload := map[string]any{
			"conversation": map[string]any{
				"tenant_id":       "default",
				"conversation_id": fmt.Sprintf("chat-%d", index%32),
				"channel":         "whatsapp_web",
			},
			"locale":                    "pt-BR",
			"tone":                      "neutro",
			"context_window":            20 + (index % 6),
			"max_candidates":            3,
			"include_last_user_message": true,
		}
		return postJSON(client, env.server.URL+"/v1/suggestions", payload, nil, http.StatusOK)
	})

	summariesScenario := runScenario("summaries_enqueue", *summariesTotal, *summariesConcurrency, func(index int) error {
		requestID := atomic.AddInt64(&idCounter, 1)
		payload := map[string]any{
			"conversation": map[string]any{
				"tenant_id":       "default",
				"conversation_id": fmt.Sprintf("summary-chat-%d", index%40),
				"channel":         "whatsapp_web",
			},
			"summary_type":    "short",
			"include_actions": true,
		}
		headers := map[string]string{
			"Idempotency-Key": fmt.Sprintf("summary-%d-%d", requestID, time.Now().UnixNano()),
		}
		return postJSON(client, env.server.URL+"/v1/summaries", payload, headers, http.StatusAccepted)
	})

	reportsScenario := runScenario("reports_enqueue", *reportsTotal, *reportsConcurrency, func(index int) error {
		requestID := atomic.AddInt64(&idCounter, 1)
		payload := map[string]any{
			"conversation": map[string]any{
				"tenant_id":       "default",
				"conversation_id": fmt.Sprintf("report-chat-%d", index%40),
				"channel":         "whatsapp_web",
			},
			"report_type":  "timeline",
			"topic_filter": "prazo",
			"page":         1,
			"page_size":    20,
		}
		headers := map[string]string{
			"Idempotency-Key": fmt.Sprintf("report-%d-%d", requestID, time.Now().UnixNano()),
		}
		return postJSON(client, env.server.URL+"/v1/reports", payload, headers, http.StatusAccepted)
	})

	reportsListScenario := runScenario("reports_list", *reportsListTotal, *reportsListConcurrency, func(index int) error {
		query := fmt.Sprintf(
			"%s/v1/reports?tenant_id=default&page=%d&page_size=20&topic=prazo",
			env.server.URL,
			(index%6)+1,
		)
		return getJSON(client, query, http.StatusOK)
	})

	tokenTuning := runTokenReductionScenario()
	results := []scenarioResult{
		suggestionsScenario,
		summariesScenario,
		reportsScenario,
		reportsListScenario,
	}

	slo := map[string]bool{
		"QT-001_summary_endpoint_p95_le_5000ms":    summariesScenario.P95MS <= 5000,
		"QT-002_suggestion_endpoint_p95_le_2000ms": suggestionsScenario.P95MS <= 2000,
	}

	report := runResult{
		GeneratedAtUTC: time.Now().UTC().Format(time.RFC3339Nano),
		Environment:    "local-httptest",
		Results:        results,
		TokenTuning:    tokenTuning,
		SLOEvaluation:  slo,
	}

	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		log.Fatalf("failed to marshal benchmark report: %v", err)
	}

	if *outputPath != "" {
		if err := os.WriteFile(*outputPath, encoded, 0o644); err != nil {
			log.Fatalf("failed to write output file: %v", err)
		}
	}

	_, _ = fmt.Fprintln(os.Stdout, string(encoded))
}

func startBenchmarkEnvironment() (*benchmarkEnv, error) {
	ctx, cancel := context.WithCancel(context.Background())
	logger := log.New(io.Discard, "", 0)

	repo := repository.NewMemoryJobsRepository()
	localQueue := queue.NewLocalQueue(4096, 3, logger)

	modelRouter := ai.NewModelRouter(ai.ModelRouterConfig{})
	contextBuilder := contextbuilder.NewBuilder(contextbuilder.NewBasicRetriever())
	semanticCache := cache.NewSemanticCache(cache.Config{
		TTL:        10 * time.Minute,
		MaxEntries: 4000,
	})
	aiGeneration := service.NewAIGenerationService(service.AIGenerationDependencies{
		Router:  modelRouter,
		Client:  nil,
		Builder: contextBuilder,
		Cache:   semanticCache,
		Logger:  logger,
	})

	jobsService := service.NewJobsService(repo, localQueue)
	suggestionsService := service.NewSuggestionsService(aiGeneration)
	api := handlers.NewAPI(jobsService, suggestionsService)
	router := httpserver.NewRouter(httpserver.RouterDependencies{
		API:            api,
		Logger:         logger,
		AuthToken:      "",
		RateLimitRPS:   20000,
		RateLimitBurst: 20000,
	})

	processor := worker.NewProcessor(localQueue, repo, aiGeneration, logger)
	go processor.Start(ctx)

	server := httptest.NewServer(router)
	return &benchmarkEnv{
		server: server,
		cancel: cancel,
	}, nil
}

func runScenario(
	name string,
	total int,
	concurrency int,
	requestFn func(index int) error,
) scenarioResult {
	if total <= 0 {
		return scenarioResult{Name: name}
	}
	if concurrency <= 0 {
		concurrency = 1
	}

	startedAt := time.Now()
	type sample struct {
		durationMS float64
		err        string
	}

	jobs := make(chan int, total)
	results := make(chan sample, total)
	for i := 0; i < total; i++ {
		jobs <- i
	}
	close(jobs)

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				requestStart := time.Now()
				err := requestFn(index)
				s := sample{
					durationMS: float64(time.Since(requestStart).Microseconds()) / 1000.0,
				}
				if err != nil {
					s.err = err.Error()
				}
				results <- s
			}
		}()
	}
	wg.Wait()
	close(results)

	durations := make([]float64, 0, total)
	errorSamples := make([]string, 0, 5)
	success := 0
	errorsCount := 0
	for item := range results {
		durations = append(durations, item.durationMS)
		if item.err == "" {
			success++
			continue
		}
		errorsCount++
		if len(errorSamples) < 5 {
			errorSamples = append(errorSamples, item.err)
		}
	}

	sort.Float64s(durations)
	elapsedSeconds := time.Since(startedAt).Seconds()
	throughput := 0.0
	if elapsedSeconds > 0 {
		throughput = float64(total) / elapsedSeconds
	}

	result := scenarioResult{
		Name:          name,
		Total:         total,
		Success:       success,
		Errors:        errorsCount,
		P50MS:         percentile(durations, 0.50),
		P95MS:         percentile(durations, 0.95),
		P99MS:         percentile(durations, 0.99),
		MaxMS:         percentile(durations, 1.00),
		ThroughputRPS: round2(throughput),
		ErrorSamples:  errorSamples,
	}
	return result
}

func postJSON(
	client *http.Client,
	url string,
	payload any,
	headers map[string]string,
	expectedStatus int,
) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}

	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != expectedStatus {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return fmt.Errorf("unexpected status %d (expected %d): %s", response.StatusCode, expectedStatus, string(body))
	}
	_, _ = io.Copy(io.Discard, response.Body)
	return nil
}

func getJSON(client *http.Client, url string, expectedStatus int) error {
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	request.Header.Set("Accept", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != expectedStatus {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return fmt.Errorf("unexpected status %d (expected %d): %s", response.StatusCode, expectedStatus, string(body))
	}
	_, _ = io.Copy(io.Discard, response.Body)
	return nil
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	if p <= 0 {
		return round2(values[0])
	}
	if p >= 1 {
		return round2(values[len(values)-1])
	}
	rank := int(math.Ceil(float64(len(values))*p)) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(values) {
		rank = len(values) - 1
	}
	return round2(values[rank])
}

func runTokenReductionScenario() tokenResult {
	builder := contextbuilder.NewBuilder(contextbuilder.NewBasicRetriever())

	payload := map[string]any{
		"context_window": 20,
		"messages": []string{
			"Cliente pediu retorno ainda hoje sobre o status da entrega.",
			"Cliente pediu retorno ainda hoje sobre o status da entrega.",
			"Cliente pediu retorno ainda hoje sobre o status da entrega.",
			"Existe risco de atraso por dependencia de aprovacao interna.",
			"Existe risco de atraso por dependencia de aprovacao interna.",
			"Equipe de suporte pediu linguagem mais formal na resposta.",
			"Equipe de suporte pediu linguagem mais formal na resposta.",
			"Registrar plano de acao e proximo prazo com o cliente.",
			"Registrar plano de acao e proximo prazo com o cliente.",
			"Registrar plano de acao e proximo prazo com o cliente.",
		},
	}
	encoded, _ := json.Marshal(payload)

	optimized, err := builder.Build(context.Background(), contextbuilder.BuildInput{
		Task:           "suggestion",
		TenantID:       "default",
		ConversationID: "token-case",
		Payload:        encoded,
		MaxInputTokens: 2500,
		MaxChunks:      6,
		ContextWindow:  20,
	})
	if err != nil {
		return tokenResult{}
	}

	legacyTokens := legacyTokenEstimate(encoded, 2500, 8)
	if legacyTokens <= 0 {
		legacyTokens = optimized.TokenCount
	}

	reduction := 0.0
	if legacyTokens > 0 {
		reduction = (float64(legacyTokens-optimized.TokenCount) / float64(legacyTokens)) * 100
	}

	return tokenResult{
		LegacyTokens:    legacyTokens,
		OptimizedTokens: optimized.TokenCount,
		ReductionPct:    round2(reduction),
	}
}

func legacyTokenEstimate(payload []byte, maxTokens int, maxChunks int) int {
	if maxTokens <= 0 {
		maxTokens = 2500
	}
	if maxChunks <= 0 {
		maxChunks = 8
	}

	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return 0
	}

	fragments := make([]string, 0, 18)
	collectLegacyFragments(decoded, &fragments, 18)
	if len(fragments) == 0 {
		return 0
	}

	totalTokens := 0
	selected := 0
	for _, fragment := range fragments {
		tokens := len([]rune(strings.TrimSpace(fragment))) / 4
		if tokens <= 0 {
			tokens = 1
		}
		if totalTokens+tokens > maxTokens {
			continue
		}
		totalTokens += tokens
		selected++
		if selected >= maxChunks {
			break
		}
	}
	return totalTokens
}

func collectLegacyFragments(value any, target *[]string, limit int) {
	if len(*target) >= limit {
		return
	}
	switch typed := value.(type) {
	case map[string]any:
		for _, nested := range typed {
			if len(*target) >= limit {
				return
			}
			collectLegacyFragments(nested, target, limit)
		}
	case []any:
		for _, nested := range typed {
			if len(*target) >= limit {
				return
			}
			collectLegacyFragments(nested, target, limit)
		}
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return
		}
		if len(trimmed) > 700 {
			trimmed = trimmed[:700]
		}
		*target = append(*target, trimmed)
	case float64:
		*target = append(*target, strconv.FormatFloat(typed, 'f', -1, 64))
	}
}

func round2(value float64) float64 {
	return math.Round(value*100) / 100
}
