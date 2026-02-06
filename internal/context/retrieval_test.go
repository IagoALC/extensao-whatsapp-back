package contextbuilder

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestBasicRetrieverPrioritizesMessagesAndSkipsConversationMetadata(t *testing.T) {
	retriever := NewBasicRetriever()

	payload, err := json.Marshal(map[string]any{
		"conversation": map[string]any{
			"tenant_id":       "default",
			"conversation_id": "wa:title:cliente-importante",
			"channel":         "whatsapp_web",
		},
		"messages": []string{
			"Contato: Preciso do boleto atualizado ainda hoje.",
			"Voce: Claro, vou gerar e te envio em seguida.",
			"Contato: Pode confirmar o vencimento?",
		},
		"locale":         "pt-BR",
		"tone":           "neutro",
		"context_window": 20,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	chunks, err := retriever.Retrieve(context.Background(), RetrievalInput{
		Task:           "suggestion",
		TenantID:       "default",
		ConversationID: "wa:title:cliente-importante",
		Payload:        payload,
		ContextWindow:  20,
	})
	if err != nil {
		t.Fatalf("retrieve failed: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatalf("expected chunks from payload")
	}

	if !containsChunkText(chunks, "Preciso do boleto atualizado") {
		t.Fatalf("expected message text to appear in chunks, got %+v", chunks)
	}
	if containsChunkText(chunks, "wa:title:cliente-importante") {
		t.Fatalf("expected conversation metadata to be ignored in chunks, got %+v", chunks)
	}
}

func TestBasicRetrieverSupportsStructuredMessageObjects(t *testing.T) {
	retriever := NewBasicRetriever()

	payload, err := json.Marshal(map[string]any{
		"messages": []map[string]any{
			{
				"author": "contact",
				"text":   "Consegue me atualizar sobre o pedido?",
			},
			{
				"author": "self",
				"text":   "Consigo sim, vou te passar o status agora.",
			},
		},
		"locale": "pt-BR",
		"tone":   "neutro",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	chunks, err := retriever.Retrieve(context.Background(), RetrievalInput{
		Task:           "suggestion",
		TenantID:       "default",
		ConversationID: "wa:123",
		Payload:        payload,
		ContextWindow:  20,
	})
	if err != nil {
		t.Fatalf("retrieve failed: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatalf("expected chunks from structured messages")
	}
	if !containsChunkText(chunks, "Consegue me atualizar sobre o pedido?") {
		t.Fatalf("expected structured message text to appear in chunks, got %+v", chunks)
	}
}

func containsChunkText(chunks []Chunk, expected string) bool {
	expectedLower := strings.ToLower(expected)
	for _, chunk := range chunks {
		if strings.Contains(strings.ToLower(chunk.Text), expectedLower) {
			return true
		}
	}
	return false
}
