package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/iago/extensao-whatsapp-back/internal/policy"
)

func (api *API) Summaries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(idempotencyKey) < 16 {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Idempotency-Key header is required")
		return
	}

	var request summaryRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid JSON payload")
		return
	}
	if err := validateConversation(request.Conversation); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "conversation fields are required")
		return
	}
	if request.SummaryType == "" {
		request.SummaryType = "short"
	}
	if request.SummaryType != "short" && request.SummaryType != "full" {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "summary_type must be short or full")
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

	job, err := api.jobsService.EnqueueSummary(
		r.Context(),
		request.Conversation.TenantID,
		request.Conversation.ConversationID,
		rawPayload,
	)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "failed to enqueue summary job")
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
