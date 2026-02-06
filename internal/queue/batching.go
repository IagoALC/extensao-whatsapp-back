package queue

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/iago/extensao-whatsapp-back/internal/domain"
)

var (
	ErrQueueBackpressure = errors.New("queue backpressure: enqueue buffer is full")
	ErrBatchingClosed    = errors.New("batching producer is closed")
)

type BatchingConfig struct {
	MaxBatchSize       int
	FlushInterval      time.Duration
	FlushTimeout       time.Duration
	QueueCapacity      int
	MaxInFlightBatches int
}

type batchCapableProducer interface {
	EnqueueBatch(ctx context.Context, messages []domain.QueueMessage) error
}

type enqueueRequest struct {
	ctx     context.Context
	message domain.QueueMessage
	result  chan error
}

// BatchingProducer groups close-in-time enqueue operations and applies bounded buffering.
type BatchingProducer struct {
	base        Producer
	batchWriter batchCapableProducer

	in         chan enqueueRequest
	semaphore  chan struct{}
	stop       chan struct{}
	done       chan struct{}
	closeOnce  sync.Once
	config     BatchingConfig
	parentDone <-chan struct{}
}

func NewBatchingProducer(
	parent context.Context,
	base Producer,
	cfg BatchingConfig,
) *BatchingProducer {
	if cfg.MaxBatchSize <= 0 {
		cfg.MaxBatchSize = 32
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 25 * time.Millisecond
	}
	if cfg.FlushTimeout <= 0 {
		cfg.FlushTimeout = 3 * time.Second
	}
	if cfg.QueueCapacity <= 0 {
		cfg.QueueCapacity = 2048
	}
	if cfg.MaxInFlightBatches <= 0 {
		cfg.MaxInFlightBatches = 4
	}

	batcher := &BatchingProducer{
		base:        base,
		in:          make(chan enqueueRequest, cfg.QueueCapacity),
		semaphore:   make(chan struct{}, cfg.MaxInFlightBatches),
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
		config:      cfg,
		parentDone:  parent.Done(),
		batchWriter: nil,
	}
	if writer, ok := base.(batchCapableProducer); ok {
		batcher.batchWriter = writer
	}

	go batcher.run()
	return batcher
}

func (b *BatchingProducer) Enqueue(ctx context.Context, message domain.QueueMessage) error {
	if ctx == nil {
		ctx = context.Background()
	}

	request := enqueueRequest{
		ctx:     ctx,
		message: message,
		result:  make(chan error, 1),
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.done:
		return ErrBatchingClosed
	default:
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.done:
		return ErrBatchingClosed
	case b.in <- request:
	default:
		return ErrQueueBackpressure
	}

	select {
	case err := <-request.result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *BatchingProducer) Close() {
	b.closeOnce.Do(func() {
		close(b.stop)
		<-b.done
	})
}

func (b *BatchingProducer) run() {
	defer close(b.done)

	pending := make([]enqueueRequest, 0, b.config.MaxBatchSize)
	timer := time.NewTimer(b.config.FlushInterval)
	stopTimer(timer)
	timerRunning := false

	flush := func(final bool) {
		if len(pending) == 0 {
			return
		}
		batch := append([]enqueueRequest(nil), pending...)
		pending = pending[:0]
		b.flushBatch(batch, final)
	}

	for {
		var timerCh <-chan time.Time
		if timerRunning {
			timerCh = timer.C
		}

		select {
		case <-b.parentDone:
			stopTimer(timer)
			flush(true)
			return
		case <-b.stop:
			stopTimer(timer)
			flush(true)
			return
		case <-timerCh:
			timerRunning = false
			flush(false)
		case request := <-b.in:
			if request.ctx.Err() != nil {
				request.result <- request.ctx.Err()
				continue
			}
			pending = append(pending, request)
			if len(pending) == 1 {
				resetTimer(timer, b.config.FlushInterval)
				timerRunning = true
			}
			if len(pending) >= b.config.MaxBatchSize {
				stopTimer(timer)
				timerRunning = false
				flush(false)
			}
		}
	}
}

func (b *BatchingProducer) flushBatch(batch []enqueueRequest, final bool) {
	active := make([]enqueueRequest, 0, len(batch))
	for _, request := range batch {
		if err := request.ctx.Err(); err != nil {
			request.result <- err
			continue
		}
		active = append(active, request)
	}
	if len(active) == 0 {
		return
	}

	// Coalescing by tenant/conversation improves locality while preserving deterministic order.
	sort.SliceStable(active, func(i, j int) bool {
		left := coalesceKey(active[i].message)
		right := coalesceKey(active[j].message)
		if left == right {
			return active[i].message.RequestedAt.Before(active[j].message.RequestedAt)
		}
		return left < right
	})

	messages := make([]domain.QueueMessage, 0, len(active))
	for _, request := range active {
		messages = append(messages, request.message)
	}

	flushCtx := context.Background()
	if !final {
		var cancel context.CancelFunc
		flushCtx, cancel = context.WithTimeout(context.Background(), b.config.FlushTimeout)
		defer cancel()
	}

	select {
	case b.semaphore <- struct{}{}:
	case <-flushCtx.Done():
		for _, request := range active {
			request.result <- flushCtx.Err()
		}
		return
	}
	defer func() { <-b.semaphore }()

	var enqueueErr error
	if b.batchWriter != nil {
		enqueueErr = b.batchWriter.EnqueueBatch(flushCtx, messages)
	} else {
		for _, message := range messages {
			if err := b.base.Enqueue(flushCtx, message); err != nil {
				enqueueErr = err
				break
			}
		}
	}

	for _, request := range active {
		request.result <- enqueueErr
	}
}

func coalesceKey(message domain.QueueMessage) string {
	return strings.Join([]string{
		message.TenantID,
		message.ConversationID,
		string(message.Kind),
	}, "|")
}

func stopTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func resetTimer(timer *time.Timer, value time.Duration) {
	if timer == nil {
		return
	}
	stopTimer(timer)
	timer.Reset(value)
}
