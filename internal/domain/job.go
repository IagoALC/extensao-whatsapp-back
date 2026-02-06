package domain

import (
	"encoding/json"
	"time"
)

type JobKind string

const (
	JobKindSummary JobKind = "summary"
	JobKindReport  JobKind = "report"
)

type JobStatus string

const (
	JobStatusPending    JobStatus = "pending"
	JobStatusProcessing JobStatus = "processing"
	JobStatusDone       JobStatus = "done"
	JobStatusFailed     JobStatus = "failed"
)

// Job is the canonical async unit processed by worker pipelines.
type Job struct {
	ID             string
	Kind           JobKind
	TenantID       string
	ConversationID string
	Payload        json.RawMessage
	Status         JobStatus
	Result         json.RawMessage
	ErrorMessage   string
	Attempts       int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// QueueMessage is the transport format sent to queue backends.
type QueueMessage struct {
	JobID          string          `json:"job_id"`
	Kind           JobKind         `json:"kind"`
	TenantID       string          `json:"tenant_id"`
	ConversationID string          `json:"conversation_id"`
	Payload        json.RawMessage `json:"payload"`
	Attempt        int             `json:"attempt"`
	RequestedAt    time.Time       `json:"requested_at"`
}

type ReportListItem struct {
	ReportID       string
	ConversationID string
	Status         JobStatus
	CreatedAt      time.Time
	Title          string
}

type ReportListFilter struct {
	TenantID string
	Page     int
	PageSize int
	From     *time.Time
	To       *time.Time
	Topic    string
}
