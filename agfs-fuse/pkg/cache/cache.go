package cache

import (
	"container/list"
	"sync"
	"time"

	agfs "github.com/c4pt0r/agfs/agfs-sdk/go"
)

// Option configures a Cache at construction.
type Option func(*Cache)

// WithMaxEntries bounds the cache to at most n entries. When the limit is
// reached, least-recently-used entries are evicted to make room for new
// writes. A value <= 0 disables capacity bounding (backward-compatible
// default — callers that don't opt in keep the old unbounded behavior).
//
// Long-lived mounts should set a finite bound: an unbounded TTL cache can
// grow without bound between cleanup ticks if the access pattern produces
// distinct keys faster than they expire.
func WithMaxEntries(n int) Option {
	return func(c *Cache) {
		if n > 0 {
			c.maxEntries = n
		}
	}
}

// entry represents a cache entry with expiration. The key is duplicated
// here so eviction can locate the map slot from the list element in O(1).
type entry struct {
	key        string
	value      interface{}
	expiration time.Time
}

// isExpired checks if the entry has expired.
func (e *entry) isExpired() bool {
	return time.Now().After(e.expiration)
}

// Cache is a TTL cache with optional LRU bounding.
//
// Concurrency model:
//   - All mutations and reads (including LRU bookkeeping on Get) take the
//     write lock, so a single sync.Mutex is sufficient. An RWMutex would
//     have been wrong because Get must move the element to the front of
//     the eviction list on hit.
//   - A background goroutine sweeps expired entries on a ticker. Call
//     Stop() to terminate it; calling Stop more than once is safe.
//   - Done() returns a channel that the cleanup goroutine closes as the
//     last thing it does on its way out. This gives tests (and shutdown
//     code) a deterministic signal — `<-cache.Done()` — without polling
//     runtime.NumGoroutine, which is timing-sensitive and flaky.
type Cache struct {
	mu         sync.Mutex
	entries    map[string]*list.Element // key -> *list.Element wrapping *entry
	evictList  *list.List               // doubly-linked list of *entry (front = MRU)
	ttl        time.Duration
	maxEntries int           // 0 = unbounded
	stop       chan struct{} // closed by Stop()
	stopOnce   sync.Once     // guards Stop()
	done       chan struct{} // closed by cleanup() when it exits
}

// NewCache creates a cache with the given TTL. Pass WithMaxEntries(...) to
// bound the cache and enable LRU eviction. Without it, the cache only
// expires entries via TTL — appropriate for short-lived process scopes
// but unsafe for long-lived mounts.
func NewCache(ttl time.Duration, opts ...Option) *Cache {
	c := &Cache{
		entries:   make(map[string]*list.Element),
		evictList: list.New(),
		ttl:       ttl,
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	for _, opt := range opts {
		opt(c)
	}

	// Start cleanup goroutine. It exits when Stop() closes c.stop and
	// signals exit by closing c.done.
	go c.cleanup()

	return c
}

// Set stores a value under key. If MaxEntries is set and the cache is at
// capacity, the least-recently-used entry is evicted first.
func (c *Cache) Set(key string, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.entries[key]; ok {
		// Update existing entry and move to front.
		ent := elem.Value.(*entry)
		ent.value = value
		ent.expiration = time.Now().Add(c.ttl)
		c.evictList.MoveToFront(elem)
		return
	}

	ent := &entry{
		key:        key,
		value:      value,
		expiration: time.Now().Add(c.ttl),
	}
	elem := c.evictList.PushFront(ent)
	c.entries[key] = elem

	// Evict from the back if over capacity.
	if c.maxEntries > 0 && c.evictList.Len() > c.maxEntries {
		c.evictOldestLocked()
	}
}

// Get retrieves a value under key. A hit moves the entry to the front of
// the LRU list. Expired entries return (nil, false) and are pruned
// immediately rather than waiting for the cleanup sweep.
func (c *Cache) Get(key string) (interface{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.entries[key]
	if !ok {
		return nil, false
	}

	ent := elem.Value.(*entry)
	if ent.isExpired() {
		c.removeElementLocked(elem)
		return nil, false
	}

	c.evictList.MoveToFront(elem)
	return ent.value, true
}

// Delete removes a value from the cache. No-op if absent.
func (c *Cache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.entries[key]; ok {
		c.removeElementLocked(elem)
	}
}

// DeletePrefix removes all entries with the given prefix.
func (c *Cache) DeletePrefix(prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Snapshot keys to remove so we don't mutate the map under iteration.
	var toRemove []string
	for key := range c.entries {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			toRemove = append(toRemove, key)
		}
	}
	for _, key := range toRemove {
		if elem, ok := c.entries[key]; ok {
			c.removeElementLocked(elem)
		}
	}
}

// Clear removes all entries from the cache. Does not stop the cleanup
// goroutine — call Stop() for that.
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]*list.Element)
	c.evictList.Init()
}

// Len returns the current number of cached entries (including any that
// have expired but have not yet been swept).
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.evictList.Len()
}

// Stop terminates the background cleanup goroutine. Calling Stop more
// than once is safe — subsequent calls are no-ops.
//
// Callers should invoke Stop when the cache is no longer needed (for
// example, from a containing struct's Close method). Otherwise the
// cleanup goroutine outlives the cache and leaks for the rest of the
// process lifetime.
//
// Stop is non-blocking. To wait for the cleanup goroutine to actually
// exit (e.g. in tests or strict shutdown ordering), use `<-c.Done()`.
func (c *Cache) Stop() {
	c.stopOnce.Do(func() {
		close(c.stop)
	})
}

// Done returns a channel that is closed when the cleanup goroutine has
// exited. Useful for deterministic test shutdown — `<-cache.Done()`
// returns immediately once the cleanup loop has acknowledged a Stop().
func (c *Cache) Done() <-chan struct{} {
	return c.done
}

// evictOldestLocked drops the LRU entry. Caller must hold c.mu.
func (c *Cache) evictOldestLocked() {
	elem := c.evictList.Back()
	if elem == nil {
		return
	}
	c.removeElementLocked(elem)
}

// removeElementLocked removes a list element from both the eviction list
// and the entries map. Caller must hold c.mu.
func (c *Cache) removeElementLocked(elem *list.Element) {
	ent := elem.Value.(*entry)
	c.evictList.Remove(elem)
	delete(c.entries, ent.key)
}

// cleanup periodically removes expired entries. Exits when Stop() closes
// c.stop. Closes c.done as the last thing it does so callers can wait
// deterministically via Done() rather than poll runtime.NumGoroutine.
func (c *Cache) cleanup() {
	defer close(c.done)

	ticker := time.NewTicker(c.ttl)
	defer ticker.Stop()

	for {
		select {
		case <-c.stop:
			return
		case <-ticker.C:
			c.sweepExpired()
		}
	}
}

// sweepExpired walks the eviction list once and removes anything past
// its expiration. Walking front-to-back is fine because TTL is uniform —
// any traversal order touches every entry.
func (c *Cache) sweepExpired() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	// Iterate via map keys to avoid mutating list while walking it.
	var stale []*list.Element
	for _, elem := range c.entries {
		if now.After(elem.Value.(*entry).expiration) {
			stale = append(stale, elem)
		}
	}
	for _, elem := range stale {
		c.removeElementLocked(elem)
	}
}

// MetadataCache caches file metadata.
type MetadataCache struct {
	cache *Cache
}

// NewMetadataCache creates a new metadata cache. Pass cache.WithMaxEntries
// to bound it.
func NewMetadataCache(ttl time.Duration, opts ...Option) *MetadataCache {
	return &MetadataCache{
		cache: NewCache(ttl, opts...),
	}
}

// Get retrieves file info from cache.
func (mc *MetadataCache) Get(path string) (*agfs.FileInfo, bool) {
	value, ok := mc.cache.Get(path)
	if !ok {
		return nil, false
	}
	info, ok := value.(*agfs.FileInfo)
	return info, ok
}

// Set stores file info in cache.
func (mc *MetadataCache) Set(path string, info *agfs.FileInfo) {
	mc.cache.Set(path, info)
}

// Invalidate removes file info from cache.
func (mc *MetadataCache) Invalidate(path string) {
	mc.cache.Delete(path)
}

// InvalidatePrefix invalidates all paths with the given prefix.
func (mc *MetadataCache) InvalidatePrefix(prefix string) {
	mc.cache.DeletePrefix(prefix)
}

// Clear clears all cached metadata.
func (mc *MetadataCache) Clear() {
	mc.cache.Clear()
}

// Stop terminates the background cleanup goroutine. Idempotent.
func (mc *MetadataCache) Stop() {
	mc.cache.Stop()
}

// DirectoryCache caches directory listings.
type DirectoryCache struct {
	cache *Cache
}

// NewDirectoryCache creates a new directory cache. Pass cache.WithMaxEntries
// to bound it.
func NewDirectoryCache(ttl time.Duration, opts ...Option) *DirectoryCache {
	return &DirectoryCache{
		cache: NewCache(ttl, opts...),
	}
}

// Get retrieves directory listing from cache.
func (dc *DirectoryCache) Get(path string) ([]agfs.FileInfo, bool) {
	value, ok := dc.cache.Get(path)
	if !ok {
		return nil, false
	}
	files, ok := value.([]agfs.FileInfo)
	return files, ok
}

// Set stores directory listing in cache.
func (dc *DirectoryCache) Set(path string, files []agfs.FileInfo) {
	dc.cache.Set(path, files)
}

// Invalidate removes directory listing from cache.
func (dc *DirectoryCache) Invalidate(path string) {
	dc.cache.Delete(path)
}

// InvalidatePrefix invalidates all directories with the given prefix.
func (dc *DirectoryCache) InvalidatePrefix(prefix string) {
	dc.cache.DeletePrefix(prefix)
}

// Clear clears all cached directories.
func (dc *DirectoryCache) Clear() {
	dc.cache.Clear()
}

// Stop terminates the background cleanup goroutine. Idempotent.
func (dc *DirectoryCache) Stop() {
	dc.cache.Stop()
}
