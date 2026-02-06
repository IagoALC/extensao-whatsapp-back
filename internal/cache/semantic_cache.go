package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"
)

type Entry struct {
	Value         json.RawMessage
	ModelID       string
	PromptVersion string
	CreatedAt     time.Time
	ExpiresAt     time.Time
}

type Config struct {
	TTL        time.Duration
	MaxEntries int
}

type SemanticCache struct {
	mu         sync.RWMutex
	entries    map[string]Entry
	ttl        time.Duration
	maxEntries int
}

func NewSemanticCache(config Config) *SemanticCache {
	if config.TTL <= 0 {
		config.TTL = 15 * time.Minute
	}
	if config.MaxEntries <= 0 {
		config.MaxEntries = 2000
	}
	return &SemanticCache{
		entries:    make(map[string]Entry),
		ttl:        config.TTL,
		maxEntries: config.MaxEntries,
	}
}

func (c *SemanticCache) Get(signature string) (Entry, bool) {
	c.mu.RLock()
	entry, exists := c.entries[signature]
	c.mu.RUnlock()

	if !exists {
		return Entry{}, false
	}
	if time.Now().UTC().After(entry.ExpiresAt) {
		c.mu.Lock()
		delete(c.entries, signature)
		c.mu.Unlock()
		return Entry{}, false
	}
	return cloneEntry(entry), true
}

func (c *SemanticCache) Set(signature string, entry Entry) {
	now := time.Now().UTC()
	entry.CreatedAt = now
	entry.ExpiresAt = now.Add(c.ttl)
	entry.Value = append([]byte(nil), entry.Value...)

	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) >= c.maxEntries {
		c.evictOldest()
	}
	c.entries[signature] = entry
}

func (c *SemanticCache) BuildSignature(parts ...string) string {
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(strings.ToLower(part))
		normalized = append(normalized, trimmed)
	}
	joined := strings.Join(normalized, "||")
	sum := sha256.Sum256([]byte(joined))
	return hex.EncodeToString(sum[:])
}

func (c *SemanticCache) evictOldest() {
	if len(c.entries) == 0 {
		return
	}

	type pair struct {
		key   string
		value Entry
	}
	pairs := make([]pair, 0, len(c.entries))
	for key, value := range c.entries {
		pairs = append(pairs, pair{key: key, value: value})
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].value.CreatedAt.Before(pairs[j].value.CreatedAt)
	})
	delete(c.entries, pairs[0].key)
}

func cloneEntry(entry Entry) Entry {
	clone := entry
	clone.Value = append([]byte(nil), entry.Value...)
	return clone
}
