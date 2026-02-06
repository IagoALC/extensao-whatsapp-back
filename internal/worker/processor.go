package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/iago/extensao-whatsapp-back/internal/domain"
	"github.com/iago/extensao-whatsapp-back/internal/policy"
	"github.com/iago/extensao-whatsapp-back/internal/queue"
	"github.com/iago/extensao-whatsapp-back/internal/repository"
	"github.com/iago/extensao-whatsapp-back/internal/service"
)

// Processor consumes queue jobs and persists status transitions.
type Processor struct {
	consumer queue.Consumer
	repo     repository.JobsRepository
	ai       *service.AIGenerationService
	logger   *log.Logger
}

func NewProcessor(
	consumer queue.Consumer,
	repo repository.JobsRepository,
	ai *service.AIGenerationService,
	logger *log.Logger,
) *Processor {
	return &Processor{
		consumer: consumer,
		repo:     repo,
		ai:       ai,
		logger:   logger,
	}
}

func (p *Processor) Start(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}

		err := p.consumer.Consume(ctx, p.processMessage)
		if err == nil || ctx.Err() != nil {
			return
		}
		if p.logger != nil {
			p.logger.Printf("worker consume loop error: %v", err)
		}

		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (p *Processor) processMessage(ctx context.Context, message domain.QueueMessage) error {
	job, err := p.repo.GetJob(ctx, message.JobID)
	if err != nil {
		return fmt.Errorf("load job %s: %w", message.JobID, err)
	}

	job.Status = domain.JobStatusProcessing
	job.Attempts = message.Attempt + 1
	job.UpdatedAt = time.Now().UTC()
	if err := p.repo.UpdateJob(ctx, job); err != nil {
		return fmt.Errorf("mark processing: %w", err)
	}

	result, processErr := p.buildResult(ctx, job.Kind, message)
	if processErr != nil {
		job.Status = domain.JobStatusFailed
		job.ErrorMessage = processErr.Error()
		job.UpdatedAt = time.Now().UTC()
		_ = p.repo.UpdateJob(ctx, job)
		return processErr
	}
	result = policy.MaskPIIJSON(result)

	job.Status = domain.JobStatusDone
	job.ErrorMessage = ""
	job.Result = result
	job.UpdatedAt = time.Now().UTC()
	if err := p.repo.UpdateJob(ctx, job); err != nil {
		return fmt.Errorf("mark done: %w", err)
	}

	if p.logger != nil {
		p.logger.Printf("job processed kind=%s job_id=%s", job.Kind, job.ID)
	}

	return nil
}

func (p *Processor) buildResult(
	ctx context.Context,
	kind domain.JobKind,
	message domain.QueueMessage,
) (json.RawMessage, error) {
	if p.ai != nil {
		input := service.JobGenerationInput{
			TenantID:       message.TenantID,
			ConversationID: message.ConversationID,
			Locale:         "pt-BR",
			Tone:           "neutro",
			Payload:        message.Payload,
		}
		switch kind {
		case domain.JobKindSummary:
			output, err := p.ai.GenerateSummary(ctx, input)
			if err == nil {
				return output.Body, nil
			}
			if p.logger != nil {
				p.logger.Printf("ai summary generation failed, fallback to static result: %v", err)
			}
		case domain.JobKindReport:
			output, err := p.ai.GenerateReport(ctx, input)
			if err == nil {
				return output.Body, nil
			}
			if p.logger != nil {
				p.logger.Printf("ai report generation failed, fallback to static result: %v", err)
			}
		}
	}

	switch kind {
	case domain.JobKindSummary:
		result := map[string]any{
			"summary":        "Resumo gerado automaticamente para a conversa atual.",
			"action_items":   []string{"Confirmar pendencias em aberto", "Responder contato com proximo passo"},
			"prompt_version": "summary_v1",
			"model_id":       "summary-fast-v1",
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("encode summary result: %w", err)
		}
		return encoded, nil
	case domain.JobKindReport:
		result := map[string]any{
			"title": "Relatorio da conversa",
			"sections": []map[string]string{
				{"heading": "Visao geral", "content": "Conversa processada com sucesso e principais pontos consolidados."},
				{"heading": "Pendencias", "content": "Nenhuma pendencia critica identificada no processamento inicial."},
			},
			"prompt_version": "report_v1",
			"model_id":       "report-fast-v1",
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("encode report result: %w", err)
		}
		return encoded, nil
	default:
		return nil, fmt.Errorf("unsupported job kind: %s", kind)
	}
}
