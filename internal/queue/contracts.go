package queue

import (
	"context"

	"github.com/iago/extensao-whatsapp-back/internal/domain"
)

// Producer sends async jobs to a queue backend.
type Producer interface {
	Enqueue(ctx context.Context, message domain.QueueMessage) error
}

// Consumer receives async jobs and executes handlers.
type Consumer interface {
	Consume(ctx context.Context, handler func(context.Context, domain.QueueMessage) error) error
}
