package ai

import "strings"

type TaskKind string

const (
	TaskSuggestion TaskKind = "suggestion"
	TaskSummary    TaskKind = "summary"
	TaskReport     TaskKind = "report"
)

type ModelProfile struct {
	PrimaryModel    string
	FallbackModel   string
	Temperature     float64
	MaxOutputTokens int
}

type ModelRouterConfig struct {
	SuggestionPrimary  string
	SuggestionFallback string

	SummaryPrimary  string
	SummaryFallback string

	ReportPrimary  string
	ReportFallback string
}

type ModelRouter struct {
	config ModelRouterConfig
}

func NewModelRouter(config ModelRouterConfig) *ModelRouter {
	if strings.TrimSpace(config.SuggestionPrimary) == "" {
		config.SuggestionPrimary = "openai/gpt-4o-mini"
	}
	if strings.TrimSpace(config.SuggestionFallback) == "" {
		config.SuggestionFallback = "openai/gpt-4o-mini"
	}
	if strings.TrimSpace(config.SummaryPrimary) == "" {
		config.SummaryPrimary = "openai/gpt-4o-mini"
	}
	if strings.TrimSpace(config.SummaryFallback) == "" {
		config.SummaryFallback = "openai/gpt-4o-mini"
	}
	if strings.TrimSpace(config.ReportPrimary) == "" {
		config.ReportPrimary = "openai/gpt-4o-mini"
	}
	if strings.TrimSpace(config.ReportFallback) == "" {
		config.ReportFallback = "openai/gpt-4o-mini"
	}

	return &ModelRouter{config: config}
}

func (r *ModelRouter) Select(task TaskKind) ModelProfile {
	switch task {
	case TaskSuggestion:
		return ModelProfile{
			PrimaryModel:    r.config.SuggestionPrimary,
			FallbackModel:   r.config.SuggestionFallback,
			Temperature:     0.4,
			MaxOutputTokens: 500,
		}
	case TaskSummary:
		return ModelProfile{
			PrimaryModel:    r.config.SummaryPrimary,
			FallbackModel:   r.config.SummaryFallback,
			Temperature:     0.2,
			MaxOutputTokens: 700,
		}
	case TaskReport:
		return ModelProfile{
			PrimaryModel:    r.config.ReportPrimary,
			FallbackModel:   r.config.ReportFallback,
			Temperature:     0.2,
			MaxOutputTokens: 1400,
		}
	default:
		return ModelProfile{
			PrimaryModel:    r.config.SummaryPrimary,
			FallbackModel:   r.config.SummaryFallback,
			Temperature:     0.2,
			MaxOutputTokens: 700,
		}
	}
}
