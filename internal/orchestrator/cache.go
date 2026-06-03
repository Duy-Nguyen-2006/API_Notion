package orchestrator

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ToolCache struct {
	mu      sync.RWMutex
	entries map[string]ToolResult
}

func NewToolCache() *ToolCache { return &ToolCache{entries: map[string]ToolResult{}} }

func (c *ToolCache) Get(path string) (ToolResult, bool) {
	if c == nil { return ToolResult{}, false }
	key, err := toolCacheKey(path)
	if err != nil { return ToolResult{}, false }
	c.mu.RLock(); defer c.mu.RUnlock()
	result, ok := c.entries[key]
	return result, ok
}

func (c *ToolCache) Set(path string, result ToolResult) {
	if c == nil { return }
	key, err := toolCacheKey(path)
	if err != nil { return }
	c.mu.Lock(); defer c.mu.Unlock()
	c.entries[key] = result
}

func (c *ToolCache) Invalidate(path string) {
	if c == nil { return }
	abs, err := filepath.Abs(path)
	if err != nil { abs = filepath.Clean(path) }
	prefix := abs + "|"
	c.mu.Lock(); defer c.mu.Unlock()
	for key := range c.entries {
		if strings.HasPrefix(key, prefix) { delete(c.entries, key) }
	}
}

func toolCacheKey(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil { return "", err }
	info, err := os.Stat(abs)
	if err != nil { return "", err }
	return abs + "|" + info.ModTime().UTC().Format(time.RFC3339Nano) + "|" + strconv.FormatInt(info.Size(), 10), nil
}

type ResponseCache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry
	ttl     time.Duration
}

type cacheEntry struct { value ModelResult; expires time.Time }

func NewResponseCache(ttl time.Duration) *ResponseCache {
	if ttl <= 0 { ttl = 30 * time.Minute }
	return &ResponseCache{entries: map[string]cacheEntry{}, ttl: ttl}
}

func (c *ResponseCache) Key(query string, messages []Message) string {
	h := sha256.New(); h.Write([]byte(query))
	for _, msg := range messages { h.Write([]byte("\x00" + msg.Role + "\x00" + msg.Content)) }
	return hex.EncodeToString(h.Sum(nil))
}

func (c *ResponseCache) Get(key string) (ModelResult, bool) {
	if c == nil { return ModelResult{}, false }
	c.mu.RLock(); entry, ok := c.entries[key]; c.mu.RUnlock()
	if !ok || time.Now().After(entry.expires) { return ModelResult{}, false }
	return entry.value, true
}

func (c *ResponseCache) Set(key string, value ModelResult) {
	if c == nil { return }
	c.mu.Lock(); c.entries[key] = cacheEntry{value: value, expires: time.Now().Add(c.ttl)}; c.mu.Unlock()
}

type PrefixCache struct { mu sync.RWMutex; value []Message }
func (c *PrefixCache) Get() []Message { if c == nil { return nil }; c.mu.RLock(); defer c.mu.RUnlock(); return append([]Message(nil), c.value...) }
func (c *PrefixCache) Set(v []Message) { if c == nil { return }; c.mu.Lock(); c.value = append([]Message(nil), v...); c.mu.Unlock() }
