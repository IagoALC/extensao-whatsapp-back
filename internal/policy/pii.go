package policy

import (
	"encoding/json"
	"regexp"
	"strings"
)

var (
	emailPattern = regexp.MustCompile(`(?i)\b[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}\b`)
	phonePattern = regexp.MustCompile(`(?:\+?\d[\d()\-\s.]{7,}\d)`)
	cpfPattern   = regexp.MustCompile(`\b\d{3}\.?\d{3}\.?\d{3}\-?\d{2}\b`)
	cnpjPattern  = regexp.MustCompile(`\b\d{2}\.?\d{3}\.?\d{3}/?\d{4}\-?\d{2}\b`)
	cardPattern  = regexp.MustCompile(`\b(?:\d[ -]*?){13,16}\b`)
)

func MaskPIIString(value string) string {
	masked := emailPattern.ReplaceAllStringFunc(value, func(_ string) string {
		return "[email_redacted]"
	})
	masked = phonePattern.ReplaceAllStringFunc(masked, func(_ string) string {
		return "[phone_redacted]"
	})
	masked = cpfPattern.ReplaceAllString(masked, "***.***.***-**")
	masked = cnpjPattern.ReplaceAllString(masked, "**.***.***/****-**")
	masked = cardPattern.ReplaceAllStringFunc(masked, maskCardNumber)
	return masked
}

func MaskPIIJSON(payload json.RawMessage) json.RawMessage {
	trimmed := strings.TrimSpace(string(payload))
	if trimmed == "" {
		return append(json.RawMessage(nil), payload...)
	}

	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return json.RawMessage(MaskPIIString(string(payload)))
	}

	sanitized := maskValue(decoded)
	encoded, err := json.Marshal(sanitized)
	if err != nil {
		return append(json.RawMessage(nil), payload...)
	}

	return encoded
}

func maskValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for key, child := range typed {
			cloned[key] = maskValue(child)
		}
		return cloned
	case []any:
		cloned := make([]any, 0, len(typed))
		for _, child := range typed {
			cloned = append(cloned, maskValue(child))
		}
		return cloned
	case string:
		return MaskPIIString(typed)
	default:
		return value
	}
}

func maskCardNumber(value string) string {
	digits := make([]rune, 0, len(value))
	for _, char := range value {
		if char >= '0' && char <= '9' {
			digits = append(digits, char)
		}
	}
	if len(digits) < 8 {
		return "[card_redacted]"
	}

	last4 := string(digits[len(digits)-4:])
	return "**** **** **** " + last4
}
