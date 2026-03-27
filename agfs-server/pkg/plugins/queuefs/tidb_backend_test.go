package queuefs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func tidbTestConfig(t *testing.T, database string) map[string]interface{} {
	t.Helper()

	if os.Getenv("TIDB_TEST") == "" {
		t.Skip("set TIDB_TEST=1 to run TiDB integration tests")
	}

	dsn := os.Getenv("TIDB_TEST_DSN")
	if dsn == "" {
		t.Skip("set TIDB_TEST_DSN to run TiDB integration tests")
	}

	parsedDSN, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse TIDB_TEST_DSN: %v", err)
	}
	parsedDSN.DBName = database
	if parsedDSN.Params == nil {
		parsedDSN.Params = map[string]string{}
	}

	return map[string]interface{}{
		"backend":  "tidb",
		"dsn":      parsedDSN.FormatDSN(),
		"database": database,
	}
}

func newTiDBTestQueueFS(t *testing.T, database string) *queueFS {
	t.Helper()

	plugin := NewQueueFSPlugin()
	if err := plugin.Initialize(tidbTestConfig(t, database)); err != nil {
		t.Fatalf("initialize tidb queuefs: %v", err)
	}
	t.Cleanup(func() {
		if plugin.backend != nil {
			_ = plugin.backend.Close()
		}
	})

	fs, ok := plugin.GetFileSystem().(*queueFS)
	if !ok {
		t.Fatalf("unexpected filesystem type %T", plugin.GetFileSystem())
	}
	return fs
}

func newTiDBTestDatabaseName(t *testing.T) string {
	t.Helper()

	return fmt.Sprintf("queuefs_test_%d", time.Now().UnixNano())
}

func TestQueueFSTiDBFileRegression(t *testing.T) {
	database := newTiDBTestDatabaseName(t)
	fs := newTiDBTestQueueFS(t, database)

	if err := fs.Mkdir("/jobs", 0o755); err != nil {
		t.Fatalf("mkdir /jobs: %v", err)
	}
	if err := fs.Mkdir("/logs/errors", 0o755); err != nil {
		t.Fatalf("mkdir /logs/errors: %v", err)
	}

	entries, err := fs.ReadDir("/")
	if err != nil {
		t.Fatalf("readdir root: %v", err)
	}
	rootEntries := queueDirEntryNames(entries)
	for _, name := range []string{"README", "jobs", "logs"} {
		if _, ok := rootEntries[name]; !ok {
			t.Fatalf("root missing %q in %+v", name, entries)
		}
	}

	if _, err := fs.Write("/jobs/enqueue", []byte("first"), -1, 0); err != nil {
		t.Fatalf("enqueue first: %v", err)
	}
	if _, err := fs.Write("/jobs/enqueue", []byte("second"), -1, 0); err != nil {
		t.Fatalf("enqueue second: %v", err)
	}

	if got := string(mustReadAll(t, fs, "/jobs/size")); got != "2" {
		t.Fatalf("queue size = %q, want 2", got)
	}

	peeked := mustReadMessage(t, fs, "/jobs/peek")
	if peeked.Data != "first" {
		t.Fatalf("peeked message = %q, want first", peeked.Data)
	}

	first := mustReadMessage(t, fs, "/jobs/dequeue")
	second := mustReadMessage(t, fs, "/jobs/dequeue")
	if first.Data != "first" || second.Data != "second" {
		t.Fatalf("dequeue order = [%q, %q], want [first, second]", first.Data, second.Data)
	}

	if got := string(mustReadAll(t, fs, "/jobs/dequeue")); got != "{}" {
		t.Fatalf("empty dequeue = %q, want {}", got)
	}

	if _, err := fs.Write("/jobs/enqueue", []byte("to-clear"), -1, 0); err != nil {
		t.Fatalf("enqueue before clear: %v", err)
	}
	if _, err := fs.Write("/jobs/clear", nil, -1, 0); err != nil {
		t.Fatalf("clear queue: %v", err)
	}
	if got := string(mustReadAll(t, fs, "/jobs/size")); got != "0" {
		t.Fatalf("queue size after clear = %q, want 0", got)
	}

	if err := fs.RemoveAll("/logs"); err != nil {
		t.Fatalf("removeall /logs: %v", err)
	}
	if _, err := fs.Stat("/logs/errors"); err == nil {
		t.Fatal("expected removed nested tidb queue to disappear")
	}
}

func TestQueueFSTiDBPersistenceRegression(t *testing.T) {
	database := newTiDBTestDatabaseName(t)

	func() {
		fs := newTiDBTestQueueFS(t, database)
		if err := fs.Mkdir("/jobs", 0o755); err != nil {
			t.Fatalf("mkdir /jobs: %v", err)
		}
		if _, err := fs.Write("/jobs/enqueue", []byte("persisted"), -1, 0); err != nil {
			t.Fatalf("enqueue persisted message: %v", err)
		}
		if got := string(mustReadAll(t, fs, "/jobs/size")); got != "1" {
			t.Fatalf("initial queue size = %q, want 1", got)
		}
	}()

	fs := newTiDBTestQueueFS(t, database)

	entries, err := fs.ReadDir("/")
	if err != nil {
		t.Fatalf("readdir root after reopen: %v", err)
	}
	if _, ok := queueDirEntryNames(entries)["jobs"]; !ok {
		t.Fatalf("root missing reopened queue in %+v", entries)
	}

	if got := string(mustReadAll(t, fs, "/jobs/size")); got != "1" {
		t.Fatalf("reopened queue size = %q, want 1", got)
	}
	peeked := mustReadMessage(t, fs, "/jobs/peek")
	if peeked.Data != "persisted" {
		t.Fatalf("peek after reopen = %q, want persisted", peeked.Data)
	}

	dequeued := mustReadMessage(t, fs, "/jobs/dequeue")
	if dequeued.Data != "persisted" {
		t.Fatalf("dequeue after reopen = %q, want persisted", dequeued.Data)
	}
	if got := string(mustReadAll(t, fs, "/jobs/size")); got != "0" {
		t.Fatalf("queue size after reopened dequeue = %q, want 0", got)
	}

	if _, err := fs.Stat("/jobs"); err != nil {
		t.Fatalf("stat empty queue after reopen: %v", err)
	}
}

func TestQueueFSTiDBConfigUsesDSN(t *testing.T) {
	if os.Getenv("TIDB_TEST") == "" {
		t.Skip("set TIDB_TEST=1 to run TiDB integration tests")
	}

	config := tidbTestConfig(t, newTiDBTestDatabaseName(t))
	if _, ok := config["dsn"].(string); !ok || config["dsn"] == "" {
		t.Fatalf("expected non-empty dsn in config: %+v", config)
	}
}

func TestQueueFSTiDBConcurrentDequeueRegression(t *testing.T) {
	database := newTiDBTestDatabaseName(t)
	writerFS := newTiDBTestQueueFS(t, database)
	readerOne := newTiDBTestQueueFS(t, database)
	readerTwo := newTiDBTestQueueFS(t, database)

	if err := writerFS.Mkdir("/jobs", 0o755); err != nil {
		t.Fatalf("mkdir /jobs: %v", err)
	}
	if _, err := writerFS.Write("/jobs/enqueue", []byte("once"), -1, 0); err != nil {
		t.Fatalf("enqueue once: %v", err)
	}

	type dequeueResult struct {
		payload []byte
		err     error
	}

	start := make(chan struct{})
	results := make(chan dequeueResult, 2)
	var wg sync.WaitGroup
	for _, fs := range []*queueFS{readerOne, readerTwo} {
		wg.Add(1)
		go func(fs *queueFS) {
			defer wg.Done()
			<-start
			payload, err := fs.Read("/jobs/dequeue", 0, -1)
			results <- dequeueResult{payload: payload, err: err}
		}(fs)
	}
	close(start)
	wg.Wait()
	close(results)

	nonEmpty := 0
	for result := range results {
		if result.err != nil && !errors.Is(result.err, io.EOF) {
			t.Fatalf("concurrent dequeue: %v", result.err)
		}
		if string(result.payload) == "{}" {
			continue
		}

		var msg QueueMessage
		if err := json.Unmarshal(result.payload, &msg); err != nil {
			t.Fatalf("unmarshal concurrent dequeue payload: %v (payload=%q)", err, string(result.payload))
		}
		if msg.Data != "once" {
			t.Fatalf("concurrent dequeue returned %q, want once", msg.Data)
		}
		nonEmpty++
	}

	if nonEmpty != 1 {
		t.Fatalf("concurrent dequeue claimed %d messages, want 1", nonEmpty)
	}
	if got := string(mustReadAll(t, writerFS, "/jobs/size")); got != "0" {
		t.Fatalf("queue size after concurrent dequeue = %q, want 0", got)
	}
}
