package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/iago/extensao-whatsapp-back/internal/domain"
	"github.com/iago/extensao-whatsapp-back/internal/policy"
	"github.com/iago/extensao-whatsapp-back/internal/queue"
	"github.com/iago/extensao-whatsapp-back/internal/repository"
)

type JobsService struct {
	repo     repository.JobsRepository
	producer queue.Producer
}

func NewJobsService(repo repository.JobsRepository, producer queue.Producer) *JobsService {
	return &JobsService{repo: repo, producer: producer}
}

func (s *JobsService) EnqueueSummary(
	ctx context.Context,
	tenantID string,
	conversationID string,
	payload json.RawMessage,
) (*domain.Job, error) {
	return s.enqueue(ctx, domain.JobKindSummary, tenantID, conversationID, payload)
}

func (s *JobsService) EnqueueReport(
	ctx context.Context,
	tenantID string,
	conversationID string,
	payload json.RawMessage,
) (*domain.Job, error) {
	return s.enqueue(ctx, domain.JobKindReport, tenantID, conversationID, payload)
}

func (s *JobsService) GetJob(ctx context.Context, jobID string) (*domain.Job, error) {
	return s.repo.GetJob(ctx, jobID)
}

func (s *JobsService) ListReports(
	ctx context.Context,
	filter domain.ReportListFilter,
) ([]domain.ReportListItem, int, error) {
	return s.repo.ListReports(ctx, filter)
}

func (s *JobsService) enqueue(
	ctx context.Context,
	kind domain.JobKind,
	tenantID string,
	conversationID string,
	payload json.RawMessage,
) (*domain.Job, error) {
	sanitizedPayload := policy.MaskPIIJSON(payload)

	now := time.Now().UTC()
	job := &domain.Job{
		ID:             uuid.NewString(),
		Kind:           kind,
		TenantID:       tenantID,
		ConversationID: conversationID,
		Payload:        sanitizedPayload,
		Status:         domain.JobStatusPending,
		Attempts:       0,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := s.repo.CreateJob(ctx, job); err != nil {
		return nil, fmt.Errorf("create job: %w", err)
	}

	message := domain.QueueMessage{
		JobID:          job.ID,
		Kind:           job.Kind,
		TenantID:       tenantID,
		ConversationID: conversationID,
		Payload:        sanitizedPayload,
		Attempt:        0,
		RequestedAt:    now,
	}

	if err := s.producer.Enqueue(ctx, message); err != nil {
		job.Status = domain.JobStatusFailed
		job.ErrorMessage = err.Error()
		job.UpdatedAt = time.Now().UTC()
		_ = s.repo.UpdateJob(ctx, job)
		return nil, fmt.Errorf("enqueue job: %w", err)
	}

	return job, nil
}
