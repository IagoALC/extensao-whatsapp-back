package contextbuilder

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type RetrievalInput struct {
	Task           string
	TenantID       string
	ConversationID string
	Payload        json.RawMessage
	ContextWindow  int
}

type Chunk struct {
	ID    string
	Text  string
	Score float64
}

type Retriever interface {
	Retrieve(ctx context.Context, input RetrievalInput) ([]Chunk, error)
}

// BasicRetriever uses lexical signals from request payload until a vector store is available.
type BasicRetriever struct{}

func NewBasicRetriever() *BasicRetriever {
	return &BasicRetriever{}
}

func (r *BasicRetriever) Retrieve(_ context.Context, input RetrievalInput) ([]Chunk, error) {
	fragmentLimit := deriveFragmentLimit(input.Task, input.ContextWindow)
	fragments := make([]string, 0, fragmentLimit)

	if len(input.Payload) > 0 {
		var decoded any
		if err := json.Unmarshal(input.Payload, &decoded); err == nil {
			windowFromPayload := readContextWindowFromPayload(decoded)
			if input.ContextWindow <= 0 && windowFromPayload > 0 {
				fragmentLimit = deriveFragmentLimit(input.Task, windowFromPayload)
			}
			extractFragments(decoded, &fragments, fragmentLimit)
		}
	}

	if len(fragments) == 0 {
		fragments = append(fragments,
			"Nao ha historico detalhado no payload; use contexto recente da conversa quando disponivel.",
		)
	}

	uniqueFragments := dedupeFragments(fragments, fragmentLimit)
	chunks := make([]Chunk, 0, len(uniqueFragments))
	for index, fragment := range uniqueFragments {
		trimmed := strings.TrimSpace(fragment)
		if trimmed == "" {
			continue
		}
		score := computeScore(input.Task, index, trimmed)
		chunks = append(chunks, Chunk{
			ID:    fmt.Sprintf("chunk-%d", index+1),
			Text:  trimmed,
			Score: score,
		})
	}

	return chunks, nil
}

func extractFragments(value any, fragments *[]string, limit int) {
	if len(*fragments) >= limit {
		return
	}

	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if len(*fragments) >= limit {
				return
			}
			if isInterestingKey(key) {
				extractFragments(nested, fragments, limit)
				continue
			}
			extractFragments(nested, fragments, limit)
		}
	case []any:
		for _, nested := range typed {
			if len(*fragments) >= limit {
				return
			}
			extractFragments(nested, fragments, limit)
		}
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return
		}
		if len(trimmed) > 520 {
			trimmed = trimmed[:520]
		}
		*fragments = append(*fragments, trimmed)
	}
}

func isInterestingKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	switch normalized {
	case "message", "messages", "text", "topic_filter", "summary_type", "report_type", "tone", "locale", "context_window":
		return true
	default:
		return false
	}
}

func computeScore(task string, index int, fragment string) float64 {
	score := 100.0 - float64(index*3)
	normalized := strings.ToLower(fragment)

	if strings.Contains(normalized, "urgente") || strings.Contains(normalized, "prazo") {
		score += 8
	}
	if strings.Contains(normalized, "?") {
		score += 6
	}
	if task == "suggestion" {
		score += 4
	}
	if task == "report" && (strings.Contains(normalized, "tema") || strings.Contains(normalized, "timeline")) {
		score += 6
	}

	if score < 1 {
		score = 1
	}
	return score
}

func deriveFragmentLimit(task string, contextWindow int) int {
	baseLimit := 18
	switch strings.ToLower(strings.TrimSpace(task)) {
	case "suggestion":
		baseLimit = 22
	case "summary":
		baseLimit = 30
	case "report":
		baseLimit = 42
	}

	if contextWindow > 0 {
		scaled := contextWindow * 2
		if scaled < 12 {
			scaled = 12
		}
		if scaled > 80 {
			scaled = 80
		}
		if scaled < baseLimit {
			return scaled
		}
		return baseLimit
	}
	return baseLimit
}

func readContextWindowFromPayload(decoded any) int {
	payload, ok := decoded.(map[string]any)
	if !ok {
		return 0
	}

	value, ok := payload["context_window"]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil {
			return 0
		}
		return parsed
	default:
		return 0
	}
}

func dedupeFragments(fragments []string, limit int) []string {
	seen := make(map[string]struct{}, len(fragments))
	result := make([]string, 0, len(fragments))
	for _, fragment := range fragments {
		trimmed := strings.TrimSpace(fragment)
		if trimmed == "" {
			continue
		}
		key := fragmentFingerprint(trimmed)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, trimmed)
		if len(result) >= limit {
			break
		}
	}
	return result
}

var repeatedSpacePattern = regexp.MustCompile(`\s+`)

func fragmentFingerprint(value string) string {
	lowered := strings.ToLower(strings.TrimSpace(value))
	lowered = strings.ReplaceAll(lowered, "\n", " ")
	lowered = strings.ReplaceAll(lowered, "\t", " ")
	lowered = repeatedSpacePattern.ReplaceAllString(lowered, " ")
	return lowered
}
