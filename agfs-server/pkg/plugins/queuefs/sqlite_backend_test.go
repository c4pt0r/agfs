package queuefs

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/c4pt0r/agfs/agfs-server/pkg/filesystem"
)

func newSQLiteQueueFSTest(t *testing.T) filesystem.FileSystem {
	t.Helper()

	plugin := NewQueueFSPlugin()
	if err := plugin.Initialize(map[string]interface{}{
		"backend": "sqlite",
		"db_path": filepath.Join(t.TempDir(), "queuefs.db"),
	}); err != nil {
		t.Fatalf("initialize sqlite queuefs: %v", err)
	}
	t.Cleanup(func() {
		if plugin.backend != nil {
			_ = plugin.backend.Close()
		}
	})

	return plugin.GetFileSystem()
}

func newSQLiteQueueBackendTest(t *testing.T) *TiDBBackend {
	t.Helper()

	plugin := NewQueueFSPlugin()
	if err := plugin.Initialize(map[string]interface{}{
		"backend": "sqlite",
		"db_path": filepath.Join(t.TempDir(), "queuefs.db"),
	}); err != nil {
		t.Fatalf("initialize sqlite queuefs: %v", err)
	}
	t.Cleanup(func() {
		if plugin.backend != nil {
			_ = plugin.backend.Close()
		}
	})

	backend, ok := plugin.backend.(*TiDBBackend)
	if !ok {
		t.Fatalf("unexpected backend type %T", plugin.backend)
	}
	return backend
}

func readQueueMessage(t *testing.T, fs filesystem.FileSystem, path string) QueueMessage {
	t.Helper()

	data, err := fs.Read(path, 0, -1)
	if err != nil && err != io.EOF {
		t.Fatalf("read %s: %v", path, err)
	}

	var msg QueueMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("decode %s response %q: %v", path, string(data), err)
	}
	return msg
}

func readString(t *testing.T, fs filesystem.FileSystem, path string) string {
	t.Helper()

	data, err := fs.Read(path, 0, -1)
	if err != nil && err != io.EOF {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func writeQueueMessage(t *testing.T, fs filesystem.FileSystem, path, value string) {
	t.Helper()

	if _, err := fs.Write(path, []byte(value), -1, filesystem.WriteFlagCreate); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readDirNames(t *testing.T, fs filesystem.FileSystem, path string) []string {
	t.Helper()

	entries, err := fs.ReadDir(path)
	if err != nil {
		t.Fatalf("readdir %s: %v", path, err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	sort.Strings(names)
	return names
}

func assertNames(t *testing.T, got []string, want ...string) {
	t.Helper()

	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("names = %v, want %v", got, want)
		}
	}
}

func TestQueueFSSQLiteBasicOperations(t *testing.T) {
	fs := newSQLiteQueueFSTest(t)

	if err := fs.Mkdir("/jobs", 0755); err != nil {
		t.Fatalf("mkdir /jobs: %v", err)
	}
	if _, err := fs.Stat("/jobs"); err != nil {
		t.Fatalf("stat /jobs: %v", err)
	}

	writeQueueMessage(t, fs, "/jobs/enqueue", "first")
	writeQueueMessage(t, fs, "/jobs/enqueue", "second")

	if got := readString(t, fs, "/jobs/size"); got != "2" {
		t.Fatalf("size after enqueue = %q, want 2", got)
	}

	if got := readQueueMessage(t, fs, "/jobs/peek"); got.Data != "first" {
		t.Fatalf("peek data = %q, want first", got.Data)
	}
	if got := readString(t, fs, "/jobs/size"); got != "2" {
		t.Fatalf("size after peek = %q, want 2", got)
	}

	if got := readQueueMessage(t, fs, "/jobs/dequeue"); got.Data != "first" {
		t.Fatalf("first dequeue data = %q, want first", got.Data)
	}
	if got := readString(t, fs, "/jobs/size"); got != "1" {
		t.Fatalf("size after first dequeue = %q, want 1", got)
	}
	if got := readQueueMessage(t, fs, "/jobs/dequeue"); got.Data != "second" {
		t.Fatalf("second dequeue data = %q, want second", got.Data)
	}
	if got := readString(t, fs, "/jobs/size"); got != "0" {
		t.Fatalf("size after second dequeue = %q, want 0", got)
	}
	if got := readString(t, fs, "/jobs/dequeue"); got != "{}" {
		t.Fatalf("empty dequeue = %q, want {}", got)
	}

	writeQueueMessage(t, fs, "/jobs/enqueue", "third")
	writeQueueMessage(t, fs, "/jobs/enqueue", "fourth")
	if _, err := fs.Write("/jobs/clear", nil, -1, 0); err != nil {
		t.Fatalf("clear /jobs: %v", err)
	}
	if got := readString(t, fs, "/jobs/size"); got != "0" {
		t.Fatalf("size after clear = %q, want 0", got)
	}
}

func TestQueueFSSQLiteNestedQueuesAndRemoveQueue(t *testing.T) {
	fs := newSQLiteQueueFSTest(t)

	for _, dir := range []string{"/jobs", "/jobs/high", "/jobs/low", "/logs"} {
		if err := fs.Mkdir(dir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	writeQueueMessage(t, fs, "/jobs/high/enqueue", "urgent")
	writeQueueMessage(t, fs, "/jobs/low/enqueue", "background")
	writeQueueMessage(t, fs, "/logs/enqueue", "audit")

	assertNames(t, readDirNames(t, fs, "/"), "README", "jobs", "logs")
	assertNames(t, readDirNames(t, fs, "/jobs"), "high", "low")

	if err := fs.RemoveAll("/jobs/high"); err != nil {
		t.Fatalf("remove /jobs/high: %v", err)
	}
	if _, err := fs.Stat("/jobs/high"); err == nil {
		t.Fatal("stat /jobs/high succeeded after removal")
	}
	assertNames(t, readDirNames(t, fs, "/jobs"), "low")

	if err := fs.RemoveAll("/jobs"); err != nil {
		t.Fatalf("remove /jobs: %v", err)
	}
	if _, err := fs.Stat("/jobs"); err == nil {
		t.Fatal("stat /jobs succeeded after subtree removal")
	}
	if _, err := fs.Stat("/logs"); err != nil {
		t.Fatalf("stat /logs after /jobs removal: %v", err)
	}

	if err := fs.RemoveAll("/"); err != nil {
		t.Fatalf("remove root queues: %v", err)
	}
	assertNames(t, readDirNames(t, fs, "/"), "README")
}

func TestQueueFSSQLiteRequiresQueueCreation(t *testing.T) {
	fs := newSQLiteQueueFSTest(t)

	if _, err := fs.Write("/missing/enqueue", []byte("no queue"), -1, filesystem.WriteFlagCreate); err == nil {
		t.Fatal("write to missing sqlite queue succeeded")
	}
}

func TestQueueFSSQLiteConcurrentDequeueNoDuplicateDelivery(t *testing.T) {
	backend := newSQLiteQueueBackendTest(t)
	const queueName = "jobs"
	const total = 64

	if err := backend.CreateQueue(queueName); err != nil {
		t.Fatalf("create queue: %v", err)
	}

	want := make(map[string]struct{}, total)
	for i := 0; i < total; i++ {
		data := fmt.Sprintf("job-%02d", i)
		want[data] = struct{}{}
		if err := backend.Enqueue(queueName, QueueMessage{
			ID:        fmt.Sprintf("msg-%02d", i),
			Data:      data,
			Timestamp: time.Unix(int64(i+1), 0),
		}); err != nil {
			t.Fatalf("enqueue %s: %v", data, err)
		}
	}

	start := make(chan struct{})
	results := make(chan QueueMessage, total)
	errs := make(chan error, total)

	var wg sync.WaitGroup
	wg.Add(total)
	for i := 0; i < total; i++ {
		go func() {
			defer wg.Done()
			<-start
			msg, found, err := backend.Dequeue(queueName)
			if err != nil {
				errs <- err
				return
			}
			if !found {
				errs <- fmt.Errorf("concurrent dequeue returned empty queue before all messages were delivered")
				return
			}
			results <- msg
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		t.Errorf("concurrent dequeue error: %v", err)
	}
	if t.Failed() {
		t.FailNow()
	}

	got := make(map[string]int, total)
	for msg := range results {
		got[msg.Data]++
	}
	if len(got) != total {
		t.Fatalf("unique dequeued messages = %d, want %d; got=%v", len(got), total, got)
	}
	for data := range want {
		if got[data] != 1 {
			t.Fatalf("message %q delivered %d times, want once; got=%v", data, got[data], got)
		}
	}

	if size, err := backend.Size(queueName); err != nil {
		t.Fatalf("size after concurrent dequeue: %v", err)
	} else if size != 0 {
		t.Fatalf("size after concurrent dequeue = %d, want 0", size)
	}
	if msg, found, err := backend.Dequeue(queueName); err != nil {
		t.Fatalf("final empty dequeue: %v", err)
	} else if found {
		t.Fatalf("final empty dequeue returned %+v", msg)
	}
}
