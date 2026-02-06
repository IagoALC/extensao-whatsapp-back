package policy

import (
	"encoding/json"
	"errors"
	"strings"
)

var ErrContentPolicyViolation = errors.New("content policy violation")

type Violation struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Evaluation struct {
	Allowed    bool        `json:"allowed"`
	Violations []Violation `json:"violations,omitempty"`
}

type PolicyViolationError struct {
	Violations []Violation
}

func (e *PolicyViolationError) Error() string {
	if len(e.Violations) == 0 {
		return ErrContentPolicyViolation.Error()
	}
	return "content policy violation: " + e.Violations[0].Message
}

func (e *PolicyViolationError) Unwrap() error {
	return ErrContentPolicyViolation
}

func EnforceContentPolicy(payload json.RawMessage) error {
	evaluation := EvaluateContentPolicy(payload)
	if evaluation.Allowed {
		return nil
	}
	return &PolicyViolationError{Violations: evaluation.Violations}
}

func EvaluateContentPolicy(payload json.RawMessage) Evaluation {
	trimmed := strings.TrimSpace(string(payload))
	if trimmed == "" {
		return Evaluation{Allowed: true}
	}

	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return Evaluation{Allowed: true}
	}

	values := collectStringValues(decoded, nil)
	if len(values) == 0 {
		return Evaluation{Allowed: true}
	}

	violations := make([]Violation, 0, 2)
	if hasOversizedField(values) {
		violations = append(violations, Violation{
			Code:    "payload_too_large",
			Message: "one or more text fields exceed policy size limits",
		})
	}

	content := strings.ToLower(strings.Join(values, "\n"))
	for _, token := range blockedKeywords {
		if strings.Contains(content, token) {
			violations = append(violations, Violation{
				Code:    "blocked_operation",
				Message: "request contains operation blocked by policy",
			})
			break
		}
	}

	if len(violations) == 0 {
		return Evaluation{Allowed: true}
	}

	return Evaluation{
		Allowed:    false,
		Violations: dedupeViolations(violations),
	}
}

var blockedKeywords = []string{
	"auto send",
	"automatic send",
	"envio automatico",
	"disparo em massa",
	"bulk messaging",
	"mass spam",
	"phishing",
	"ransomware",
	"malware",
	"golpe",
	"fraude",
}

func collectStringValues(value any, current []string) []string {
	switch typed := value.(type) {
	case map[string]any:
		for _, child := range typed {
			current = collectStringValues(child, current)
		}
	case []any:
		for _, child := range typed {
			current = collectStringValues(child, current)
		}
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed != "" {
			current = append(current, trimmed)
		}
	}
	return current
}

func hasOversizedField(values []string) bool {
	for _, value := range values {
		if len(value) > 4000 {
			return true
		}
	}
	return false
}

func dedupeViolations(values []Violation) []Violation {
	seen := make(map[string]struct{}, len(values))
	result := make([]Violation, 0, len(values))
	for _, value := range values {
		key := value.Code + "|" + value.Message
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}
