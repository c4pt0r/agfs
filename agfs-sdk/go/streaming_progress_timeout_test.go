package agfs

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestStreamingProgressTimeout_FiresOnStalledServer verifies that
// ReadStream surfaces an error when the server stops sending bytes for
// longer than the configured progress timeout. Without the timeout,
// the same scenario would hang indefinitely.
func TestStreamingProgressTimeout_FiresOnStalledServer(t *testing.T) {
	// Flush headers, then hold the body open until the request context
	// is canceled OR a long sentinel fires. Tying the hold to
	// r.Context().Done() lets httptest.Server.Close() finish promptly
	// when our progressReader cancels the request; the long sentinel
	// makes the test deterministic if cancellation ever regresses.
	const stall = 3 * time.Second
	stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
		case <-time.After(stall):
		}
	}))
	defer stalled.Close()

	client := NewClient(stalled.URL)
	client.SetStreamingProgressTimeout(300 * time.Millisecond)

	rc, err := client.ReadStream("/anything")
	if err != nil {
		t.Fatalf("ReadStream failed early: %v", err)
	}
	defer rc.Close()

	start := time.Now()
	buf := make([]byte, 1024)
	var readErr error
	for {
		_, readErr = rc.Read(buf)
		if readErr != nil {
			break
		}
	}
	elapsed := time.Since(start)

	if readErr == nil || readErr == io.EOF {
		t.Fatalf("expected non-EOF error from stalled stream, got %v", readErr)
	}
	// We expect the underlying connection to fail (context canceled
	// closes the conn). The exact error wording depends on the Go
	// version's net/http, so match on a coarse fingerprint.
	msg := readErr.Error()
	if !strings.Contains(msg, "context canceled") &&
		!strings.Contains(msg, "use of closed network connection") &&
		!strings.Contains(msg, "EOF") {
		t.Fatalf("unexpected error from stalled stream: %v", readErr)
	}
	// Sanity: we waited about the configured progress timeout, not the
	// server's full stall window.
	if elapsed >= stall {
		t.Fatalf("progress timeout did not fire — waited %s (stall was %s)", elapsed, stall)
	}
}

// TestStreamingProgressTimeout_PassesThroughWhenDisabled ensures the
// pre-2026-05 behavior (no progress bound) is preserved for callers
// who explicitly opt out via SetStreamingProgressTimeout(0).
func TestStreamingProgressTimeout_PassesThroughWhenDisabled(t *testing.T) {
	// Send headers, then a small payload after a deliberate gap. With
	// the timeout disabled, the gap should not fail the read.
	gap := 250 * time.Millisecond
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(gap)
		_, _ = w.Write([]byte("hello"))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	// Disable the progress timeout entirely.
	client.SetStreamingProgressTimeout(0)

	rc, err := client.ReadStream("/anything")
	if err != nil {
		t.Fatalf("ReadStream failed early: %v", err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll failed with progress-timeout disabled: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("ReadAll returned %q, want %q", data, "hello")
	}
}

// TestStreamingProgressTimeout_ProgressKeepsAlive shows that a stream
// making steady progress doesn't trip the per-chunk timeout even when
// each chunk takes longer than the timeout would have allowed if
// applied to the whole stream.
func TestStreamingProgressTimeout_ProgressKeepsAlive(t *testing.T) {
	chunkInterval := 200 * time.Millisecond
	chunks := 3

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("ResponseWriter does not implement http.Flusher")
		}
		for i := 0; i < chunks; i++ {
			time.Sleep(chunkInterval)
			_, _ = w.Write([]byte("chunk-"))
			flusher.Flush()
		}
	}))
	defer server.Close()

	client := NewClient(server.URL)
	// Per-chunk bound is generous relative to each gap but much tighter
	// than the cumulative stream duration.
	client.SetStreamingProgressTimeout(500 * time.Millisecond)

	rc, err := client.ReadStream("/anything")
	if err != nil {
		t.Fatalf("ReadStream failed early: %v", err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll failed mid-stream: %v", err)
	}
	want := strings.Repeat("chunk-", chunks)
	if string(data) != want {
		t.Fatalf("got %q, want %q", data, want)
	}
}

// TestStreamingProgressTimeout_DefaultIsSet pins the default to the
// declared constant, in case a future refactor accidentally drops the
// initialization in NewClient.
func TestStreamingProgressTimeout_DefaultIsSet(t *testing.T) {
	c := NewClient("http://example.invalid")
	if c.streamingProgressTimeout != DefaultStreamingProgressTimeout {
		t.Fatalf("default streamingProgressTimeout = %s, want %s",
			c.streamingProgressTimeout, DefaultStreamingProgressTimeout)
	}

	c2 := NewClientWithHTTPClient("http://example.invalid", &http.Client{})
	if c2.streamingProgressTimeout != DefaultStreamingProgressTimeout {
		t.Fatalf("NewClientWithHTTPClient default streamingProgressTimeout = %s, want %s",
			c2.streamingProgressTimeout, DefaultStreamingProgressTimeout)
	}
}
