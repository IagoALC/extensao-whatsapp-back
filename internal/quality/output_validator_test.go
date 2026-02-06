package quality

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/iago/extensao-whatsapp-back/internal/ai"
)

func TestValidateSuggestionsReturnsScore(t *testing.T) {
	validator := NewOutputValidator()

	result, err := validator.ValidateSuggestions(SuggestionValidationInput{
		Locale: "pt-BR",
		Tone:   "neutro",
		Suggestions: []SuggestionCandidate{
			{Rank: 1, Content: "Recebi sua mensagem e vou te atualizar em breve", Rationale: "objetiva"},
			{Rank: 2, Content: "Perfeito, estou verificando os detalhes agora.", Rationale: "clara"},
		},
	})
	if err != nil {
		t.Fatalf("expected suggestions to validate: %v", err)
	}
	if len(result.Suggestions) == 0 {
		t.Fatalf("expected at least one suggestion")
	}
	if result.Score <= 0 {
		t.Fatalf("expected positive quality score, got %.2f", result.Score)
	}
}

func TestValidateTaskPayloadSummaryMasksPIIAndAddsQualityScore(t *testing.T) {
	validator := NewOutputValidator()
	body := json.RawMessage(`{
		"summary":"Contato user@example.com pediu retorno sobre a entrega prevista para hoje.",
		"action_items":["Responder com novo prazo","Confirmar endereco"],
		"prompt_version":"summary_v1",
		"model_id":"test-model"
	}`)

	validated, score, err := validator.ValidateTaskPayload(ai.TaskSummary, body, "pt-BR", "neutro")
	if err != nil {
		t.Fatalf("expected summary payload to validate: %v", err)
	}
	if score <= 0 {
		t.Fatalf("expected positive score, got %.2f", score)
	}

	raw := string(validated)
	if strings.Contains(raw, "user@example.com") {
		t.Fatalf("expected pii to be masked")
	}
	if !strings.Contains(raw, "\"quality_score\"") {
		t.Fatalf("expected quality_score in validated payload")
	}
}
