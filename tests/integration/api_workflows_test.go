package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

type integrationRuntime struct {
	server *httptest.Server
	cancel context.CancelFunc
}

func startIntegrationRuntime(t *testing.T) integrationRuntime {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	logger := log.New(io.Discard, "", 0)
	repo := repository.NewMemoryJobsRepository()
	localQueue := queue.NewLocalQueue(2048, 3, logger)

	modelRouter := ai.NewModelRouter(ai.ModelRouterConfig{})
	contextBuilder := contextbuilder.NewBuilder(contextbuilder.NewBasicRetriever())
	semanticCache := cache.NewSemanticCache(cache.Config{
		TTL:        10 * time.Minute,
		MaxEntries: 4000,
	})
	aiGeneration := service.NewAIGenerationService(service.AIGenerationDependencies{
		Router:  modelRouter,
		Client:  nil, // fallback path for deterministic local integration tests.
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
	return integrationRuntime{
		server: server,
		cancel: func() {
			cancel()
			server.Close()
		},
	}
}

func postJSON(
	t *testing.T,
	client *http.Client,
	url string,
	payload any,
	headers map[string]string,
) (int, map[string]any) {
	t.Helper()

	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("execute request: %v", err)
	}
	defer response.Body.Close()

	raw, _ := io.ReadAll(response.Body)
	if len(raw) == 0 {
		return response.StatusCode, map[string]any{}
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode response body (%d): %s", response.StatusCode, string(raw))
	}

	return response.StatusCode, decoded
}

func getJSON(t *testing.T, client *http.Client, url string) (int, map[string]any) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build get request: %v", err)
	}
	request.Header.Set("Accept", "application/json")

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("execute get request: %v", err)
	}
	defer response.Body.Close()

	raw, _ := io.ReadAll(response.Body)
	if len(raw) == 0 {
		return response.StatusCode, map[string]any{}
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode get response body (%d): %s", response.StatusCode, string(raw))
	}

	return response.StatusCode, decoded
}

func waitForJobDone(
	t *testing.T,
	client *http.Client,
	baseURL string,
	jobID string,
	timeout time.Duration,
) map[string]any {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status, body := getJSON(t, client, fmt.Sprintf("%s/v1/jobs/%s", baseURL, jobID))
		if status != http.StatusOK {
			time.Sleep(20 * time.Millisecond)
			continue
		}

		jobStatus, _ := body["status"].(string)
		if jobStatus == "done" {
			return body
		}
		if jobStatus == "failed" {
			t.Fatalf("job %s failed: %+v", jobID, body)
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("timeout waiting for job %s to reach done", jobID)
	return nil
}

func TestCriticalFlowsSummarySuggestionReport(t *testing.T) {
	runtime := startIntegrationRuntime(t)
	defer runtime.cancel()

	client := runtime.server.Client()
	baseURL := runtime.server.URL

	suggestionPayload := map[string]any{
		"conversation": map[string]any{
			"tenant_id":       "default",
			"conversation_id": "chat-e2e-1",
			"channel":         "whatsapp_web",
		},
		"locale":                    "pt-BR",
		"tone":                      "neutro",
		"context_window":            24,
		"max_candidates":            3,
		"include_last_user_message": true,
	}
	suggestionStatus, suggestionBody := postJSON(
		t,
		client,
		baseURL+"/v1/suggestions",
		suggestionPayload,
		nil,
	)
	if suggestionStatus != http.StatusOK {
		t.Fatalf("expected 200 from suggestions, got %d body=%+v", suggestionStatus, suggestionBody)
	}

	suggestions, ok := suggestionBody["suggestions"].([]any)
	if !ok || len(suggestions) == 0 {
		t.Fatalf("expected non-empty suggestions payload, got %+v", suggestionBody)
	}
	if required, _ := suggestionBody["hitl_required"].(bool); !required {
		t.Fatalf("expected hitl_required=true, got %+v", suggestionBody["hitl_required"])
	}
	hitl, ok := suggestionBody["hitl"].(map[string]any)
	if !ok {
		t.Fatalf("expected hitl metadata in suggestion response")
	}
	if required, _ := hitl["required"].(bool); !required {
		t.Fatalf("expected hitl.required=true, got %+v", hitl["required"])
	}

	summaryPayload := map[string]any{
		"conversation": map[string]any{
			"tenant_id":       "default",
			"conversation_id": "chat-e2e-1",
			"channel":         "whatsapp_web",
		},
		"summary_type":    "short",
		"include_actions": true,
	}
	summaryStatus, summaryBody := postJSON(
		t,
		client,
		baseURL+"/v1/summaries",
		summaryPayload,
		map[string]string{
			"Idempotency-Key": "summary-e2e-flow-0001",
		},
	)
	if summaryStatus != http.StatusAccepted {
		t.Fatalf("expected 202 from summaries, got %d body=%+v", summaryStatus, summaryBody)
	}
	summaryJobID, _ := summaryBody["job_id"].(string)
	if strings.TrimSpace(summaryJobID) == "" {
		t.Fatalf("expected summary job id, got %+v", summaryBody)
	}

	summaryJob := waitForJobDone(t, client, baseURL, summaryJobID, 4*time.Second)
	summaryResult, ok := summaryJob["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected summary result payload in job status, got %+v", summaryJob)
	}
	if strings.TrimSpace(fmt.Sprintf("%v", summaryResult["summary"])) == "" {
		t.Fatalf("expected non-empty summary text in result: %+v", summaryResult)
	}
	if score, _ := summaryResult["quality_score"].(float64); score <= 0 {
		t.Fatalf("expected positive quality_score in summary result: %+v", summaryResult)
	}

	reportPayload := map[string]any{
		"conversation": map[string]any{
			"tenant_id":       "default",
			"conversation_id": "chat-e2e-1",
			"channel":         "whatsapp_web",
		},
		"report_type":  "timeline",
		"topic_filter": "prazo",
		"page":         1,
		"page_size":    20,
	}
	reportStatus, reportBody := postJSON(
		t,
		client,
		baseURL+"/v1/reports",
		reportPayload,
		map[string]string{
			"Idempotency-Key": "report-e2e-flow-0001",
		},
	)
	if reportStatus != http.StatusAccepted {
		t.Fatalf("expected 202 from reports, got %d body=%+v", reportStatus, reportBody)
	}
	reportJobID, _ := reportBody["job_id"].(string)
	if strings.TrimSpace(reportJobID) == "" {
		t.Fatalf("expected report job id, got %+v", reportBody)
	}

	reportJob := waitForJobDone(t, client, baseURL, reportJobID, 4*time.Second)
	reportResult, ok := reportJob["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected report result payload in job status, got %+v", reportJob)
	}
	if strings.TrimSpace(fmt.Sprintf("%v", reportResult["title"])) == "" {
		t.Fatalf("expected non-empty report title: %+v", reportResult)
	}
	reportSections, ok := reportResult["sections"].([]any)
	if !ok || len(reportSections) == 0 {
		t.Fatalf("expected report sections in payload: %+v", reportResult)
	}

	listStatus, listBody := getJSON(
		t,
		client,
		baseURL+"/v1/reports?tenant_id=default&page=1&page_size=20&topic=prazo",
	)
	if listStatus != http.StatusOK {
		t.Fatalf("expected 200 from report list, got %d body=%+v", listStatus, listBody)
	}
	items, ok := listBody["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("expected non-empty report list items, got %+v", listBody)
	}
}

func TestPolicyBlocksAndHITLMetadata(t *testing.T) {
	runtime := startIntegrationRuntime(t)
	defer runtime.cancel()

	client := runtime.server.Client()
	baseURL := runtime.server.URL

	blockedSuggestionPayload := map[string]any{
		"conversation": map[string]any{
			"tenant_id":       "default",
			"conversation_id": "chat-phishing-42",
			"channel":         "whatsapp_web",
		},
		"locale":         "pt-BR",
		"tone":           "neutro",
		"context_window": 20,
	}
	blockedStatus, blockedBody := postJSON(
		t,
		client,
		baseURL+"/v1/suggestions",
		blockedSuggestionPayload,
		nil,
	)
	if blockedStatus != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 from blocked suggestion request, got %d body=%+v", blockedStatus, blockedBody)
	}
	errorEnvelope, ok := blockedBody["error"].(map[string]any)
	if !ok || fmt.Sprintf("%v", errorEnvelope["code"]) != "policy_violation" {
		t.Fatalf("expected policy_violation error envelope, got %+v", blockedBody)
	}

	blockedReportPayload := map[string]any{
		"conversation": map[string]any{
			"tenant_id":       "default",
			"conversation_id": "chat-safe",
			"channel":         "whatsapp_web",
		},
		"report_type":  "timeline",
		"topic_filter": "phishing campaign",
	}
	blockedReportStatus, blockedReportBody := postJSON(
		t,
		client,
		baseURL+"/v1/reports",
		blockedReportPayload,
		map[string]string{
			"Idempotency-Key": "report-policy-block-001",
		},
	)
	if blockedReportStatus != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 from blocked report request, got %d body=%+v", blockedReportStatus, blockedReportBody)
	}

	allowedPayload := map[string]any{
		"conversation": map[string]any{
			"tenant_id":       "default",
			"conversation_id": "chat-safe",
			"channel":         "whatsapp_web",
		},
		"locale":         "pt-BR",
		"tone":           "formal",
		"context_window": 20,
	}
	allowedStatus, allowedBody := postJSON(
		t,
		client,
		baseURL+"/v1/suggestions",
		allowedPayload,
		nil,
	)
	if allowedStatus != http.StatusOK {
		t.Fatalf("expected 200 from safe suggestion request, got %d body=%+v", allowedStatus, allowedBody)
	}

	hitl, ok := allowedBody["hitl"].(map[string]any)
	if !ok {
		t.Fatalf("expected hitl metadata in response, got %+v", allowedBody)
	}
	if required, _ := hitl["required"].(bool); !required {
		t.Fatalf("expected hitl.required=true, got %+v", hitl["required"])
	}

	allowedActions, ok := hitl["allowed_actions"].([]any)
	if !ok || len(allowedActions) == 0 {
		t.Fatalf("expected allowed_actions list in hitl metadata, got %+v", hitl)
	}
	hasCopy := false
	for _, action := range allowedActions {
		if fmt.Sprintf("%v", action) == "copy" {
			hasCopy = true
			break
		}
	}
	if !hasCopy {
		t.Fatalf("expected allowed_actions to include copy, got %+v", allowedActions)
	}
}
