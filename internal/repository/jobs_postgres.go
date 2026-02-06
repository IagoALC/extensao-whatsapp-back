package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/iago/extensao-whatsapp-back/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresJobsRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresJobsRepository(ctx context.Context, databaseURL string) (*PostgresJobsRepository, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("create pg pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping pg: %w", err)
	}
	return &PostgresJobsRepository{pool: pool}, nil
}

func (r *PostgresJobsRepository) Close() {
	r.pool.Close()
}

func (r *PostgresJobsRepository) CreateJob(ctx context.Context, job *domain.Job) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO jobs (
			id,
			kind,
			tenant_id,
			conversation_id,
			payload,
			status,
			result,
			error_message,
			attempts,
			created_at,
			updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
	`,
		job.ID,
		string(job.Kind),
		job.TenantID,
		job.ConversationID,
		job.Payload,
		string(job.Status),
		job.Result,
		job.ErrorMessage,
		job.Attempts,
		job.CreatedAt,
		job.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert job: %w", err)
	}
	return nil
}

func (r *PostgresJobsRepository) UpdateJob(ctx context.Context, job *domain.Job) error {
	command, err := r.pool.Exec(ctx, `
		UPDATE jobs
		SET status = $2,
			result = $3,
			error_message = $4,
			attempts = $5,
			updated_at = $6
		WHERE id = $1
	`, job.ID, string(job.Status), job.Result, job.ErrorMessage, job.Attempts, job.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update job: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *PostgresJobsRepository) GetJob(ctx context.Context, jobID string) (*domain.Job, error) {
	var (
		job       domain.Job
		kind      string
		status    string
		payload   []byte
		result    []byte
		createdAt time.Time
		updatedAt time.Time
	)

	err := r.pool.QueryRow(ctx, `
		SELECT id, kind, tenant_id, conversation_id, payload, status, result, error_message, attempts, created_at, updated_at
		FROM jobs
		WHERE id = $1
	`, jobID).Scan(
		&job.ID,
		&kind,
		&job.TenantID,
		&job.ConversationID,
		&payload,
		&status,
		&result,
		&job.ErrorMessage,
		&job.Attempts,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("query job: %w", err)
	}

	job.Kind = domain.JobKind(kind)
	job.Status = domain.JobStatus(status)
	job.Payload = json.RawMessage(payload)
	job.Result = json.RawMessage(result)
	job.CreatedAt = createdAt
	job.UpdatedAt = updatedAt
	return &job, nil
}

func (r *PostgresJobsRepository) ListReports(
	ctx context.Context,
	filter domain.ReportListFilter,
) ([]domain.ReportListItem, int, error) {
	if filter.Page <= 0 {
		filter.Page = 1
	}
	if filter.PageSize <= 0 {
		filter.PageSize = 20
	}

	baseQuery, args := buildReportFilters(filter)

	var total int
	countQuery := "SELECT COUNT(*) " + baseQuery
	if err := r.pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count reports: %w", err)
	}

	listQuery := fmt.Sprintf(
		`SELECT id, conversation_id, status, created_at
		%s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d`,
		baseQuery,
		len(args)+1,
		len(args)+2,
	)
	listArgs := append(args, filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := r.pool.Query(ctx, listQuery, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list reports: %w", err)
	}
	defer rows.Close()

	items := make([]domain.ReportListItem, 0)
	for rows.Next() {
		var (
			item      domain.ReportListItem
			status    string
			createdAt time.Time
		)
		if err := rows.Scan(&item.ReportID, &item.ConversationID, &status, &createdAt); err != nil {
			return nil, 0, fmt.Errorf("scan report item: %w", err)
		}
		item.Status = domain.JobStatus(status)
		item.CreatedAt = createdAt
		if item.Status == domain.JobStatusDone {
			item.Title = "Relatorio gerado"
		} else {
			item.Title = "Relatorio"
		}
		items = append(items, item)
	}

	if rows.Err() != nil {
		return nil, 0, fmt.Errorf("iterate report items: %w", rows.Err())
	}

	return items, total, nil
}

func buildReportFilters(filter domain.ReportListFilter) (string, []any) {
	query := strings.Builder{}
	query.WriteString("FROM jobs WHERE kind = 'report'")

	args := make([]any, 0, 4)
	argIndex := 1

	if tenantID := strings.TrimSpace(filter.TenantID); tenantID != "" {
		query.WriteString(fmt.Sprintf(" AND tenant_id = $%d", argIndex))
		args = append(args, tenantID)
		argIndex++
	}

	if filter.From != nil {
		query.WriteString(fmt.Sprintf(" AND created_at >= $%d", argIndex))
		args = append(args, *filter.From)
		argIndex++
	}

	if filter.To != nil {
		query.WriteString(fmt.Sprintf(" AND created_at <= $%d", argIndex))
		args = append(args, *filter.To)
		argIndex++
	}

	if topic := strings.TrimSpace(filter.Topic); topic != "" {
		query.WriteString(fmt.Sprintf(" AND payload::text ILIKE '%%' || $%d || '%%'", argIndex))
		args = append(args, topic)
		argIndex++
	}

	return query.String(), args
}
