package vectorfs

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// ============================================================================
// Unit Tests for Batch Insert Optimization
// ============================================================================

func TestChunkDataStructure(t *testing.T) {
	// Test that ChunkData struct is properly defined
	chunk := ChunkData{
		ChunkIndex: 0,
		ChunkText:  "test text",
		Embedding:  []float32{0.1, 0.2, 0.3},
	}

	if chunk.ChunkIndex != 0 {
		t.Errorf("ChunkIndex mismatch: got %d, want 0", chunk.ChunkIndex)
	}
	if chunk.ChunkText != "test text" {
		t.Errorf("ChunkText mismatch: got %s, want 'test text'", chunk.ChunkText)
	}
	if len(chunk.Embedding) != 3 {
		t.Errorf("Embedding length mismatch: got %d, want 3", len(chunk.Embedding))
	}
}

func TestFormatVector(t *testing.T) {
	tests := []struct {
		name     string
		input    []float32
		expected string
	}{
		{
			name:     "empty vector",
			input:    []float32{},
			expected: "[]",
		},
		{
			name:     "single element",
			input:    []float32{1.0},
			expected: "[1.000000]",
		},
		{
			name:     "multiple elements",
			input:    []float32{0.1, 0.2, 0.3},
			expected: "[0.100000,0.200000,0.300000]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatVector(tt.input)
			if result != tt.expected {
				t.Errorf("formatVector(%v) = %s, want %s", tt.input, result, tt.expected)
			}
		})
	}
}

func TestSanitizeTableName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"test", "test"},
		{"test-namespace", "test_namespace"},
		{"test.name", "test_name"},
		{"test/path", "test_path"},
		{"test name", "test_name"},
		{"test-name.space/path", "test_name_space_path"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := sanitizeTableName(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeTableName(%s) = %s, want %s", tt.input, result, tt.expected)
			}
		})
	}
}

// ============================================================================
// Unit Tests for HTTP Client Timeout
// ============================================================================

func TestEmbeddingClientTimeout(t *testing.T) {
	// Create a slow server that delays response
	slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second) // Simulate slow response
		w.WriteHeader(http.StatusOK)
	}))
	defer slowServer.Close()

	// Create client with short timeout
	client := &http.Client{
		Timeout: 100 * time.Millisecond,
	}

	// Make request - should timeout
	_, err := client.Get(slowServer.URL)
	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "deadline exceeded") && !strings.Contains(err.Error(), "timeout") {
		t.Errorf("Expected timeout error, got: %v", err)
	}
}

func TestNewEmbeddingClientHasTimeout(t *testing.T) {
	cfg := EmbeddingConfig{
		Provider:  "openai",
		APIKey:    "test-key",
		Model:     "text-embedding-3-small",
		Dimension: 1536,
	}

	client, err := NewEmbeddingClient(cfg)
	if err != nil {
		t.Fatalf("NewEmbeddingClient failed: %v", err)
	}

	// Verify that the HTTP client has a timeout set
	if client.client.Timeout == 0 {
		t.Error("Expected HTTP client to have timeout set, got 0")
	}
	if client.client.Timeout != 60*time.Second {
		t.Errorf("Expected timeout of 60s, got %v", client.client.Timeout)
	}
}

// ============================================================================
// Unit Tests for Queue Overflow Handling
// ============================================================================

func TestIndexQueueNonBlocking(t *testing.T) {
	plugin := &VectorFSPlugin{
		indexQueue:     make(chan indexTask, 1), // Small buffer for testing
		shutdown:       make(chan struct{}),
		indexingStatus: make(map[string]map[string]*indexingFileInfo),
	}

	// Fill the queue
	plugin.indexQueue <- indexTask{namespace: "test", digest: "digest1", fileName: "file1"}

	// This should not block - it should use the goroutine fallback
	done := make(chan bool, 1)
	go func() {
		// Simulate Write behavior
		task := indexTask{namespace: "test", digest: "digest2", fileName: "file2"}
		plugin.addIndexingTask(task.namespace, task.digest, task.fileName)

		select {
		case plugin.indexQueue <- task:
			// Task queued successfully
		default:
			// Queue is full - use goroutine with shutdown awareness
			go func(t indexTask) {
				select {
				case plugin.indexQueue <- t:
				case <-plugin.shutdown:
					plugin.removeIndexingTask(t.namespace, t.digest)
				}
			}(task)
		}
		done <- true
	}()

	select {
	case <-done:
		// Write completed without blocking - success
	case <-time.After(100 * time.Millisecond):
		t.Error("Write blocked when queue was full")
	}
}

func TestIndexQueueShutdownAwareness(t *testing.T) {
	plugin := &VectorFSPlugin{
		indexQueue:     make(chan indexTask, 1),
		shutdown:       make(chan struct{}),
		indexingStatus: make(map[string]map[string]*indexingFileInfo),
	}

	// Fill the queue
	plugin.indexQueue <- indexTask{namespace: "test", digest: "digest1", fileName: "file1"}

	// Add a task that will overflow
	task := indexTask{namespace: "test", digest: "digest2", fileName: "file2"}
	plugin.addIndexingTask(task.namespace, task.digest, task.fileName)

	// Verify task is in indexing status
	status := plugin.getIndexingStatus("test")
	if !strings.Contains(status, "file2") {
		t.Error("Task should be in indexing status")
	}

	// Start overflow goroutine
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case plugin.indexQueue <- task:
			// Would block forever without shutdown
		case <-plugin.shutdown:
			plugin.removeIndexingTask(task.namespace, task.digest)
		}
	}()

	// Close shutdown channel to unblock the goroutine
	close(plugin.shutdown)

	// Wait for goroutine to finish
	done := make(chan bool)
	go func() {
		wg.Wait()
		done <- true
	}()

	select {
	case <-done:
		// Goroutine exited cleanly
	case <-time.After(time.Second):
		t.Error("Goroutine did not exit on shutdown")
	}

	// Verify task was removed from indexing status
	status = plugin.getIndexingStatus("test")
	if status != "idle" {
		t.Errorf("Expected 'idle' status after cleanup, got: %s", status)
	}
}

// ============================================================================
// Unit Tests for Path Parsing
// ============================================================================

func TestParsePath(t *testing.T) {
	tests := []struct {
		path              string
		expectedNamespace string
		expectedRelative  string
		expectError       bool
	}{
		{"/", "", "", false},
		{"/test", "test", "", false},
		{"/test/docs", "test", "docs", false},
		{"/test/docs/file.txt", "test", "docs/file.txt", false},
		{"/ns/docs/subdir/file.txt", "ns", "docs/subdir/file.txt", false},
		{"", "", "", false},
		{".", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			ns, rel, err := parsePath(tt.path)

			if tt.expectError && err == nil {
				t.Error("Expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if ns != tt.expectedNamespace {
				t.Errorf("namespace: got %q, want %q", ns, tt.expectedNamespace)
			}
			if rel != tt.expectedRelative {
				t.Errorf("relativePath: got %q, want %q", rel, tt.expectedRelative)
			}
		})
	}
}

// ============================================================================
// Unit Tests for Chunker
// ============================================================================

func TestChunkDocument(t *testing.T) {
	config := ChunkerConfig{
		ChunkSize:    100,
		ChunkOverlap: 10,
	}

	// Short text - should be single chunk
	shortText := "This is a short text."
	chunks := ChunkDocument(shortText, config)
	if len(chunks) != 1 {
		t.Errorf("Expected 1 chunk for short text, got %d", len(chunks))
	}

	// Longer text
	longText := strings.Repeat("This is a sentence. ", 50)
	chunks = ChunkDocument(longText, config)
	if len(chunks) < 2 {
		t.Errorf("Expected multiple chunks for long text, got %d", len(chunks))
	}

	// Verify chunk indices are sequential
	for i, chunk := range chunks {
		if chunk.Index != i {
			t.Errorf("Chunk %d has index %d", i, chunk.Index)
		}
	}
}

// ============================================================================
// Unit Tests for Indexing Status
// ============================================================================

func TestIndexingStatus(t *testing.T) {
	plugin := &VectorFSPlugin{
		indexingStatus: make(map[string]map[string]*indexingFileInfo),
	}

	// Initially idle
	status := plugin.getIndexingStatus("test-ns")
	if status != "idle" {
		t.Errorf("Expected 'idle', got %q", status)
	}

	// Add a task
	plugin.addIndexingTask("test-ns", "digest1", "file1.txt")
	status = plugin.getIndexingStatus("test-ns")
	if !strings.Contains(status, "indexing 1 file") {
		t.Errorf("Expected 'indexing 1 file', got %q", status)
	}
	if !strings.Contains(status, "file1.txt") {
		t.Errorf("Expected 'file1.txt' in status, got %q", status)
	}

	// Add another task
	plugin.addIndexingTask("test-ns", "digest2", "file2.txt")
	status = plugin.getIndexingStatus("test-ns")
	if !strings.Contains(status, "indexing 2 file") {
		t.Errorf("Expected 'indexing 2 files', got %q", status)
	}

	// Remove a task
	plugin.removeIndexingTask("test-ns", "digest1")
	status = plugin.getIndexingStatus("test-ns")
	if !strings.Contains(status, "indexing 1 file") {
		t.Errorf("Expected 'indexing 1 file' after removal, got %q", status)
	}

	// Remove last task
	plugin.removeIndexingTask("test-ns", "digest2")
	status = plugin.getIndexingStatus("test-ns")
	if status != "idle" {
		t.Errorf("Expected 'idle' after all removed, got %q", status)
	}
}

// ============================================================================
// Integration Tests (require database connection)
// ============================================================================

// TestTiDBBatchInsert tests the batch insert functionality
// Skip if no database connection available
func TestTiDBBatchInsert(t *testing.T) {
	dsn := getTestDSN()
	if dsn == "" {
		t.Skip("Skipping database test: TIDB_TEST_DSN not set")
	}

	client, err := NewTiDBClient(TiDBConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("Failed to connect to TiDB: %v", err)
	}
	defer client.Close()

	namespace := fmt.Sprintf("test_batch_%d", time.Now().UnixNano())

	// Create namespace
	err = client.CreateNamespace(namespace, 3)
	if err != nil {
		t.Fatalf("Failed to create namespace: %v", err)
	}
	defer client.DeleteNamespace(namespace)

	// Insert metadata first
	meta := FileMetadata{
		FileDigest: "test-digest",
		FileName:   "test.txt",
		S3Key:      "test/key",
		FileSize:   100,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	err = client.InsertFileMetadata(namespace, meta)
	if err != nil {
		t.Fatalf("Failed to insert metadata: %v", err)
	}

	// Test batch insert
	chunks := []ChunkData{
		{ChunkIndex: 0, ChunkText: "chunk 0", Embedding: []float32{0.1, 0.2, 0.3}},
		{ChunkIndex: 1, ChunkText: "chunk 1", Embedding: []float32{0.4, 0.5, 0.6}},
		{ChunkIndex: 2, ChunkText: "chunk 2", Embedding: []float32{0.7, 0.8, 0.9}},
	}

	err = client.InsertChunksBatch(namespace, "test-digest", chunks)
	if err != nil {
		t.Fatalf("Batch insert failed: %v", err)
	}

	// Verify chunks were inserted
	tableSuffix := sanitizeTableName(namespace)
	chunksTable := fmt.Sprintf("tbl_chunks_%s", tableSuffix)

	var count int
	err = client.db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE file_digest = ?", chunksTable), "test-digest").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count chunks: %v", err)
	}
	if count != 3 {
		t.Errorf("Expected 3 chunks, got %d", count)
	}
}

// TestTiDBListFilesWithPrefix tests prefix-filtered file listing
func TestTiDBListFilesWithPrefix(t *testing.T) {
	dsn := getTestDSN()
	if dsn == "" {
		t.Skip("Skipping database test: TIDB_TEST_DSN not set")
	}

	client, err := NewTiDBClient(TiDBConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("Failed to connect to TiDB: %v", err)
	}
	defer client.Close()

	namespace := fmt.Sprintf("test_prefix_%d", time.Now().UnixNano())

	// Create namespace
	err = client.CreateNamespace(namespace, 3)
	if err != nil {
		t.Fatalf("Failed to create namespace: %v", err)
	}
	defer client.DeleteNamespace(namespace)

	// Insert test files
	now := time.Now()
	files := []FileMetadata{
		{FileDigest: "d1", FileName: "dir1/file1.txt", S3Key: "k1", FileSize: 100, CreatedAt: now, UpdatedAt: now},
		{FileDigest: "d2", FileName: "dir1/file2.txt", S3Key: "k2", FileSize: 100, CreatedAt: now, UpdatedAt: now},
		{FileDigest: "d3", FileName: "dir1/subdir/file3.txt", S3Key: "k3", FileSize: 100, CreatedAt: now, UpdatedAt: now},
		{FileDigest: "d4", FileName: "dir2/file4.txt", S3Key: "k4", FileSize: 100, CreatedAt: now, UpdatedAt: now},
		{FileDigest: "d5", FileName: "root.txt", S3Key: "k5", FileSize: 100, CreatedAt: now, UpdatedAt: now},
	}

	for _, f := range files {
		err = client.InsertFileMetadata(namespace, f)
		if err != nil {
			t.Fatalf("Failed to insert file %s: %v", f.FileName, err)
		}
	}

	// Test ListFilesWithPrefix
	tests := []struct {
		prefix        string
		expectedCount int
	}{
		{"dir1/", 3},
		{"dir1/subdir/", 1},
		{"dir2/", 1},
		{"nonexistent/", 0},
	}

	for _, tt := range tests {
		t.Run(tt.prefix, func(t *testing.T) {
			result, err := client.ListFilesWithPrefix(namespace, tt.prefix)
			if err != nil {
				t.Fatalf("ListFilesWithPrefix failed: %v", err)
			}
			if len(result) != tt.expectedCount {
				t.Errorf("Expected %d files for prefix %q, got %d", tt.expectedCount, tt.prefix, len(result))
			}
		})
	}
}

// TestTiDBHasFilesWithPrefix tests the directory existence check
func TestTiDBHasFilesWithPrefix(t *testing.T) {
	dsn := getTestDSN()
	if dsn == "" {
		t.Skip("Skipping database test: TIDB_TEST_DSN not set")
	}

	client, err := NewTiDBClient(TiDBConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("Failed to connect to TiDB: %v", err)
	}
	defer client.Close()

	namespace := fmt.Sprintf("test_has_prefix_%d", time.Now().UnixNano())

	// Create namespace
	err = client.CreateNamespace(namespace, 3)
	if err != nil {
		t.Fatalf("Failed to create namespace: %v", err)
	}
	defer client.DeleteNamespace(namespace)

	// Insert test file
	now := time.Now()
	err = client.InsertFileMetadata(namespace, FileMetadata{
		FileDigest: "d1",
		FileName:   "subdir/file.txt",
		S3Key:      "k1",
		FileSize:   100,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		t.Fatalf("Failed to insert file: %v", err)
	}

	// Test HasFilesWithPrefix
	tests := []struct {
		prefix   string
		expected bool
	}{
		{"subdir/", true},
		{"subdir/file", true},
		{"nonexistent/", false},
		{"sub", true}, // "subdir/" starts with "sub"
	}

	for _, tt := range tests {
		t.Run(tt.prefix, func(t *testing.T) {
			result, err := client.HasFilesWithPrefix(namespace, tt.prefix)
			if err != nil {
				t.Fatalf("HasFilesWithPrefix failed: %v", err)
			}
			if result != tt.expected {
				t.Errorf("HasFilesWithPrefix(%q) = %v, want %v", tt.prefix, result, tt.expected)
			}
		})
	}
}

// TestTiDBEmptyBatchInsert tests batch insert with empty slice
func TestTiDBEmptyBatchInsert(t *testing.T) {
	dsn := getTestDSN()
	if dsn == "" {
		t.Skip("Skipping database test: TIDB_TEST_DSN not set")
	}

	client, err := NewTiDBClient(TiDBConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("Failed to connect to TiDB: %v", err)
	}
	defer client.Close()

	// Empty batch should succeed without error
	err = client.InsertChunksBatch("any-namespace", "any-digest", []ChunkData{})
	if err != nil {
		t.Errorf("Empty batch insert should succeed, got: %v", err)
	}
}

// TestTiDBLargeBatchInsert tests batch insert with more than batchSize items
func TestTiDBLargeBatchInsert(t *testing.T) {
	dsn := getTestDSN()
	if dsn == "" {
		t.Skip("Skipping database test: TIDB_TEST_DSN not set")
	}

	client, err := NewTiDBClient(TiDBConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("Failed to connect to TiDB: %v", err)
	}
	defer client.Close()

	namespace := fmt.Sprintf("test_large_batch_%d", time.Now().UnixNano())

	// Create namespace
	err = client.CreateNamespace(namespace, 3)
	if err != nil {
		t.Fatalf("Failed to create namespace: %v", err)
	}
	defer client.DeleteNamespace(namespace)

	// Insert metadata first
	meta := FileMetadata{
		FileDigest: "large-batch-digest",
		FileName:   "large.txt",
		S3Key:      "test/key",
		FileSize:   100,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	err = client.InsertFileMetadata(namespace, meta)
	if err != nil {
		t.Fatalf("Failed to insert metadata: %v", err)
	}

	// Create 120 chunks (more than batchSize of 50)
	chunks := make([]ChunkData, 120)
	for i := 0; i < 120; i++ {
		chunks[i] = ChunkData{
			ChunkIndex: i,
			ChunkText:  fmt.Sprintf("chunk %d", i),
			Embedding:  []float32{float32(i) * 0.1, float32(i) * 0.2, float32(i) * 0.3},
		}
	}

	err = client.InsertChunksBatch(namespace, "large-batch-digest", chunks)
	if err != nil {
		t.Fatalf("Large batch insert failed: %v", err)
	}

	// Verify all chunks were inserted
	tableSuffix := sanitizeTableName(namespace)
	chunksTable := fmt.Sprintf("tbl_chunks_%s", tableSuffix)

	var count int
	err = client.db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE file_digest = ?", chunksTable), "large-batch-digest").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count chunks: %v", err)
	}
	if count != 120 {
		t.Errorf("Expected 120 chunks, got %d", count)
	}
}

// getTestDSN returns the test database DSN from environment
func getTestDSN() string {
	// Set TIDB_TEST_DSN environment variable for integration testing
	// Example: export TIDB_TEST_DSN="user:pass@tcp(localhost:4000)/test?parseTime=true"
	return os.Getenv("TIDB_TEST_DSN")
}
