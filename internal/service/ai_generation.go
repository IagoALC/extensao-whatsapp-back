package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/template"

	"github.com/iago/extensao-whatsapp-back/internal/ai"
	"github.com/iago/extensao-whatsapp-back/internal/cache"
	contextbuilder "github.com/iago/extensao-whatsapp-back/internal/context"
	"github.com/iago/extensao-whatsapp-back/internal/policy"
	"github.com/iago/extensao-whatsapp-back/internal/quality"
)

type AIGenerationDependencies struct {
	Router     *ai.ModelRouter
	Client     ai.TextGenerator
	Builder    *contextbuilder.Builder
	Cache      *cache.SemanticCache
	Validator  *quality.OutputValidator
	PromptsDir string
	Logger     *log.Logger
}

type AIGenerationService struct {
	router     *ai.ModelRouter
	client     ai.TextGenerator
	builder    *contextbuilder.Builder
	cache      *cache.SemanticCache
	validator  *quality.OutputValidator
	promptsDir string
	logger     *log.Logger

	tmplMu    sync.RWMutex
	templates map[string]*template.Template
}

type JobGenerationInput struct {
	TenantID       string
	ConversationID string
	Locale         string
	Tone           string
	Payload        json.RawMessage
}

type JobGenerationOutput struct {
	Body          json.RawMessage
	ModelID       string
	PromptVersion string
	CacheHit      bool
	UsedFallback  bool
}

func NewAIGenerationService(deps AIGenerationDependencies) *AIGenerationService {
	promptsDir := strings.TrimSpace(deps.PromptsDir)
	if promptsDir == "" {
		promptsDir = "prompts"
	}
	if deps.Cache == nil {
		deps.Cache = cache.NewSemanticCache(cache.Config{})
	}
	if deps.Builder == nil {
		deps.Builder = contextbuilder.NewBuilder(contextbuilder.NewBasicRetriever())
	}
	if deps.Validator == nil {
		deps.Validator = quality.NewOutputValidator()
	}

	return &AIGenerationService{
		router:     deps.Router,
		client:     deps.Client,
		builder:    deps.Builder,
		cache:      deps.Cache,
		validator:  deps.Validator,
		promptsDir: promptsDir,
		logger:     deps.Logger,
		templates:  make(map[string]*template.Template),
	}
}

func (s *AIGenerationService) GenerateSuggestions(ctx context.Context, input SuggestionsInput) (SuggestionsOutput, error) {
	locale := normalizeLocale(input.Locale)
	tone := normalizeTone(input.Tone)
	profile := s.router.Select(ai.TaskSuggestion)
	promptVersion := "reply_v1"
	promptFile := "reply_v1.tmpl"

	contextOut, err := s.builder.Build(ctx, contextbuilder.BuildInput{
		Task:           string(ai.TaskSuggestion),
		TenantID:       input.TenantID,
		ConversationID: input.ConversationID,
		Payload:        input.Payload,
		MaxInputTokens: suggestionTokenBudget(input.ContextWindow),
		MaxChunks:      suggestionChunkLimit(input.ContextWindow),
		ContextWindow:  input.ContextWindow,
	})
	if err != nil {
		s.logf("context build failed for suggestions: %v", err)
		return s.fallbackSuggestions(locale, tone, promptVersion), nil
	}

	signature := s.cache.BuildSignature(
		string(ai.TaskSuggestion),
		input.TenantID,
		input.ConversationID,
		locale,
		tone,
		promptVersion,
		contextOut.ContextText,
	)
	if cached, ok := s.cache.Get(signature); ok {
		parsed, cachedScore, parseErr := parseSuggestionsPayload(cached.Value)
		if parseErr == nil {
			return SuggestionsOutput{
				ModelID:       firstNonEmpty(cached.ModelID, "cache-hit"),
				PromptVersion: firstNonEmpty(cached.PromptVersion, promptVersion),
				Suggestions:   parsed,
				QualityScore:  cachedScore,
			}, nil
		}
	}

	renderedPrompt, err := s.renderPrompt(promptFile, map[string]any{
		"Locale":  locale,
		"Tone":    tone,
		"Context": contextOut.ContextText,
	})
	if err != nil {
		s.logf("render prompt failed for suggestions: %v", err)
		return s.fallbackSuggestions(locale, tone, promptVersion), nil
	}

	text, modelID, callErr := s.generateText(ctx, profile, renderedPrompt)
	if callErr != nil {
		s.logf("openai generate suggestion failed, using fallback: %v", callErr)
		return s.fallbackSuggestions(locale, tone, promptVersion), nil
	}

	suggestions, parseErr := parseSuggestionsFromModel(text, locale, tone)
	if parseErr != nil {
		s.logf("parse suggestions failed, using fallback: %v", parseErr)
		return s.fallbackSuggestions(locale, tone, promptVersion), nil
	}

	validatedSuggestions, qualityScore, validationErr := s.validateSuggestions(locale, tone, suggestions)
	if validationErr != nil {
		s.logf("validate suggestions failed, using fallback: %v", validationErr)
		return s.fallbackSuggestions(locale, tone, promptVersion), nil
	}

	cacheBody, _ := json.Marshal(map[string]any{
		"suggestions":   validatedSuggestions,
		"quality_score": qualityScore,
	})
	s.cache.Set(signature, cache.Entry{
		Value:         cacheBody,
		ModelID:       modelID,
		PromptVersion: promptVersion,
	})

	return SuggestionsOutput{
		ModelID:       modelID,
		PromptVersion: promptVersion,
		Suggestions:   validatedSuggestions,
		QualityScore:  qualityScore,
	}, nil
}

func (s *AIGenerationService) GenerateSummary(ctx context.Context, input JobGenerationInput) (JobGenerationOutput, error) {
	return s.generateStructuredJob(ctx, ai.TaskSummary, input, "summary_v1", "summary_v1.tmpl", 3200)
}

func (s *AIGenerationService) GenerateReport(ctx context.Context, input JobGenerationInput) (JobGenerationOutput, error) {
	return s.generateStructuredJob(ctx, ai.TaskReport, input, "report_v1", "report_v1.tmpl", 5200)
}

func (s *AIGenerationService) generateStructuredJob(
	ctx context.Context,
	task ai.TaskKind,
	input JobGenerationInput,
	promptVersion string,
	promptFile string,
	maxInputTokens int,
) (JobGenerationOutput, error) {
	locale := normalizeLocale(input.Locale)
	tone := normalizeTone(input.Tone)
	profile := s.router.Select(task)

	contextOut, err := s.builder.Build(ctx, contextbuilder.BuildInput{
		Task:           string(task),
		TenantID:       input.TenantID,
		ConversationID: input.ConversationID,
		Payload:        input.Payload,
		MaxInputTokens: maxInputTokens,
		MaxChunks:      maxChunkLimitByTask(task),
		ContextWindow:  20,
	})
	if err != nil {
		s.logf("context build failed for task=%s: %v", task, err)
		return s.fallbackJob(task, promptVersion), nil
	}

	signature := s.cache.BuildSignature(
		string(task),
		input.TenantID,
		input.ConversationID,
		locale,
		tone,
		promptVersion,
		contextOut.ContextText,
	)
	if cached, ok := s.cache.Get(signature); ok {
		body := append([]byte(nil), cached.Value...)
		if len(body) > 0 {
			return JobGenerationOutput{
				Body:          body,
				ModelID:       firstNonEmpty(cached.ModelID, "cache-hit"),
				PromptVersion: firstNonEmpty(cached.PromptVersion, promptVersion),
				CacheHit:      true,
			}, nil
		}
	}

	renderedPrompt, err := s.renderPrompt(promptFile, map[string]any{
		"Locale":  locale,
		"Tone":    tone,
		"Context": contextOut.ContextText,
	})
	if err != nil {
		s.logf("render prompt failed for task=%s: %v", task, err)
		return s.fallbackJob(task, promptVersion), nil
	}

	text, modelID, callErr := s.generateText(ctx, profile, renderedPrompt)
	if callErr != nil {
		s.logf("openai generate failed for task=%s, fallback enabled: %v", task, callErr)
		return s.fallbackJob(task, promptVersion), nil
	}

	body, parseErr := parseJobPayload(task, text, promptVersion, modelID)
	if parseErr != nil {
		s.logf("parse model payload failed for task=%s, fallback enabled: %v", task, parseErr)
		return s.fallbackJob(task, promptVersion), nil
	}

	validatedBody, _, validationErr := s.validator.ValidateTaskPayload(task, body, locale, tone)
	if validationErr != nil {
		s.logf("validate payload failed for task=%s, fallback enabled: %v", task, validationErr)
		return s.fallbackJob(task, promptVersion), nil
	}
	body = validatedBody

	s.cache.Set(signature, cache.Entry{
		Value:         body,
		ModelID:       modelID,
		PromptVersion: promptVersion,
	})

	return JobGenerationOutput{
		Body:          body,
		ModelID:       modelID,
		PromptVersion: promptVersion,
	}, nil
}

func (s *AIGenerationService) fallbackSuggestions(locale, tone, promptVersion string) SuggestionsOutput {
	candidates := buildENSuggestions(tone)
	isPortuguese := strings.HasPrefix(strings.ToLower(locale), "pt")
	if isPortuguese {
		candidates = buildPTSuggestions(tone)
	}

	validated, score, err := s.validateSuggestions(locale, tone, candidates)
	if err != nil {
		s.logf("fallback suggestions validation failed: %v", err)
		score = 0.55
		for index := range candidates {
			candidates[index].Content = policy.MaskPIIString(candidates[index].Content)
			candidates[index].Rationale = policy.MaskPIIString(candidates[index].Rationale)
			candidates[index].Rank = index + 1
		}
		return SuggestionsOutput{
			ModelID:       "fallback-local",
			PromptVersion: promptVersion,
			Suggestions:   candidates,
			QualityScore:  score,
		}
	}

	return SuggestionsOutput{
		ModelID:       "fallback-local",
		PromptVersion: promptVersion,
		Suggestions:   validated,
		QualityScore:  score,
	}
}

func (s *AIGenerationService) fallbackJob(task ai.TaskKind, promptVersion string) JobGenerationOutput {
	const fallbackModelID = "fallback-local"

	var (
		payload json.RawMessage
		err     error
	)
	switch task {
	case ai.TaskSummary:
		payload, err = json.Marshal(map[string]any{
			"summary":        "Resumo gerado em modo degradado por indisponibilidade temporaria do modelo.",
			"action_items":   []string{"Revisar pendencias principais", "Responder contato com proximo passo"},
			"prompt_version": promptVersion,
			"model_id":       fallbackModelID,
			"quality_score":  0.55,
		})
	case ai.TaskReport:
		payload, err = json.Marshal(map[string]any{
			"title": "Relatorio (modo degradado)",
			"sections": []map[string]string{
				{"heading": "Visao geral", "content": "Relatorio gerado em modo degradado devido a indisponibilidade temporaria do modelo."},
				{"heading": "Pendencias", "content": "Validar manualmente os pontos criticos da conversa."},
				{"heading": "Proximos passos", "content": "Tentar nova geracao quando o servico de IA estiver disponivel."},
			},
			"prompt_version": promptVersion,
			"model_id":       fallbackModelID,
			"quality_score":  0.55,
		})
	default:
		payload, err = json.Marshal(map[string]any{
			"model_id":       fallbackModelID,
			"prompt_version": promptVersion,
			"quality_score":  0.55,
		})
	}

	if err != nil {
		payload = json.RawMessage(`{"model_id":"fallback-local","quality_score":0.55}`)
	}

	return JobGenerationOutput{
		Body:          payload,
		ModelID:       fallbackModelID,
		PromptVersion: promptVersion,
		UsedFallback:  true,
	}
}

func (s *AIGenerationService) validateSuggestions(
	locale string,
	tone string,
	suggestions []SuggestionCandidate,
) ([]SuggestionCandidate, float64, error) {
	if len(suggestions) == 0 {
		return nil, 0, errors.New("empty suggestions for validation")
	}

	if s.validator == nil {
		for index := range suggestions {
			suggestions[index].Rank = index + 1
			suggestions[index].Content = policy.MaskPIIString(strings.TrimSpace(suggestions[index].Content))
			suggestions[index].Rationale = policy.MaskPIIString(strings.TrimSpace(suggestions[index].Rationale))
		}
		return suggestions, 0.5, nil
	}

	input := quality.SuggestionValidationInput{
		Locale:      locale,
		Tone:        tone,
		Suggestions: make([]quality.SuggestionCandidate, 0, len(suggestions)),
	}
	for _, candidate := range suggestions {
		input.Suggestions = append(input.Suggestions, quality.SuggestionCandidate{
			Rank:      candidate.Rank,
			Content:   candidate.Content,
			Rationale: candidate.Rationale,
		})
	}

	validation, err := s.validator.ValidateSuggestions(input)
	if err != nil {
		return nil, 0, err
	}

	result := make([]SuggestionCandidate, 0, 3)
	seen := make(map[string]struct{}, len(validation.Suggestions))
	for _, candidate := range validation.Suggestions {
		content := strings.TrimSpace(policy.MaskPIIString(candidate.Content))
		if content == "" {
			continue
		}
		key := strings.ToLower(content)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, SuggestionCandidate{
			Rank:      len(result) + 1,
			Content:   content,
			Rationale: strings.TrimSpace(policy.MaskPIIString(candidate.Rationale)),
		})
		if len(result) >= 3 {
			break
		}
	}

	if len(result) < 3 {
		pool := buildENSuggestions(tone)
		if strings.HasPrefix(strings.ToLower(locale), "pt") {
			pool = buildPTSuggestions(tone)
		}
		for _, fallback := range pool {
			if len(result) >= 3 {
				break
			}
			content := strings.TrimSpace(policy.MaskPIIString(fallback.Content))
			key := strings.ToLower(content)
			if content == "" {
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, SuggestionCandidate{
				Rank:      len(result) + 1,
				Content:   content,
				Rationale: strings.TrimSpace(policy.MaskPIIString(fallback.Rationale)),
			})
		}
	}

	if len(result) == 0 {
		return nil, 0, errors.New("no suggestions available after validation")
	}

	score := validation.Score
	if len(validation.Suggestions) < len(result) {
		score -= 0.05
	}
	if score < 0 {
		score = 0
	}
	return result, score, nil
}

func (s *AIGenerationService) generateText(
	ctx context.Context,
	profile ai.ModelProfile,
	prompt string,
) (string, string, error) {
	if s.client == nil || !s.client.Available() {
		return "", "", ai.ErrOpenAIUnavailable
	}

	primaryResult, err := s.client.Generate(ctx, ai.GenerateRequest{
		Model:           profile.PrimaryModel,
		Instructions:    "Return only valid JSON. Do not use markdown code fences.",
		Input:           prompt,
		Temperature:     profile.Temperature,
		MaxOutputTokens: profile.MaxOutputTokens,
	})
	if err == nil {
		return primaryResult.Text, firstNonEmpty(primaryResult.ModelID, profile.PrimaryModel), nil
	}

	if strings.TrimSpace(profile.FallbackModel) == "" || profile.FallbackModel == profile.PrimaryModel {
		return "", "", err
	}

	fallbackResult, fallbackErr := s.client.Generate(ctx, ai.GenerateRequest{
		Model:           profile.FallbackModel,
		Instructions:    "Return only valid JSON. Do not use markdown code fences.",
		Input:           prompt,
		Temperature:     profile.Temperature,
		MaxOutputTokens: profile.MaxOutputTokens,
	})
	if fallbackErr != nil {
		return "", "", fmt.Errorf("primary model failed: %v; fallback failed: %w", err, fallbackErr)
	}
	return fallbackResult.Text, firstNonEmpty(fallbackResult.ModelID, profile.FallbackModel), nil
}

func (s *AIGenerationService) renderPrompt(fileName string, data any) (string, error) {
	tmpl, err := s.loadTemplate(fileName)
	if err != nil {
		return "", err
	}

	buffer := bytes.NewBuffer(nil)
	if err := tmpl.Execute(buffer, data); err != nil {
		return "", fmt.Errorf("execute template %s: %w", fileName, err)
	}
	return buffer.String(), nil
}

func (s *AIGenerationService) loadTemplate(fileName string) (*template.Template, error) {
	s.tmplMu.RLock()
	if tmpl, ok := s.templates[fileName]; ok {
		s.tmplMu.RUnlock()
		return tmpl, nil
	}
	s.tmplMu.RUnlock()

	absolute := filepath.Join(s.promptsDir, fileName)
	content, err := os.ReadFile(absolute)
	if err != nil {
		return nil, fmt.Errorf("read prompt template %s: %w", absolute, err)
	}

	tmpl, err := template.New(fileName).Parse(string(content))
	if err != nil {
		return nil, fmt.Errorf("parse prompt template %s: %w", fileName, err)
	}

	s.tmplMu.Lock()
	s.templates[fileName] = tmpl
	s.tmplMu.Unlock()

	return tmpl, nil
}

func parseSuggestionsFromModel(text, locale, tone string) ([]SuggestionCandidate, error) {
	rawJSON, err := extractJSON(text)
	if err != nil {
		return nil, err
	}

	type suggestionItem struct {
		Content   string `json:"content"`
		Rationale string `json:"rationale"`
	}
	type envelope struct {
		Suggestions []suggestionItem `json:"suggestions"`
	}

	var parsed envelope
	if err := json.Unmarshal(rawJSON, &parsed); err != nil {
		return nil, fmt.Errorf("decode suggestions json: %w", err)
	}
	if len(parsed.Suggestions) == 0 {
		return nil, errors.New("empty suggestions")
	}

	result := make([]SuggestionCandidate, 0, 3)
	for _, item := range parsed.Suggestions {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		result = append(result, SuggestionCandidate{
			Rank:      len(result) + 1,
			Content:   content,
			Rationale: strings.TrimSpace(item.Rationale),
		})
		if len(result) >= 3 {
			break
		}
	}

	if len(result) < 3 {
		fallback := buildENSuggestions(tone)
		if strings.HasPrefix(strings.ToLower(locale), "pt") {
			fallback = buildPTSuggestions(tone)
		}
		for _, item := range fallback {
			if len(result) >= 3 {
				break
			}
			result = append(result, SuggestionCandidate{
				Rank:      len(result) + 1,
				Content:   item.Content,
				Rationale: item.Rationale,
			})
		}
	}

	for index := range result {
		result[index].Rank = index + 1
	}

	return result, nil
}

func parseSuggestionsPayload(value []byte) ([]SuggestionCandidate, float64, error) {
	type cachedEnvelope struct {
		Suggestions  []SuggestionCandidate `json:"suggestions"`
		QualityScore float64               `json:"quality_score"`
	}
	var payload cachedEnvelope
	if err := json.Unmarshal(value, &payload); err != nil {
		return nil, 0, err
	}
	if len(payload.Suggestions) == 0 {
		return nil, 0, errors.New("empty suggestions payload")
	}
	if payload.QualityScore < 0 || payload.QualityScore > 1 {
		payload.QualityScore = 0.5
	}
	return payload.Suggestions, payload.QualityScore, nil
}

func parseJobPayload(task ai.TaskKind, text string, promptVersion string, modelID string) (json.RawMessage, error) {
	rawJSON, err := extractJSON(text)
	if err != nil {
		return nil, err
	}

	switch task {
	case ai.TaskSummary:
		var payload struct {
			Summary     string   `json:"summary"`
			ActionItems []string `json:"action_items"`
		}
		if err := json.Unmarshal(rawJSON, &payload); err != nil {
			return nil, fmt.Errorf("decode summary json: %w", err)
		}
		if strings.TrimSpace(payload.Summary) == "" {
			return nil, errors.New("summary is empty")
		}
		encoded, err := json.Marshal(map[string]any{
			"summary":        strings.TrimSpace(payload.Summary),
			"action_items":   payload.ActionItems,
			"prompt_version": promptVersion,
			"model_id":       modelID,
		})
		if err != nil {
			return nil, err
		}
		return encoded, nil
	case ai.TaskReport:
		var payload struct {
			Title    string `json:"title"`
			Sections []struct {
				Heading string `json:"heading"`
				Content string `json:"content"`
			} `json:"sections"`
		}
		if err := json.Unmarshal(rawJSON, &payload); err != nil {
			return nil, fmt.Errorf("decode report json: %w", err)
		}
		if strings.TrimSpace(payload.Title) == "" {
			payload.Title = "Relatorio da conversa"
		}
		if len(payload.Sections) == 0 {
			return nil, errors.New("report sections are empty")
		}
		sections := make([]map[string]string, 0, len(payload.Sections))
		for _, section := range payload.Sections {
			heading := strings.TrimSpace(section.Heading)
			content := strings.TrimSpace(section.Content)
			if heading == "" || content == "" {
				continue
			}
			sections = append(sections, map[string]string{"heading": heading, "content": content})
		}
		if len(sections) == 0 {
			return nil, errors.New("report sections are invalid")
		}
		encoded, err := json.Marshal(map[string]any{
			"title":          strings.TrimSpace(payload.Title),
			"sections":       sections,
			"prompt_version": promptVersion,
			"model_id":       modelID,
		})
		if err != nil {
			return nil, err
		}
		return encoded, nil
	default:
		return nil, fmt.Errorf("unsupported task for parse payload: %s", task)
	}
}

func extractJSON(text string) ([]byte, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, errors.New("empty model output")
	}

	if strings.HasPrefix(trimmed, "```") {
		trimmed = stripCodeFence(trimmed)
	}

	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err == nil {
		return []byte(trimmed), nil
	}

	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start >= 0 && end > start {
		candidate := trimmed[start : end+1]
		if err := json.Unmarshal([]byte(candidate), &decoded); err == nil {
			return []byte(candidate), nil
		}
	}

	return nil, errors.New("model output is not valid JSON")
}

func stripCodeFence(text string) string {
	trimmed := strings.TrimSpace(text)
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimPrefix(trimmed, "json")
	trimmed = strings.TrimSuffix(trimmed, "```")
	return strings.TrimSpace(trimmed)
}

func normalizeLocale(locale string) string {
	trimmed := strings.TrimSpace(locale)
	if trimmed == "" {
		return "pt-BR"
	}
	if len(trimmed) > 16 {
		return trimmed[:16]
	}
	return trimmed
}

func normalizeTone(tone string) string {
	normalized := strings.ToLower(strings.TrimSpace(tone))
	switch normalized {
	case "formal", "neutro", "amigavel":
		return normalized
	default:
		return "neutro"
	}
}

func suggestionTokenBudget(contextWindow int) int {
	window := contextWindow
	if window <= 0 {
		window = 20
	}
	if window < 5 {
		window = 5
	}
	if window > 80 {
		window = 80
	}

	budget := 900 + (window * 32)
	if budget < 1000 {
		budget = 1000
	}
	if budget > 2200 {
		budget = 2200
	}
	return budget
}

func suggestionChunkLimit(contextWindow int) int {
	window := contextWindow
	if window <= 0 {
		window = 20
	}
	chunks := 3 + (window / 12)
	if chunks < 4 {
		chunks = 4
	}
	if chunks > 8 {
		chunks = 8
	}
	return chunks
}

func maxChunkLimitByTask(task ai.TaskKind) int {
	switch task {
	case ai.TaskSummary:
		return 10
	case ai.TaskReport:
		return 12
	default:
		return 8
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (s *AIGenerationService) logf(format string, args ...any) {
	if s.logger == nil {
		return
	}
	s.logger.Printf(format, args...)
}
