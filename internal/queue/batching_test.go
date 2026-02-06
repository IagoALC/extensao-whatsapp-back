package queue

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/iago/extensao-whatsapp-back/internal/domain"
)

type recordingBatchProducer struct {
	mu      sync.Mutex
	batches [][]domain.QueueMessage
}

func (p *recordingBatchProducer) Enqueue(ctx context.Context, message domain.QueueMessage) error {
	return p.EnqueueBatch(ctx, []domain.QueueMessage{message})
}

func (p *recordingBatchProducer) EnqueueBatch(_ context.Context, messages []domain.QueueMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	copied := make([]domain.QueueMessage, 0, len(messages))
	for _, message := range messages {
		copied = append(copied, message)
	}
	p.batches = append(p.batches, copied)
	return nil
}

func (p *recordingBatchProducer) batchCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.batches)
}

func (p *recordingBatchProducer) totalMessages() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	total := 0
	for _, batch := range p.batches {
		total += len(batch)
	}
	return total
}

type blockingBatchProducer struct {
	block chan struct{}
}

func (p *blockingBatchProducer) Enqueue(ctx context.Context, message domain.QueueMessage) error {
	return p.EnqueueBatch(ctx, []domain.QueueMessage{message})
}

func (p *blockingBatchProducer) EnqueueBatch(ctx context.Context, _ []domain.QueueMessage) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.block:
		return nil
	}
}

func TestBatchingProducerBatchesRequests(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()

	base := &recordingBatchProducer{}
	batcher := NewBatchingProducer(parent, base, BatchingConfig{
		MaxBatchSize:       8,
		FlushInterval:      20 * time.Millisecond,
		FlushTimeout:       1 * time.Second,
		QueueCapacity:      64,
		MaxInFlightBatches: 2,
	})
	defer batcher.Close()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			err := batcher.Enqueue(context.Background(), domain.QueueMessage{
				JobID:          "job-" + time.Now().UTC().String(),
				Kind:           domain.JobKindSummary,
				TenantID:       "t1",
				ConversationID: "c1",
				Payload:        []byte(`{"index":1}`),
				RequestedAt:    time.Now().UTC().Add(time.Duration(index) * time.Millisecond),
			})
			if err != nil {
				t.Errorf("enqueue failed: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if base.totalMessages() != 10 {
		t.Fatalf("expected 10 enqueued messages, got %d", base.totalMessages())
	}
	if base.batchCount() >= 10 {
		t.Fatalf("expected batching to reduce write count, got %d batches", base.batchCount())
	}
}

func TestBatchingProducerBackpressure(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()

	base := &blockingBatchProducer{block: make(chan struct{})}
	batcher := NewBatchingProducer(parent, base, BatchingConfig{
		MaxBatchSize:       1,
		FlushInterval:      200 * time.Millisecond,
		FlushTimeout:       2 * time.Second,
		QueueCapacity:      1,
		MaxInFlightBatches: 1,
	})
	defer batcher.Close()

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- batcher.Enqueue(context.Background(), domain.QueueMessage{
			JobID:          "job-first",
			Kind:           domain.JobKindSummary,
			TenantID:       "t1",
			ConversationID: "c1",
			RequestedAt:    time.Now().UTC(),
		})
	}()

	// Allow the internal loop to start flushing and block on base producer.
	time.Sleep(30 * time.Millisecond)

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- batcher.Enqueue(context.Background(), domain.QueueMessage{
			JobID:          "job-second",
			Kind:           domain.JobKindSummary,
			TenantID:       "t1",
			ConversationID: "c1",
			RequestedAt:    time.Now().UTC(),
		})
	}()

	time.Sleep(10 * time.Millisecond)

	thirdErr := batcher.Enqueue(context.Background(), domain.QueueMessage{
		JobID:          "job-third",
		Kind:           domain.JobKindSummary,
		TenantID:       "t1",
		ConversationID: "c1",
		RequestedAt:    time.Now().UTC(),
	})
	if thirdErr != ErrQueueBackpressure {
		t.Fatalf("expected backpressure error, got %v", thirdErr)
	}

	// Release blocking flushes and ensure pending enqueues complete.
	close(base.block)
	if err := <-firstDone; err != nil {
		t.Fatalf("first enqueue failed unexpectedly: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second enqueue failed unexpectedly: %v", err)
	}
}
