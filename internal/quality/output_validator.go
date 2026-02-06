package quality

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/iago/extensao-whatsapp-back/internal/ai"
	"github.com/iago/extensao-whatsapp-back/internal/policy"
)

var ErrQualityRejected = errors.New("output failed quality checks")

const (
	minSuggestionScore = 0.45
	minStructuredScore = 0.50
)

type SuggestionCandidate struct {
	Rank      int
	Content   string
	Rationale string
}

type SuggestionValidationInput struct {
	Locale      string
	Tone        string
	Suggestions []SuggestionCandidate
}

type SuggestionValidationResult struct {
	Suggestions []SuggestionCandidate
	Score       float64
	Corrected   bool
}

type OutputValidator struct{}

func NewOutputValidator() *OutputValidator {
	return &OutputValidator{}
}

func (v *OutputValidator) ValidateSuggestions(
	input SuggestionValidationInput,
) (SuggestionValidationResult, error) {
	if len(input.Suggestions) == 0 {
		return SuggestionValidationResult{}, fmt.Errorf("%w: empty suggestions", ErrQualityRejected)
	}

	locale := strings.ToLower(strings.TrimSpace(input.Locale))
	tone := strings.ToLower(strings.TrimSpace(input.Tone))
	if tone == "" {
		tone = "neutro"
	}

	corrected := false
	penalty := 0.0
	seen := make(map[string]struct{}, len(input.Suggestions))
	output := make([]SuggestionCandidate, 0, 3)

	for _, item := range input.Suggestions {
		content := normalizeText(item.Content)
		if content == "" {
			corrected = true
			penalty += 0.20
			continue
		}

		masked := policy.MaskPIIString(content)
		if masked != content {
			content = masked
			corrected = true
			penalty += 0.05
		}

		if len(content) > 320 {
			content = truncateAtWord(content, 320)
			corrected = true
			penalty += 0.08
		}
		if !hasTerminalPunctuation(content) {
			content += "."
			corrected = true
		}

		key := strings.ToLower(content)
		if _, exists := seen[key]; exists {
			corrected = true
			penalty += 0.06
			continue
		}
		seen[key] = struct{}{}

		if toneMismatch(content, tone) {
			penalty += 0.07
		}
		if localeMismatch(content, locale) {
			penalty += 0.07
		}

		rationale := normalizeText(item.Rationale)
		if len(rationale) > 180 {
			rationale = truncateAtWord(rationale, 180)
			corrected = true
		}

		output = append(output, SuggestionCandidate{
			Rank:      len(output) + 1,
			Content:   content,
			Rationale: rationale,
		})
		if len(output) == 3 {
			break
		}
	}

	if len(output) == 0 {
		return SuggestionValidationResult{}, fmt.Errorf("%w: no valid suggestion candidates", ErrQualityRejected)
	}

	score := clamp01(1.0 - penalty)
	if score < minSuggestionScore {
		return SuggestionValidationResult{}, fmt.Errorf("%w: low suggestion quality score %.2f", ErrQualityRejected, score)
	}

	return SuggestionValidationResult{
		Suggestions: output,
		Score:       round2(score),
		Corrected:   corrected,
	}, nil
}

func (v *OutputValidator) ValidateTaskPayload(
	task ai.TaskKind,
	body json.RawMessage,
	locale string,
	tone string,
) (json.RawMessage, float64, error) {
	switch task {
	case ai.TaskSummary:
		return v.validateSummary(body, locale, tone)
	case ai.TaskReport:
		return v.validateReport(body, locale, tone)
	default:
		return nil, 0, fmt.Errorf("%w: unsupported task %s", ErrQualityRejected, task)
	}
}

func (v *OutputValidator) validateSummary(
	body json.RawMessage,
	locale string,
	_ string,
) (json.RawMessage, float64, error) {
	var payload struct {
		Summary       string   `json:"summary"`
		ActionItems   []string `json:"action_items"`
		PromptVersion string   `json:"prompt_version"`
		ModelID       string   `json:"model_id"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, 0, fmt.Errorf("%w: decode summary payload: %v", ErrQualityRejected, err)
	}

	penalty := 0.0
	summary := normalizeText(policy.MaskPIIString(payload.Summary))
	if summary == "" {
		return nil, 0, fmt.Errorf("%w: summary text is empty", ErrQualityRejected)
	}
	if len(summary) > 2400 {
		summary = truncateAtWord(summary, 2400)
		penalty += 0.06
	}
	if len(summary) < 40 {
		penalty += 0.18
	}
	if localeMismatch(summary, strings.ToLower(strings.TrimSpace(locale))) {
		penalty += 0.07
	}

	actionItems := make([]string, 0, len(payload.ActionItems))
	seen := make(map[string]struct{}, len(payload.ActionItems))
	for _, item := range payload.ActionItems {
		normalized := normalizeText(policy.MaskPIIString(item))
		if normalized == "" {
			continue
		}
		if len(normalized) > 220 {
			normalized = truncateAtWord(normalized, 220)
			penalty += 0.03
		}
		key := strings.ToLower(normalized)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		actionItems = append(actionItems, normalized)
		if len(actionItems) >= 10 {
			break
		}
	}

	if len(actionItems) == 0 {
		penalty += 0.10
	}

	score := clamp01(1.0 - penalty)
	if score < minStructuredScore {
		return nil, 0, fmt.Errorf("%w: low summary quality score %.2f", ErrQualityRejected, score)
	}

	encoded, err := json.Marshal(map[string]any{
		"summary":        summary,
		"action_items":   actionItems,
		"prompt_version": payload.PromptVersion,
		"model_id":       payload.ModelID,
		"quality_score":  round2(score),
	})
	if err != nil {
		return nil, 0, fmt.Errorf("encode summary payload: %w", err)
	}
	return encoded, round2(score), nil
}

func (v *OutputValidator) validateReport(
	body json.RawMessage,
	locale string,
	_ string,
) (json.RawMessage, float64, error) {
	var payload struct {
		Title    string `json:"title"`
		Sections []struct {
			Heading string `json:"heading"`
			Content string `json:"content"`
		} `json:"sections"`
		PromptVersion string `json:"prompt_version"`
		ModelID       string `json:"model_id"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, 0, fmt.Errorf("%w: decode report payload: %v", ErrQualityRejected, err)
	}

	penalty := 0.0
	title := normalizeText(policy.MaskPIIString(payload.Title))
	if title == "" {
		title = "Relatorio da conversa"
		penalty += 0.05
	}
	if len(title) > 120 {
		title = truncateAtWord(title, 120)
		penalty += 0.02
	}

	sections := make([]map[string]string, 0, len(payload.Sections))
	for _, section := range payload.Sections {
		heading := normalizeText(policy.MaskPIIString(section.Heading))
		content := normalizeText(policy.MaskPIIString(section.Content))
		if heading == "" || content == "" {
			continue
		}
		if len(heading) > 90 {
			heading = truncateAtWord(heading, 90)
			penalty += 0.02
		}
		if len(content) > 1800 {
			content = truncateAtWord(content, 1800)
			penalty += 0.05
		}
		if localeMismatch(content, strings.ToLower(strings.TrimSpace(locale))) {
			penalty += 0.05
		}
		sections = append(sections, map[string]string{
			"heading": heading,
			"content": content,
		})
		if len(sections) >= 8 {
			break
		}
	}

	if len(sections) == 0 {
		return nil, 0, fmt.Errorf("%w: report sections are empty", ErrQualityRejected)
	}
	if len(sections) < 2 {
		penalty += 0.12
	}

	score := clamp01(1.0 - penalty)
	if score < minStructuredScore {
		return nil, 0, fmt.Errorf("%w: low report quality score %.2f", ErrQualityRejected, score)
	}

	encoded, err := json.Marshal(map[string]any{
		"title":          title,
		"sections":       sections,
		"prompt_version": payload.PromptVersion,
		"model_id":       payload.ModelID,
		"quality_score":  round2(score),
	})
	if err != nil {
		return nil, 0, fmt.Errorf("encode report payload: %w", err)
	}
	return encoded, round2(score), nil
}

func normalizeText(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	parts := strings.Fields(trimmed)
	return strings.Join(parts, " ")
}

func truncateAtWord(value string, maxLen int) string {
	if len(value) <= maxLen || maxLen <= 0 {
		return value
	}
	cut := value[:maxLen]
	lastSpace := strings.LastIndex(cut, " ")
	if lastSpace > maxLen/2 {
		cut = cut[:lastSpace]
	}
	return strings.TrimSpace(cut)
}

func hasTerminalPunctuation(value string) bool {
	if value == "" {
		return false
	}
	last := value[len(value)-1]
	return last == '.' || last == '!' || last == '?'
}

func toneMismatch(value string, tone string) bool {
	if tone != "formal" {
		return false
	}
	lowered := strings.ToLower(value)
	for _, slang := range []string{
		"mano",
		"vlw",
		"blz",
		"cara",
		"bro",
	} {
		if strings.Contains(lowered, slang) {
			return true
		}
	}
	return false
}

func localeMismatch(value string, locale string) bool {
	if locale == "" {
		return false
	}

	lowered := strings.ToLower(value)
	switch {
	case strings.HasPrefix(locale, "pt"):
		return hasMoreMarkers(lowered, enMarkers, ptMarkers)
	case strings.HasPrefix(locale, "en"):
		return hasMoreMarkers(lowered, ptMarkers, enMarkers)
	default:
		return false
	}
}

func hasMoreMarkers(value string, negative []string, positive []string) bool {
	negativeCount := 0
	for _, marker := range negative {
		if strings.Contains(value, marker) {
			negativeCount++
		}
	}
	positiveCount := 0
	for _, marker := range positive {
		if strings.Contains(value, marker) {
			positiveCount++
		}
	}
	return negativeCount > positiveCount+1
}

var ptMarkers = []string{
	" voce ",
	" obrigado",
	" por favor",
	" vamos ",
	" que ",
	" com ",
}

var enMarkers = []string{
	" you ",
	" thanks",
	" please",
	" we ",
	" with ",
	" and ",
}

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func round2(value float64) float64 {
	return math.Round(value*100) / 100
}
