package queue

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/iago/extensao-whatsapp-back/internal/domain"
	"github.com/redis/go-redis/v9"
)

type StreamsConfig struct {
	Addr        string
	Password    string
	DB          int
	Stream      string
	DLQStream   string
	Group       string
	Consumer    string
	MaxAttempts int
}

// StreamsQueue implements Producer+Consumer backed by Redis Streams.
type StreamsQueue struct {
	client      *redis.Client
	stream      string
	dlqStream   string
	group       string
	consumer    string
	maxAttempts int
}

func NewStreamsQueue(ctx context.Context, cfg StreamsConfig) (*StreamsQueue, error) {
	if cfg.Addr == "" {
		return nil, errors.New("redis address is required")
	}
	if cfg.Stream == "" {
		cfg.Stream = "wa_jobs"
	}
	if cfg.DLQStream == "" {
		cfg.DLQStream = "wa_jobs_dlq"
	}
	if cfg.Group == "" {
		cfg.Group = "wa_workers"
	}
	if cfg.Consumer == "" {
		cfg.Consumer = "api-1"
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}

	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	queue := &StreamsQueue{
		client:      client,
		stream:      cfg.Stream,
		dlqStream:   cfg.DLQStream,
		group:       cfg.Group,
		consumer:    cfg.Consumer,
		maxAttempts: cfg.MaxAttempts,
	}
	if err := queue.ensureGroup(ctx); err != nil {
		client.Close()
		return nil, err
	}
	return queue, nil
}

func (q *StreamsQueue) Close() error {
	return q.client.Close()
}

func (q *StreamsQueue) Enqueue(ctx context.Context, message domain.QueueMessage) error {
	_, err := q.client.XAdd(ctx, &redis.XAddArgs{
		Stream: q.stream,
		Values: map[string]any{
			"job_id":          message.JobID,
			"kind":            string(message.Kind),
			"tenant_id":       message.TenantID,
			"conversation_id": message.ConversationID,
			"payload":         string(message.Payload),
			"attempt":         message.Attempt,
			"requested_at":    message.RequestedAt.Format(time.RFC3339Nano),
		},
	}).Result()
	if err != nil {
		return fmt.Errorf("enqueue to stream: %w", err)
	}
	return nil
}

func (q *StreamsQueue) EnqueueBatch(ctx context.Context, messages []domain.QueueMessage) error {
	if len(messages) == 0 {
		return nil
	}

	pipeline := q.client.Pipeline()
	for _, message := range messages {
		pipeline.XAdd(ctx, &redis.XAddArgs{
			Stream: q.stream,
			Values: map[string]any{
				"job_id":          message.JobID,
				"kind":            string(message.Kind),
				"tenant_id":       message.TenantID,
				"conversation_id": message.ConversationID,
				"payload":         string(message.Payload),
				"attempt":         message.Attempt,
				"requested_at":    message.RequestedAt.Format(time.RFC3339Nano),
			},
		})
	}

	if _, err := pipeline.Exec(ctx); err != nil {
		return fmt.Errorf("enqueue batch to stream: %w", err)
	}
	return nil
}

func (q *StreamsQueue) Consume(ctx context.Context, handler func(context.Context, domain.QueueMessage) error) error {
	if err := q.ensureGroup(ctx); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		streams, err := q.client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    q.group,
			Consumer: q.consumer,
			Streams:  []string{q.stream, ">"},
			Count:    10,
			Block:    5 * time.Second,
		}).Result()

		if err != nil {
			if errors.Is(err, redis.Nil) {
				continue
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			return fmt.Errorf("xreadgroup: %w", err)
		}

		for _, stream := range streams {
			for _, item := range stream.Messages {
				message, parseErr := parseStreamMessage(item)
				if parseErr != nil {
					_ = q.sendToDLQ(ctx, domain.QueueMessage{}, item, parseErr.Error())
					_ = q.ackAndDelete(ctx, item.ID)
					continue
				}

				handleErr := handler(ctx, message)
				if handleErr == nil {
					_ = q.ackAndDelete(ctx, item.ID)
					continue
				}

				message.Attempt++
				if message.Attempt >= q.maxAttempts {
					_ = q.sendToDLQ(ctx, message, item, handleErr.Error())
					_ = q.ackAndDelete(ctx, item.ID)
					continue
				}

				if requeueErr := q.Enqueue(ctx, message); requeueErr != nil {
					_ = q.sendToDLQ(ctx, message, item, fmt.Sprintf("requeue failed: %v", requeueErr))
				}
				_ = q.ackAndDelete(ctx, item.ID)
			}
		}
	}
}

func (q *StreamsQueue) ensureGroup(ctx context.Context) error {
	err := q.client.XGroupCreateMkStream(ctx, q.stream, q.group, "$").Err()
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "BUSYGROUP") {
		return nil
	}
	return fmt.Errorf("ensure stream group: %w", err)
}

func (q *StreamsQueue) ackAndDelete(ctx context.Context, streamID string) error {
	if err := q.client.XAck(ctx, q.stream, q.group, streamID).Err(); err != nil {
		return fmt.Errorf("xack: %w", err)
	}
	if err := q.client.XDel(ctx, q.stream, streamID).Err(); err != nil {
		return fmt.Errorf("xdel: %w", err)
	}
	return nil
}

func (q *StreamsQueue) sendToDLQ(
	ctx context.Context,
	message domain.QueueMessage,
	item redis.XMessage,
	errorMessage string,
) error {
	values := map[string]any{
		"stream_id":       item.ID,
		"job_id":          message.JobID,
		"kind":            string(message.Kind),
		"tenant_id":       message.TenantID,
		"conversation_id": message.ConversationID,
		"payload":         string(message.Payload),
		"attempt":         message.Attempt,
		"error":           errorMessage,
		"moved_at":        time.Now().UTC().Format(time.RFC3339Nano),
	}
	if _, err := q.client.XAdd(ctx, &redis.XAddArgs{Stream: q.dlqStream, Values: values}).Result(); err != nil {
		return fmt.Errorf("send to dlq: %w", err)
	}
	return nil
}

func parseStreamMessage(item redis.XMessage) (domain.QueueMessage, error) {
	getString := func(key string) (string, error) {
		value, ok := item.Values[key]
		if !ok {
			return "", fmt.Errorf("missing field %s", key)
		}
		switch casted := value.(type) {
		case string:
			return casted, nil
		case []byte:
			return string(casted), nil
		default:
			return fmt.Sprintf("%v", casted), nil
		}
	}

	payloadString, err := getString("payload")
	if err != nil {
		return domain.QueueMessage{}, err
	}

	attemptString, err := getString("attempt")
	if err != nil {
		return domain.QueueMessage{}, err
	}
	attempt, err := strconv.Atoi(attemptString)
	if err != nil {
		return domain.QueueMessage{}, fmt.Errorf("invalid attempt: %w", err)
	}

	requestedAtString, err := getString("requested_at")
	if err != nil {
		return domain.QueueMessage{}, err
	}
	requestedAt, err := time.Parse(time.RFC3339Nano, requestedAtString)
	if err != nil {
		return domain.QueueMessage{}, fmt.Errorf("invalid requested_at: %w", err)
	}

	jobID, err := getString("job_id")
	if err != nil {
		return domain.QueueMessage{}, err
	}
	kindValue, err := getString("kind")
	if err != nil {
		return domain.QueueMessage{}, err
	}
	tenantID, err := getString("tenant_id")
	if err != nil {
		return domain.QueueMessage{}, err
	}
	conversationID, err := getString("conversation_id")
	if err != nil {
		return domain.QueueMessage{}, err
	}

	return domain.QueueMessage{
		JobID:          jobID,
		Kind:           domain.JobKind(kindValue),
		TenantID:       tenantID,
		ConversationID: conversationID,
		Payload:        []byte(payloadString),
		Attempt:        attempt,
		RequestedAt:    requestedAt,
	}, nil
}
