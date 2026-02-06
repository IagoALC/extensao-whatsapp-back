package queue

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/iago/extensao-whatsapp-back/internal/domain"
)

// LocalQueue is a fallback queue used when Redis is not configured.
type LocalQueue struct {
	ch          chan domain.QueueMessage
	maxAttempts int
	logger      *log.Logger

	dlqMu sync.Mutex
	dlq   []domain.QueueMessage
}

func NewLocalQueue(bufferSize, maxAttempts int, logger *log.Logger) *LocalQueue {
	if bufferSize <= 0 {
		bufferSize = 512
	}
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	return &LocalQueue{
		ch:          make(chan domain.QueueMessage, bufferSize),
		maxAttempts: maxAttempts,
		logger:      logger,
		dlq:         make([]domain.QueueMessage, 0),
	}
}

func (q *LocalQueue) Enqueue(ctx context.Context, message domain.QueueMessage) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case q.ch <- message:
		return nil
	}
}

func (q *LocalQueue) EnqueueBatch(ctx context.Context, messages []domain.QueueMessage) error {
	for _, message := range messages {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case q.ch <- message:
		}
	}
	return nil
}

func (q *LocalQueue) Consume(ctx context.Context, handler func(context.Context, domain.QueueMessage) error) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case message := <-q.ch:
			err := handler(ctx, message)
			if err == nil {
				continue
			}

			message.Attempt++
			if message.Attempt >= q.maxAttempts {
				q.dlqMu.Lock()
				q.dlq = append(q.dlq, message)
				q.dlqMu.Unlock()
				if q.logger != nil {
					q.logger.Printf("local queue moved message to DLQ job_id=%s err=%v", message.JobID, err)
				}
				continue
			}

			delay := time.Duration(message.Attempt) * 500 * time.Millisecond
			go func(retryMessage domain.QueueMessage) {
				timer := time.NewTimer(delay)
				defer timer.Stop()
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
					q.ch <- retryMessage
				}
			}(message)
		}
	}
}

func (q *LocalQueue) DLQSize() int {
	q.dlqMu.Lock()
	defer q.dlqMu.Unlock()
	return len(q.dlq)
}
