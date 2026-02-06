package service

import (
	"context"
	"encoding/json"
	"strings"
)

type SuggestionsInput struct {
	TenantID       string
	ConversationID string
	Locale         string
	Tone           string
	ContextWindow  int
	Payload        json.RawMessage
}

type SuggestionCandidate struct {
	Rank      int    `json:"rank"`
	Content   string `json:"content"`
	Rationale string `json:"rationale,omitempty"`
}

type SuggestionsOutput struct {
	ModelID       string                `json:"model_id"`
	PromptVersion string                `json:"prompt_version"`
	Suggestions   []SuggestionCandidate `json:"suggestions"`
	QualityScore  float64               `json:"quality_score"`
}

type SuggestionsService struct {
	generator *AIGenerationService
}

func NewSuggestionsService(generator *AIGenerationService) *SuggestionsService {
	return &SuggestionsService{generator: generator}
}

func (s *SuggestionsService) Generate(
	ctx context.Context,
	input SuggestionsInput,
) (SuggestionsOutput, error) {
	if s.generator != nil {
		return s.generator.GenerateSuggestions(ctx, input)
	}

	locale := strings.ToLower(input.Locale)
	isPortuguese := strings.HasPrefix(locale, "pt") || locale == ""
	tone := strings.ToLower(strings.TrimSpace(input.Tone))
	if tone == "" {
		tone = "neutro"
	}

	if isPortuguese {
		return SuggestionsOutput{
			ModelID:       "fallback-local",
			PromptVersion: "reply_v1",
			Suggestions:   buildPTSuggestions(tone),
			QualityScore:  0.55,
		}, nil
	}

	return SuggestionsOutput{
		ModelID:       "fallback-local",
		PromptVersion: "reply_v1",
		Suggestions:   buildENSuggestions(tone),
		QualityScore:  0.55,
	}, nil
}

func buildPTSuggestions(tone string) []SuggestionCandidate {
	switch tone {
	case "formal":
		return []SuggestionCandidate{
			{Rank: 1, Content: "Perfeito, recebi sua solicitacao e vou retornar com uma atualizacao em instantes.", Rationale: "Tom profissional e objetivo."},
			{Rank: 2, Content: "Obrigado pelo contato. Estou validando os detalhes e te envio o status completo em seguida.", Rationale: "Formal com acolhimento."},
			{Rank: 3, Content: "Entendido. Vou priorizar esta demanda e te posiciono com os proximos passos ainda hoje.", Rationale: "Formal orientado a acao."},
		}
	case "amigavel":
		return []SuggestionCandidate{
			{Rank: 1, Content: "Valeu por avisar. Ja estou olhando isso e te retorno rapidinho.", Rationale: "Tom proximo e leve."},
			{Rank: 2, Content: "Boa! Recebi aqui e vou te mandar a resposta certinha em alguns minutos.", Rationale: "Amigavel sem perder clareza."},
			{Rank: 3, Content: "Fechado, pode deixar comigo. Ja te atualizo com os proximos passos.", Rationale: "Tom colaborativo."},
		}
	default:
		return []SuggestionCandidate{
			{Rank: 1, Content: "Recebi sua mensagem e estou verificando. Te atualizo em seguida.", Rationale: "Neutro e claro."},
			{Rank: 2, Content: "Obrigado pelo retorno. Vou confirmar os detalhes e te envio o status ainda hoje.", Rationale: "Neutro com compromisso de retorno."},
			{Rank: 3, Content: "Entendi o contexto. Estou cuidando disso e te aviso assim que concluir.", Rationale: "Neutro orientado a execucao."},
		}
	}
}

func buildENSuggestions(tone string) []SuggestionCandidate {
	switch tone {
	case "formal":
		return []SuggestionCandidate{
			{Rank: 1, Content: "Thank you for your message. I am reviewing this and will send you an update shortly.", Rationale: "Professional and concise."},
			{Rank: 2, Content: "Understood. I will validate the details and share the current status with you soon.", Rationale: "Formal with clear commitment."},
			{Rank: 3, Content: "Received. I am prioritizing this request and will follow up with next steps today.", Rationale: "Formal action-driven tone."},
		}
	case "amigavel":
		return []SuggestionCandidate{
			{Rank: 1, Content: "Thanks for the heads-up. I am on it and will get back to you soon.", Rationale: "Friendly and direct."},
			{Rank: 2, Content: "Got it. I will check this now and send you a quick update in a bit.", Rationale: "Friendly with urgency."},
			{Rank: 3, Content: "Perfect, leave it with me. I will share the next steps shortly.", Rationale: "Collaborative and warm."},
		}
	default:
		return []SuggestionCandidate{
			{Rank: 1, Content: "I received your message and I am checking it now. I will update you shortly.", Rationale: "Neutral and clear."},
			{Rank: 2, Content: "Thanks for the context. I will confirm the details and share status today.", Rationale: "Neutral with commitment."},
			{Rank: 3, Content: "Understood. I am handling this and will get back to you as soon as it is done.", Rationale: "Neutral operational tone."},
		}
	}
}
