package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/iago/extensao-whatsapp-back/internal/http/middleware"
	"github.com/iago/extensao-whatsapp-back/internal/policy"
	"github.com/iago/extensao-whatsapp-back/internal/service"
)

func (api *API) Suggestions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	var request suggestionRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid JSON payload")
		return
	}
	if err := validateConversation(request.Conversation); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "conversation fields are required")
		return
	}
	request.Locale = strings.TrimSpace(request.Locale)
	if request.Locale == "" || len(request.Locale) > 16 {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "locale is required and must have at most 16 chars")
		return
	}

	tone := strings.TrimSpace(strings.ToLower(request.Tone))
	switch tone {
	case "formal", "neutro", "amigavel":
	default:
		writeError(w, r, http.StatusBadRequest, "invalid_request", "tone must be formal, neutro or amigavel")
		return
	}

	if request.ContextWindow < 5 || request.ContextWindow > 80 {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "context_window must be between 5 and 80")
		return
	}

	rawPayload, _ := json.Marshal(request)
	if err := policy.ValidateManualOnlyPayload(rawPayload); err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "policy_violation", "automatic send is not allowed")
		return
	}
	if err := policy.EnforceContentPolicy(rawPayload); err != nil {
		statusCode := http.StatusUnprocessableEntity
		message := "request blocked by policy"
		var violation *policy.PolicyViolationError
		if errors.As(err, &violation) && len(violation.Violations) > 0 {
			message = violation.Violations[0].Message
		}
		writeError(w, r, statusCode, "policy_violation", message)
		return
	}
	rawPayload = policy.MaskPIIJSON(rawPayload)

	output, err := api.suggestionsService.Generate(r.Context(), service.SuggestionsInput{
		TenantID:       request.Conversation.TenantID,
		ConversationID: request.Conversation.ConversationID,
		Locale:         request.Locale,
		Tone:           tone,
		ContextWindow:  request.ContextWindow,
		Payload:        rawPayload,
	})
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "failed to generate suggestions")
		return
	}

	response := map[string]any{
		"request_id":     middleware.GetRequestID(r.Context()),
		"model_id":       output.ModelID,
		"prompt_version": output.PromptVersion,
		"suggestions":    output.Suggestions,
		"quality_score":  output.QualityScore,
		"hitl_required":  true,
		"hitl":           policy.DefaultHITLMetadata(),
	}
	writeJSON(w, http.StatusOK, response)
}
