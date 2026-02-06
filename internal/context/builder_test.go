package contextbuilder

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestBuilderDedupesAndRespectsTaskLimits(t *testing.T) {
	builder := NewBuilder(NewBasicRetriever())

	payload := map[string]any{
		"messages": []string{
			"Cliente pediu atualizacao do prazo hoje.",
			"Cliente pediu atualizacao do prazo hoje.",
			"Cliente pediu atualizacao do prazo hoje.",
			"Existe uma pendencia no pagamento da fatura.",
			"Existe uma pendencia no pagamento da fatura.",
			"Time comercial solicitou retorno formal.",
			"Preciso confirmar os proximos passos com o cliente.",
			"Preciso confirmar os proximos passos com o cliente.",
		},
		"context_window": 20,
	}
	encoded, _ := json.Marshal(payload)

	result, err := builder.Build(context.Background(), BuildInput{
		Task:           "suggestion",
		TenantID:       "tenant-a",
		ConversationID: "conversation-a",
		Payload:        encoded,
	})
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	if len(result.Chunks) == 0 {
		t.Fatalf("expected at least one chunk")
	}
	if len(result.Chunks) > 6 {
		t.Fatalf("expected suggestion chunk cap to be applied, got %d chunks", len(result.Chunks))
	}

	seen := make(map[string]struct{}, len(result.Chunks))
	for _, chunk := range result.Chunks {
		key := strings.ToLower(strings.Join(strings.Fields(chunk.Text), " "))
		if _, exists := seen[key]; exists {
			t.Fatalf("expected deduped chunks, duplicate found: %q", chunk.Text)
		}
		seen[key] = struct{}{}
	}
}

func TestBuilderReturnsStableOutputForRepeatedInputs(t *testing.T) {
	builder := NewBuilder(NewBasicRetriever())

	payload := []byte(`{"messages":["Mensagem 1","Mensagem 2","Mensagem 3"],"context_window":12}`)
	input := BuildInput{
		Task:           "summary",
		TenantID:       "tenant-a",
		ConversationID: "conversation-a",
		Payload:        payload,
		MaxInputTokens: 2400,
	}

	first, err := builder.Build(context.Background(), input)
	if err != nil {
		t.Fatalf("first build failed: %v", err)
	}
	second, err := builder.Build(context.Background(), input)
	if err != nil {
		t.Fatalf("second build failed: %v", err)
	}

	if first.ContextText != second.ContextText {
		t.Fatalf("expected stable context text across repeated builds")
	}
	if first.TokenCount != second.TokenCount {
		t.Fatalf("expected stable token count across repeated builds")
	}
}
