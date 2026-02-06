package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/iago/extensao-whatsapp-back/internal/domain"
	"github.com/iago/extensao-whatsapp-back/internal/policy"
)

func (api *API) Reports(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		api.createReport(w, r)
	case http.MethodGet:
		api.listReports(w, r)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (api *API) createReport(w http.ResponseWriter, r *http.Request) {
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(idempotencyKey) < 16 {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Idempotency-Key header is required")
		return
	}

	var request reportRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid JSON payload")
		return
	}
	if err := validateConversation(request.Conversation); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "conversation fields are required")
		return
	}
	if request.ReportType == "" {
		request.ReportType = "timeline"
	}
	switch request.ReportType {
	case "timeline", "temas", "atendimento":
	default:
		writeError(w, r, http.StatusBadRequest, "invalid_request", "report_type must be timeline, temas or atendimento")
		return
	}

	payloadHash := hashPayload(request)
	if entry, exists := api.idempotency.Get(idempotencyKey); exists {
		if entry.PayloadHash != payloadHash {
			writeError(w, r, http.StatusConflict, "idempotency_conflict", "Idempotency-Key already used with different payload")
			return
		}
		response := map[string]any{
			"job_id":      entry.JobID,
			"status":      "pending",
			"status_url":  "/v1/jobs/" + entry.JobID,
			"accepted_at": entry.CreatedAt.Format(time.RFC3339Nano),
			"hitl":        policy.DefaultHITLMetadata(),
		}
		w.Header().Set("Retry-After", "2")
		writeJSON(w, http.StatusAccepted, response)
		return
	}

	rawPayload, _ := json.Marshal(request)
	if err := policy.ValidateManualOnlyPayload(rawPayload); err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "policy_violation", "automatic send is not allowed")
		return
	}
	if err := policy.EnforceContentPolicy(rawPayload); err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "policy_violation", "request blocked by policy")
		return
	}
	rawPayload = policy.MaskPIIJSON(rawPayload)

	job, err := api.jobsService.EnqueueReport(
		r.Context(),
		request.Conversation.TenantID,
		request.Conversation.ConversationID,
		rawPayload,
	)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "failed to enqueue report job")
		return
	}

	api.idempotency.Put(idempotencyKey, payloadHash, job.ID)

	response := map[string]any{
		"job_id":      job.ID,
		"status":      "pending",
		"status_url":  "/v1/jobs/" + job.ID,
		"accepted_at": job.CreatedAt.Format(time.RFC3339Nano),
		"hitl":        policy.DefaultHITLMetadata(),
	}
	w.Header().Set("Retry-After", "2")
	writeJSON(w, http.StatusAccepted, response)
}

func (api *API) listReports(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	page, _ := strconv.Atoi(query.Get("page"))
	pageSize, _ := strconv.Atoi(query.Get("page_size"))
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	from, err := parseOptionalDateTime(query.Get("from"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid from date")
		return
	}
	to, err := parseOptionalDateTime(query.Get("to"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid to date")
		return
	}

	filter := domain.ReportListFilter{
		TenantID: strings.TrimSpace(query.Get("tenant_id")),
		Page:     page,
		PageSize: pageSize,
		From:     from,
		To:       to,
		Topic:    strings.TrimSpace(query.Get("topic")),
	}

	items, total, err := api.jobsService.ListReports(r.Context(), filter)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "failed to list reports")
		return
	}

	payloadItems := make([]map[string]any, 0, len(items))
	for _, item := range items {
		payloadItems = append(payloadItems, map[string]any{
			"report_id":       item.ReportID,
			"conversation_id": item.ConversationID,
			"status":          item.Status,
			"created_at":      item.CreatedAt.Format(time.RFC3339Nano),
			"title":           item.Title,
		})
	}

	response := map[string]any{
		"items":     payloadItems,
		"page":      page,
		"page_size": pageSize,
		"total":     total,
		"has_next":  page*pageSize < total,
	}
	writeJSON(w, http.StatusOK, response)
}
