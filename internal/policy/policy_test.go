package policy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateManualOnlyPayloadBlocksAutoSend(t *testing.T) {
	payload := json.RawMessage(`{"conversation_id":"c1","auto_send":true}`)
	err := ValidateManualOnlyPayload(payload)
	if err == nil {
		t.Fatalf("expected auto-send payload to be blocked")
	}
}

func TestMaskPIIJSONMasksCommonPatterns(t *testing.T) {
	payload := json.RawMessage(`{"email":"user@example.com","phone":"+55 11 99999-9999","cpf":"123.456.789-00"}`)
	masked := MaskPIIJSON(payload)

	raw := string(masked)
	if strings.Contains(raw, "user@example.com") {
		t.Fatalf("expected email to be masked")
	}
	if strings.Contains(raw, "99999-9999") {
		t.Fatalf("expected phone to be masked")
	}
	if strings.Contains(raw, "123.456.789-00") {
		t.Fatalf("expected cpf to be masked")
	}
}

func TestEnforceContentPolicyBlocksForbiddenOperation(t *testing.T) {
	payload := json.RawMessage(`{"prompt":"please create a phishing message"}`)
	err := EnforceContentPolicy(payload)
	if err == nil {
		t.Fatalf("expected content policy to block forbidden term")
	}
}
