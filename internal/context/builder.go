package contextbuilder

import (
	"context"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"sync"
	"time"
)

type BuildInput struct {
	Task           string
	TenantID       string
	ConversationID string
	Payload        []byte
	MaxInputTokens int
	MaxChunks      int
	ContextWindow  int
}

type BuildOutput struct {
	ContextText string
	Chunks      []Chunk
	TokenCount  int
}

type cachedBuild struct {
	output    BuildOutput
	expiresAt time.Time
}

type Builder struct {
	retriever Retriever

	cacheMu    sync.RWMutex
	cache      map[uint64]cachedBuild
	cacheTTL   time.Duration
	cacheLimit int
}

func NewBuilder(retriever Retriever) *Builder {
	return &Builder{
		retriever:  retriever,
		cache:      make(map[uint64]cachedBuild),
		cacheTTL:   90 * time.Second,
		cacheLimit: 1024,
	}
}

func (b *Builder) Build(ctx context.Context, input BuildInput) (BuildOutput, error) {
	if b.retriever == nil {
		return BuildOutput{}, fmt.Errorf("retriever is required")
	}
	input = normalizeBuildInput(input)

	cacheKey := buildCacheKey(input)
	if cached, ok := b.cacheGet(cacheKey); ok {
		return cloneBuildOutput(cached), nil
	}

	chunks, err := b.retriever.Retrieve(ctx, RetrievalInput{
		Task:           input.Task,
		TenantID:       input.TenantID,
		ConversationID: input.ConversationID,
		Payload:        input.Payload,
		ContextWindow:  input.ContextWindow,
	})
	if err != nil {
		return BuildOutput{}, err
	}
	chunks = dedupeChunks(chunks)

	sort.SliceStable(chunks, func(i, j int) bool {
		if chunks[i].Score == chunks[j].Score {
			return chunks[i].ID < chunks[j].ID
		}
		return chunks[i].Score > chunks[j].Score
	})

	selected := make([]Chunk, 0, len(chunks))
	totalTokens := 0
	for _, chunk := range chunks {
		estimatedTokens := estimateTokens(chunk.Text)
		if estimatedTokens <= 0 {
			continue
		}
		if totalTokens+estimatedTokens > input.MaxInputTokens {
			continue
		}
		selected = append(selected, chunk)
		totalTokens += estimatedTokens
		if len(selected) >= input.MaxChunks {
			break
		}
	}

	if len(selected) == 0 {
		fallback := "Contexto minimo: sem dados suficientes no payload para composicao detalhada."
		selected = append(selected, Chunk{ID: "fallback", Text: fallback, Score: 1})
		totalTokens = estimateTokens(fallback)
	}

	builder := strings.Builder{}
	builder.WriteString("Contexto priorizado:\n")
	for index, chunk := range selected {
		builder.WriteString(fmt.Sprintf("[%d] %s\n", index+1, chunk.Text))
	}

	output := BuildOutput{
		ContextText: strings.TrimSpace(builder.String()),
		Chunks:      selected,
		TokenCount:  totalTokens,
	}
	b.cachePut(cacheKey, output)
	return cloneBuildOutput(output), nil
}

func normalizeBuildInput(input BuildInput) BuildInput {
	if input.MaxInputTokens <= 0 {
		switch strings.ToLower(strings.TrimSpace(input.Task)) {
		case "suggestion":
			input.MaxInputTokens = 1600
		case "summary":
			input.MaxInputTokens = 3200
		case "report":
			input.MaxInputTokens = 5200
		default:
			input.MaxInputTokens = 2500
		}
	}
	if input.MaxChunks <= 0 {
		switch strings.ToLower(strings.TrimSpace(input.Task)) {
		case "suggestion":
			input.MaxChunks = 6
		case "summary":
			input.MaxChunks = 10
		case "report":
			input.MaxChunks = 12
		default:
			input.MaxChunks = 8
		}
	}
	if input.ContextWindow <= 0 {
		input.ContextWindow = 20
	}
	return input
}

func buildCacheKey(input BuildInput) uint64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(strings.ToLower(strings.TrimSpace(input.Task))))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(strings.TrimSpace(input.TenantID)))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(strings.TrimSpace(input.ConversationID)))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(fmt.Sprintf("%d|%d|%d", input.MaxInputTokens, input.MaxChunks, input.ContextWindow)))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(input.Payload)
	return hash.Sum64()
}

func (b *Builder) cacheGet(key uint64) (BuildOutput, bool) {
	b.cacheMu.RLock()
	entry, exists := b.cache[key]
	b.cacheMu.RUnlock()
	if !exists {
		return BuildOutput{}, false
	}
	if time.Now().After(entry.expiresAt) {
		b.cacheMu.Lock()
		delete(b.cache, key)
		b.cacheMu.Unlock()
		return BuildOutput{}, false
	}
	return entry.output, true
}

func (b *Builder) cachePut(key uint64, output BuildOutput) {
	if b.cacheLimit <= 0 {
		return
	}

	now := time.Now()
	entry := cachedBuild{
		output:    cloneBuildOutput(output),
		expiresAt: now.Add(b.cacheTTL),
	}

	b.cacheMu.Lock()
	defer b.cacheMu.Unlock()

	if len(b.cache) >= b.cacheLimit {
		for cacheKey, cacheEntry := range b.cache {
			if now.After(cacheEntry.expiresAt) {
				delete(b.cache, cacheKey)
			}
		}
	}
	if len(b.cache) >= b.cacheLimit {
		var (
			oldestKey uint64
			oldestTS  time.Time
			first     = true
		)
		for cacheKey, cacheEntry := range b.cache {
			if first || cacheEntry.expiresAt.Before(oldestTS) {
				first = false
				oldestKey = cacheKey
				oldestTS = cacheEntry.expiresAt
			}
		}
		if !first {
			delete(b.cache, oldestKey)
		}
	}
	b.cache[key] = entry
}

func cloneBuildOutput(value BuildOutput) BuildOutput {
	cloned := BuildOutput{
		ContextText: value.ContextText,
		TokenCount:  value.TokenCount,
		Chunks:      make([]Chunk, 0, len(value.Chunks)),
	}
	for _, chunk := range value.Chunks {
		cloned.Chunks = append(cloned.Chunks, chunk)
	}
	return cloned
}

func dedupeChunks(chunks []Chunk) []Chunk {
	if len(chunks) <= 1 {
		return chunks
	}

	seen := make(map[string]Chunk, len(chunks))
	order := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		key := strings.ToLower(strings.Join(strings.Fields(chunk.Text), " "))
		existing, exists := seen[key]
		if !exists {
			seen[key] = chunk
			order = append(order, key)
			continue
		}
		if chunk.Score > existing.Score {
			seen[key] = chunk
		}
	}

	result := make([]Chunk, 0, len(order))
	for _, key := range order {
		result = append(result, seen[key])
	}
	return result
}

func estimateTokens(text string) int {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0
	}
	count := len([]rune(trimmed)) / 4
	if count < 1 {
		count = 1
	}
	return count
}
