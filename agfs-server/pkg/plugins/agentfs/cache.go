package agentfs

import (
	"sort"
	"strings"
	"sync"
	"time"
)

type cacheEntry struct {
	data    []byte
	stored  time.Time
	expires time.Time
}

// CacheInfo describes a cached generated file.
type CacheInfo struct {
	Path    string
	Size    int64
	ModTime time.Time
}

// Cache is a thread-safe TTL cache for generated file content.
type Cache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry
}

func NewCache() *Cache {
	return &Cache{entries: map[string]cacheEntry{}}
}

func (c *Cache) Get(p string) ([]byte, bool) {
	c.mu.RLock()
	e, ok := c.entries[p]
	c.mu.RUnlock()
	if !ok || time.Now().After(e.expires) {
		return nil, false
	}
	return e.data, true
}

func (c *Cache) GetInfo(p string) (CacheInfo, bool) {
	c.mu.RLock()
	e, ok := c.entries[p]
	c.mu.RUnlock()
	if !ok || time.Now().After(e.expires) {
		return CacheInfo{}, false
	}
	return CacheInfo{Path: p, Size: int64(len(e.data)), ModTime: e.stored}, true
}

func (c *Cache) Set(p string, data []byte, ttl time.Duration) {
	if ttl <= 0 {
		ttl = DefaultSkillTTL
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[p] = cacheEntry{data: data, stored: now, expires: now.Add(ttl)}
}

func (c *Cache) InvalidatePath(p string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, p)
}

// InvalidatePrefix drops every entry whose path equals prefix or lives under it.
func (c *Cache) InvalidatePrefix(prefix string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for p := range c.entries {
		if p == prefix || strings.HasPrefix(p, prefix) {
			delete(c.entries, p)
			n++
		}
	}
	return n
}

// ListPrefix returns cached entries under prefix sorted by path.
func (c *Cache) ListPrefix(prefix string) []CacheInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	now := time.Now()
	var out []CacheInfo
	for p, e := range c.entries {
		if strings.HasPrefix(p, prefix) && now.Before(e.expires) {
			out = append(out, CacheInfo{Path: p, Size: int64(len(e.data)), ModTime: e.stored})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
