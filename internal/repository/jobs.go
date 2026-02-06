package repository

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/iago/extensao-whatsapp-back/internal/domain"
)

var ErrNotFound = errors.New("resource not found")

// JobsRepository abstracts job persistence and query operations.
type JobsRepository interface {
	CreateJob(ctx context.Context, job *domain.Job) error
	UpdateJob(ctx context.Context, job *domain.Job) error
	GetJob(ctx context.Context, jobID string) (*domain.Job, error)
	ListReports(ctx context.Context, filter domain.ReportListFilter) ([]domain.ReportListItem, int, error)
}

// MemoryJobsRepository stores jobs in memory for local development.
type MemoryJobsRepository struct {
	mu   sync.RWMutex
	jobs map[string]*domain.Job
}

func NewMemoryJobsRepository() *MemoryJobsRepository {
	return &MemoryJobsRepository{
		jobs: make(map[string]*domain.Job),
	}
}

func (r *MemoryJobsRepository) CreateJob(_ context.Context, job *domain.Job) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	clone := cloneJob(job)
	r.jobs[job.ID] = clone
	return nil
}

func (r *MemoryJobsRepository) UpdateJob(_ context.Context, job *domain.Job) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.jobs[job.ID]; !ok {
		return ErrNotFound
	}
	r.jobs[job.ID] = cloneJob(job)
	return nil
}

func (r *MemoryJobsRepository) GetJob(_ context.Context, jobID string) (*domain.Job, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	job, ok := r.jobs[jobID]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneJob(job), nil
}

func (r *MemoryJobsRepository) ListReports(
	_ context.Context,
	filter domain.ReportListFilter,
) ([]domain.ReportListItem, int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if filter.Page <= 0 {
		filter.Page = 1
	}
	if filter.PageSize <= 0 {
		filter.PageSize = 20
	}

	items := make([]domain.ReportListItem, 0)
	for _, job := range r.jobs {
		if job.Kind != domain.JobKindReport {
			continue
		}
		if filter.TenantID != "" && job.TenantID != filter.TenantID {
			continue
		}
		if filter.From != nil && job.CreatedAt.Before(*filter.From) {
			continue
		}
		if filter.To != nil && job.CreatedAt.After(*filter.To) {
			continue
		}
		if filter.Topic != "" && !strings.Contains(strings.ToLower(string(job.Payload)), strings.ToLower(filter.Topic)) {
			continue
		}

		title := "Relatorio"
		if job.Status == domain.JobStatusDone {
			title = "Relatorio gerado"
		}

		items = append(items, domain.ReportListItem{
			ReportID:       job.ID,
			ConversationID: job.ConversationID,
			Status:         job.Status,
			CreatedAt:      job.CreatedAt,
			Title:          title,
		})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})

	total := len(items)
	start := (filter.Page - 1) * filter.PageSize
	if start >= total {
		return []domain.ReportListItem{}, total, nil
	}
	end := start + filter.PageSize
	if end > total {
		end = total
	}

	return items[start:end], total, nil
}

func cloneJob(job *domain.Job) *domain.Job {
	if job == nil {
		return nil
	}
	clone := *job
	clone.Payload = append([]byte(nil), job.Payload...)
	clone.Result = append([]byte(nil), job.Result...)
	return &clone
}

func parseDateTime(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}
