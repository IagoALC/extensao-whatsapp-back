package handlers

import (
	"encoding/json"
	"errors"
	"hash/fnv"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/iago/extensao-whatsapp-back/internal/http/middleware"
	"github.com/iago/extensao-whatsapp-back/internal/service"
)

var errInvalidPayload = errors.New("invalid payload")

type API struct {
	jobsService        *service.JobsService
	suggestionsService *service.SuggestionsService
	idempotency        *idempotencyStore
}

func NewAPI(jobsService *service.JobsService, suggestionsService *service.SuggestionsService) *API {
	return &API{
		jobsService:        jobsService,
		suggestionsService: suggestionsService,
		idempotency:        newIdempotencyStore(),
	}
}

type conversationRef struct {
	TenantID       string `json:"tenant_id"`
	ConversationID string `json:"conversation_id"`
	Channel        string `json:"channel"`
}

type suggestionRequest struct {
	Conversation           conversationRef `json:"conversation"`
	Locale                 string          `json:"locale"`
	Tone                   string          `json:"tone"`
	ContextWindow          int             `json:"context_window"`
	Messages               []string        `json:"messages,omitempty"`
	MaxCandidates          int             `json:"max_candidates,omitempty"`
	IncludeLastUserMessage bool            `json:"include_last_user_message,omitempty"`
}

type summaryRequest struct {
	Conversation   conversationRef `json:"conversation"`
	SummaryType    string          `json:"summary_type"`
	IncludeActions bool            `json:"include_actions"`
	From           string          `json:"from,omitempty"`
	To             string          `json:"to,omitempty"`
}

type reportRequest struct {
	Conversation conversationRef `json:"conversation"`
	ReportType   string          `json:"report_type"`
	TopicFilter  string          `json:"topic_filter,omitempty"`
	From         string          `json:"from,omitempty"`
	To           string          `json:"to,omitempty"`
	Page         int             `json:"page,omitempty"`
	PageSize     int             `json:"page_size,omitempty"`
}

type errorPayload struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	RequestID string `json:"request_id"`
}

func writeJSON(w http.ResponseWriter, statusCode int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, r *http.Request, statusCode int, code, message string) {
	payload := errorPayload{RequestID: middleware.GetRequestID(r.Context())}
	payload.Error.Code = code
	payload.Error.Message = message
	writeJSON(w, statusCode, payload)
}

func decodeJSON(r *http.Request, value any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return errInvalidPayload
	}
	return nil
}

func validateConversation(conversation conversationRef) error {
	tenantID := strings.TrimSpace(conversation.TenantID)
	conversationID := strings.TrimSpace(conversation.ConversationID)

	if tenantID == "" || len(tenantID) > 64 {
		return errInvalidPayload
	}
	if conversationID == "" || len(conversationID) > 128 {
		return errInvalidPayload
	}
	if conversation.Channel == "" {
		conversation.Channel = "whatsapp_web"
	}
	if conversation.Channel != "whatsapp_web" {
		return errInvalidPayload
	}
	return nil
}

func parseOptionalDateTime(value string) (*time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, errInvalidPayload
	}
	return &parsed, nil
}

type idempotencyEntry struct {
	PayloadHash uint64
	JobID       string
	CreatedAt   time.Time
}

type idempotencyStore struct {
	mu      sync.Mutex
	entries map[string]idempotencyEntry
}

func newIdempotencyStore() *idempotencyStore {
	return &idempotencyStore{
		entries: make(map[string]idempotencyEntry),
	}
}

func (s *idempotencyStore) Get(key string) (idempotencyEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[key]
	return entry, ok
}

func (s *idempotencyStore) Put(key string, payloadHash uint64, jobID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key] = idempotencyEntry{
		PayloadHash: payloadHash,
		JobID:       jobID,
		CreatedAt:   time.Now().UTC(),
	}
}

func hashPayload(value any) uint64 {
	payload, _ := json.Marshal(value)
	hasher := fnv.New64a()
	_, _ = hasher.Write(payload)
	return hasher.Sum64()
}
