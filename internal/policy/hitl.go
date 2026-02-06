package policy

import (
	"encoding/json"
	"errors"
	"strings"
)

var ErrAutoSendNotAllowed = errors.New("automatic send is not allowed")

type HITLMetadata struct {
	Required          bool     `json:"required"`
	AllowedActions    []string `json:"allowed_actions"`
	ProhibitedActions []string `json:"prohibited_actions"`
	Reason            string   `json:"reason"`
}

func DefaultHITLMetadata() HITLMetadata {
	return HITLMetadata{
		Required:          true,
		AllowedActions:    []string{"copy", "insert", "manual_review"},
		ProhibitedActions: []string{"auto_send", "send_now", "send_without_confirmation"},
		Reason:            "manual confirmation is mandatory before sending any message",
	}
}

func EnsureManualAction(action string) error {
	normalized := strings.ToLower(strings.TrimSpace(action))
	switch normalized {
	case "", "copy", "insert", "manual_review", "suggest":
		return nil
	}

	if strings.Contains(normalized, "send") || strings.Contains(normalized, "auto") {
		return ErrAutoSendNotAllowed
	}
	return nil
}

func ValidateManualOnlyPayload(payload json.RawMessage) error {
	trimmed := strings.TrimSpace(string(payload))
	if trimmed == "" {
		return nil
	}

	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		// Only validate structured payloads.
		return nil
	}

	if hasAutoSendFlag(decoded) {
		return ErrAutoSendNotAllowed
	}
	return nil
}

func hasAutoSendFlag(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for rawKey, child := range typed {
			key := strings.ToLower(strings.TrimSpace(rawKey))
			switch key {
			case "auto_send", "autosend", "send_automatically", "send_immediately", "send_now":
				if asBool(child) {
					return true
				}
			case "delivery_mode", "execution_mode", "mode", "action":
				if isAutomaticMode(child) {
					return true
				}
			}
			if hasAutoSendFlag(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if hasAutoSendFlag(child) {
				return true
			}
		}
	}

	return false
}

func asBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes", "y", "on":
			return true
		}
	case float64:
		return typed != 0
	case int:
		return typed != 0
	}
	return false
}

func isAutomaticMode(value any) bool {
	switch typed := value.(type) {
	case string:
		normalized := strings.ToLower(strings.TrimSpace(typed))
		switch normalized {
		case "auto", "automatic", "autosend", "send_now", "send_immediately", "without_confirmation":
			return true
		}
	case map[string]any, []any:
		return hasAutoSendFlag(typed)
	}
	return false
}
