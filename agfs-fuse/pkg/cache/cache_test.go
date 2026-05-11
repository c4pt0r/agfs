package cache

import (
	"sync"
	"testing"
	"time"

	agfs "github.com/c4pt0r/agfs/agfs-sdk/go"
)

func TestCacheBasicOperations(t *testing.T) {
	c := NewCache(100 * time.Millisecond)

	// Test Set and Get
	c.Set("key1", "value1")
	value, ok := c.Get("key1")
	if !ok || value != "value1" {
		t.Errorf("Expected value1, got %v (ok=%v)", value, ok)
	}

	// Test Get non-existent key
	_, ok = c.Get("key2")
	if ok {
		t.Error("Expected key2 to not exist")
	}

	// Test Delete
	c.Delete("key1")
	_, ok = c.Get("key1")
	if ok {
		t.Error("Expected key1 to be deleted")
	}
}

func TestCacheTTL(t *testing.T) {
	c := NewCache(50 * time.Millisecond)

	c.Set("key1", "value1")

	// Should be available immediately
	_, ok := c.Get("key1")
	if !ok {
		t.Error("Expected key1 to exist")
	}

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	// Should be expired
	_, ok = c.Get("key1")
	if ok {
		t.Error("Expected key1 to be expired")
	}
}

func TestCacheDeletePrefix(t *testing.T) {
	c := NewCache(1 * time.Second)

	c.Set("/foo/bar", "1")
	c.Set("/foo/baz", "2")
	c.Set("/bar/qux", "3")

	c.DeletePrefix("/foo")

	// /foo/* should be deleted
	_, ok := c.Get("/foo/bar")
	if ok {
		t.Error("Expected /foo/bar to be deleted")
	}
	_, ok = c.Get("/foo/baz")
	if ok {
		t.Error("Expected /foo/baz to be deleted")
	}

	// /bar/qux should still exist
	_, ok = c.Get("/bar/qux")
	if !ok {
		t.Error("Expected /bar/qux to exist")
	}
}

func TestMetadataCache(t *testing.T) {
	mc := NewMetadataCache(1 * time.Second)

	info := &agfs.FileInfo{
		Name:  "test.txt",
		Size:  123,
		IsDir: false,
	}

	// Test Set and Get
	mc.Set("/test.txt", info)
	cached, ok := mc.Get("/test.txt")
	if !ok || cached.Name != "test.txt" || cached.Size != 123 {
		t.Errorf("Expected cached info to match, got %+v (ok=%v)", cached, ok)
	}

	// Test Invalidate
	mc.Invalidate("/test.txt")
	_, ok = mc.Get("/test.txt")
	if ok {
		t.Error("Expected /test.txt to be invalidated")
	}
}

func TestDirectoryCache(t *testing.T) {
	dc := NewDirectoryCache(1 * time.Second)

	files := []agfs.FileInfo{
		{Name: "file1.txt", Size: 100, IsDir: false},
		{Name: "file2.txt", Size: 200, IsDir: false},
	}

	// Test Set and Get
	dc.Set("/dir", files)
	cached, ok := dc.Get("/dir")
	if !ok || len(cached) != 2 {
		t.Errorf("Expected 2 cached files, got %d (ok=%v)", len(cached), ok)
	}

	// Test Invalidate
	dc.Invalidate("/dir")
	_, ok = dc.Get("/dir")
	if ok {
		t.Error("Expected /dir to be invalidated")
	}
}

func TestCacheConcurrency(t *testing.T) {
	c := NewCache(1 * time.Second)

	done := make(chan bool)

	// Writer goroutine
	go func() {
		for i := 0; i < 1000; i++ {
			c.Set("key", i)
		}
		done <- true
	}()

	// Reader goroutine
	go func() {
		for i := 0; i < 1000; i++ {
			c.Get("key")
		}
		done <- true
	}()

	// Wait for both to complete
	<-done
	<-done

	// If we got here without panic, concurrency is safe
}

// TestCacheLRUEviction verifies that bounding the cache via WithMaxEntries
// evicts the least-recently-used entry on overflow and that a Get on an
// older entry moves it back to the front of the eviction order.
func TestCacheLRUEviction(t *testing.T) {
	c := NewCache(1*time.Second, WithMaxEntries(2))
	defer c.Stop()

	c.Set("a", 1)
	c.Set("b", 2)

	// Touching "a" should make "b" the LRU.
	if _, ok := c.Get("a"); !ok {
		t.Fatal("expected a to be present")
	}

	// "c" forces eviction; "b" was LRU after touching "a".
	c.Set("c", 3)

	if _, ok := c.Get("b"); ok {
		t.Error("expected b to be evicted as LRU")
	}
	if _, ok := c.Get("a"); !ok {
		t.Error("expected a to survive eviction")
	}
	if _, ok := c.Get("c"); !ok {
		t.Error("expected c to be present")
	}
	if got, want := c.Len(), 2; got != want {
		t.Errorf("Len() = %d, want %d", got, want)
	}
}

// TestCacheLRUDoesNotEvictWithoutLimit confirms the legacy unbounded
// constructor path: callers who skip WithMaxEntries still see no
// eviction (only TTL expiry).
func TestCacheLRUDoesNotEvictWithoutLimit(t *testing.T) {
	c := NewCache(10 * time.Second)
	defer c.Stop()

	for i := 0; i < 100; i++ {
		c.Set(string(rune('a'+i%26))+string(rune('0'+i/26)), i)
	}
	if got, want := c.Len(), 100; got != want {
		t.Errorf("unbounded cache Len() = %d, want %d", got, want)
	}
}

// TestCacheUpdateInPlaceDoesNotEvict checks that updating an existing
// key does not count as a new insertion for capacity purposes — the
// fixed-size cache should never evict a still-present key just because
// it was rewritten.
func TestCacheUpdateInPlaceDoesNotEvict(t *testing.T) {
	c := NewCache(1*time.Second, WithMaxEntries(2))
	defer c.Stop()

	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("a", 99) // update in place, not a new entry

	if got, ok := c.Get("a"); !ok || got != 99 {
		t.Errorf("Get(a) = (%v, %v), want (99, true)", got, ok)
	}
	if _, ok := c.Get("b"); !ok {
		t.Error("b should still be present after a was updated in place")
	}
}

// TestCacheStopExitsCleanupGoroutine verifies the deterministic shutdown
// contract: after Stop, Done() closes promptly. Avoids
// runtime.NumGoroutine polling (timing-sensitive).
func TestCacheStopExitsCleanupGoroutine(t *testing.T) {
	c := NewCache(50 * time.Millisecond)

	c.Stop()

	select {
	case <-c.Done():
		// success — cleanup goroutine acknowledged the stop
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup goroutine did not exit within 2s of Stop()")
	}
}

// TestCacheStopIdempotent guarantees Stop can be called many times — and
// from multiple goroutines — without panic. Critical because containing
// structs (FUSE filesystem, etc.) may call Close multiple times during
// shutdown ordering.
func TestCacheStopIdempotent(t *testing.T) {
	c := NewCache(50 * time.Millisecond)

	const N = 8
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Stop()
		}()
	}
	wg.Wait()

	select {
	case <-c.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done did not close after concurrent Stop() calls")
	}
}

// TestCacheExpiredEntryReturnsNotFound verifies an expired-but-not-swept
// entry returns (nil, false) from Get and is pruned immediately — this
// matters because the sweeper runs on a ticker, so a Get between ticks
// must still observe the expiration boundary.
func TestCacheExpiredEntryReturnsNotFound(t *testing.T) {
	c := NewCache(10 * time.Millisecond)
	defer c.Stop()

	c.Set("k", "v")
	time.Sleep(50 * time.Millisecond)

	if _, ok := c.Get("k"); ok {
		t.Error("expected expired entry to be reported as not found")
	}
	if got := c.Len(); got != 0 {
		t.Errorf("expected expired entry to be pruned, Len() = %d", got)
	}
}

// TestMetadataCacheStop / TestDirectoryCacheStop verify that the wrapper
// types thread Stop through to the underlying Cache.
func TestMetadataCacheStop(t *testing.T) {
	mc := NewMetadataCache(50 * time.Millisecond)
	mc.Stop()
	select {
	case <-mc.cache.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("metadata cache cleanup goroutine did not exit")
	}
}

func TestDirectoryCacheStop(t *testing.T) {
	dc := NewDirectoryCache(50 * time.Millisecond)
	dc.Stop()
	select {
	case <-dc.cache.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("directory cache cleanup goroutine did not exit")
	}
}
