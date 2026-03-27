package queuefs

import (
	"path/filepath"
	"testing"
)

func newSQLiteTestQueueFS(t *testing.T, dbPath string) *queueFS {
	t.Helper()

	plugin := NewQueueFSPlugin()
	if err := plugin.Initialize(map[string]interface{}{
		"backend": "sqlite",
		"db_path": dbPath,
	}); err != nil {
		t.Fatalf("initialize sqlite queuefs: %v", err)
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

func TestQueueFSSQLiteFileRegression(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "queuefs.db")
	fs := newSQLiteTestQueueFS(t, dbPath)

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
		t.Fatal("expected removed nested sqlite queue to disappear")
	}
}

func TestQueueFSSQLitePersistenceRegression(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "queuefs-persist.db")

	func() {
		fs := newSQLiteTestQueueFS(t, dbPath)
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

	fs := newSQLiteTestQueueFS(t, dbPath)

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
