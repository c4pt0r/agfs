package queuefs

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/c4pt0r/agfs/agfs-server/pkg/filesystem"
)

func mustReadAll(t *testing.T, fs filesystem.FileSystem, path string) []byte {
	t.Helper()

	data, err := fs.Read(path, 0, -1)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func newTestQueueFS(t *testing.T) *queueFS {
	t.Helper()

	plugin := NewQueueFSPlugin()
	if err := plugin.Initialize(map[string]interface{}{"backend": "memory"}); err != nil {
		t.Fatalf("initialize queuefs: %v", err)
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

func queueDirEntryNames(entries []filesystem.FileInfo) map[string]filesystem.FileInfo {
	byName := make(map[string]filesystem.FileInfo, len(entries))
	for _, entry := range entries {
		byName[entry.Name] = entry
	}
	return byName
}

func mustReadMessage(t *testing.T, fs filesystem.FileSystem, path string) QueueMessage {
	t.Helper()

	data, err := fs.Read(path, 0, -1)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("read %s: %v", path, err)
	}

	var msg QueueMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal %s: %v (payload=%q)", path, err, string(data))
	}
	return msg
}

func TestQueueFSRootAndNestedQueueRegression(t *testing.T) {
	fs := newTestQueueFS(t)

	rootInfo, err := fs.Stat("/")
	if err != nil {
		t.Fatalf("stat root: %v", err)
	}
	if !rootInfo.IsDir {
		t.Fatalf("root should be a directory")
	}
	if got := rootInfo.Meta.Content["backend"]; got != "memory" {
		t.Fatalf("root backend = %q, want memory", got)
	}

	readme := mustReadAll(t, fs, "/README")
	if !strings.Contains(string(readme), "QueueFS Plugin") {
		t.Fatalf("README missing plugin description: %q", string(readme))
	}

	entries, err := fs.ReadDir("/")
	if err != nil {
		t.Fatalf("readdir root: %v", err)
	}
	rootEntries := queueDirEntryNames(entries)
	if len(rootEntries) != 1 || rootEntries["README"].Name != "README" {
		t.Fatalf("unexpected initial root entries: %+v", entries)
	}

	if err := fs.Mkdir("/jobs", 0o755); err != nil {
		t.Fatalf("mkdir /jobs: %v", err)
	}
	if err := fs.Mkdir("/logs/errors", 0o755); err != nil {
		t.Fatalf("mkdir /logs/errors: %v", err)
	}

	entries, err = fs.ReadDir("/")
	if err != nil {
		t.Fatalf("readdir root after mkdir: %v", err)
	}
	rootEntries = queueDirEntryNames(entries)
	for _, name := range []string{"README", "jobs", "logs"} {
		if _, ok := rootEntries[name]; !ok {
			t.Fatalf("root missing %q in %+v", name, entries)
		}
	}
	if !rootEntries["jobs"].IsDir || !rootEntries["logs"].IsDir {
		t.Fatalf("expected queue directories at root: %+v", entries)
	}

	jobsEntries, err := fs.ReadDir("/jobs")
	if err != nil {
		t.Fatalf("readdir /jobs: %v", err)
	}
	got := queueDirEntryNames(jobsEntries)
	if len(got) != 5 {
		t.Fatalf("unexpected /jobs control files: %+v", jobsEntries)
	}
	for _, name := range []string{"enqueue", "dequeue", "peek", "size", "clear"} {
		if _, ok := got[name]; !ok {
			t.Fatalf("/jobs missing control file %q in %+v", name, jobsEntries)
		}
	}

	logsEntries, err := fs.ReadDir("/logs")
	if err != nil {
		t.Fatalf("readdir /logs: %v", err)
	}
	logChildren := queueDirEntryNames(logsEntries)
	if len(logChildren) != 1 || !logChildren["errors"].IsDir {
		t.Fatalf("unexpected /logs entries: %+v", logsEntries)
	}

	errorsInfo, err := fs.Stat("/logs/errors")
	if err != nil {
		t.Fatalf("stat /logs/errors: %v", err)
	}
	if !errorsInfo.IsDir {
		t.Fatalf("/logs/errors should be a directory")
	}

	if err := fs.RemoveAll("/logs"); err != nil {
		t.Fatalf("removeall /logs: %v", err)
	}
	if _, err := fs.Stat("/logs"); err == nil || !strings.Contains(err.Error(), "no such file or directory") {
		t.Fatalf("stat removed /logs error = %v, want missing path", err)
	}
	if _, err := fs.Stat("/logs/errors"); err == nil || !strings.Contains(err.Error(), "no such file or directory") {
		t.Fatalf("stat removed /logs/errors error = %v, want missing path", err)
	}
}

func TestQueueFSControlFileRegression(t *testing.T) {
	fs := newTestQueueFS(t)

	if err := fs.Mkdir("/jobs", 0o755); err != nil {
		t.Fatalf("mkdir /jobs: %v", err)
	}

	if _, err := fs.Write("/jobs/enqueue", []byte("first"), -1, filesystem.WriteFlagCreate|filesystem.WriteFlagTruncate); err != nil {
		t.Fatalf("enqueue first: %v", err)
	}
	if _, err := fs.Write("/jobs/enqueue", []byte("second"), -1, filesystem.WriteFlagAppend); err != nil {
		t.Fatalf("enqueue second: %v", err)
	}

	sizeData := mustReadAll(t, fs, "/jobs/size")
	if got := string(sizeData); got != "2" {
		t.Fatalf("queue size = %q, want 2", got)
	}

	peeked := mustReadMessage(t, fs, "/jobs/peek")
	if peeked.Data != "first" {
		t.Fatalf("peeked message = %q, want first", peeked.Data)
	}

	first := mustReadMessage(t, fs, "/jobs/dequeue")
	if first.Data != "first" {
		t.Fatalf("first dequeue = %q, want first", first.Data)
	}

	sizeData = mustReadAll(t, fs, "/jobs/size")
	if got := string(sizeData); got != "1" {
		t.Fatalf("queue size after first dequeue = %q, want 1", got)
	}

	second := mustReadMessage(t, fs, "/jobs/dequeue")
	if second.Data != "second" {
		t.Fatalf("second dequeue = %q, want second", second.Data)
	}

	emptyPeek := mustReadAll(t, fs, "/jobs/peek")
	if got := string(emptyPeek); got != "{}" {
		t.Fatalf("empty peek = %q, want {}", got)
	}

	emptyDequeue := mustReadAll(t, fs, "/jobs/dequeue")
	if got := string(emptyDequeue); got != "{}" {
		t.Fatalf("empty dequeue = %q, want {}", got)
	}

	if _, err := fs.Write("/jobs/enqueue", []byte("to-clear"), -1, filesystem.WriteFlagAppend); err != nil {
		t.Fatalf("enqueue before clear: %v", err)
	}
	if _, err := fs.Write("/jobs/clear", nil, -1, filesystem.WriteFlagTruncate); err != nil {
		t.Fatalf("clear queue: %v", err)
	}

	sizeData = mustReadAll(t, fs, "/jobs/size")
	if got := string(sizeData); got != "0" {
		t.Fatalf("queue size after clear = %q, want 0", got)
	}
}

func TestQueueFSPermissionsAndErrorsRegression(t *testing.T) {
	fs := newTestQueueFS(t)

	if err := fs.Mkdir("/jobs", 0o755); err != nil {
		t.Fatalf("mkdir /jobs: %v", err)
	}

	if _, err := fs.Read("/jobs/enqueue", 0, -1); err == nil || !strings.Contains(err.Error(), "write-only") {
		t.Fatalf("read enqueue error = %v, want write-only", err)
	}
	if _, err := fs.Read("/jobs/clear", 0, -1); err == nil || !strings.Contains(err.Error(), "write-only") {
		t.Fatalf("read clear error = %v, want write-only", err)
	}
	if _, err := fs.Write("/jobs/dequeue", []byte("x"), -1, filesystem.WriteFlagAppend); err == nil || !strings.Contains(err.Error(), "cannot write") {
		t.Fatalf("write dequeue error = %v, want cannot write", err)
	}
	if _, err := fs.Write("/jobs/peek", []byte("x"), -1, filesystem.WriteFlagAppend); err == nil || !strings.Contains(err.Error(), "cannot write") {
		t.Fatalf("write peek error = %v, want cannot write", err)
	}
	if _, err := fs.Write("/jobs/size", []byte("x"), -1, filesystem.WriteFlagAppend); err == nil || !strings.Contains(err.Error(), "cannot write") {
		t.Fatalf("write size error = %v, want cannot write", err)
	}

	if _, err := fs.Write("/jobs", []byte("x"), -1, filesystem.WriteFlagAppend); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("write directory error = %v, want directory error", err)
	}
	if _, err := fs.Read("/enqueue", 0, -1); err == nil || !strings.Contains(err.Error(), "operation without queue name") {
		t.Fatalf("read /enqueue error = %v, want missing queue name", err)
	}
	if _, err := fs.Stat("/jobs/unknown"); err == nil || !strings.Contains(err.Error(), "no such file or directory") {
		t.Fatalf("stat unknown control path error = %v, want missing path", err)
	}
	if err := fs.Remove("/jobs"); err == nil || !strings.Contains(err.Error(), "use RemoveAll") {
		t.Fatalf("remove directory error = %v, want RemoveAll guidance", err)
	}
	if err := fs.Remove("/jobs/enqueue"); err == nil || !strings.Contains(err.Error(), "cannot remove control files") {
		t.Fatalf("remove control file error = %v, want control file error", err)
	}

	entries, err := fs.ReadDir("/jobs")
	if err != nil {
		t.Fatalf("readdir /jobs: %v", err)
	}
	files := queueDirEntryNames(entries)
	if got := files["enqueue"].Mode; got != 0o222 {
		t.Fatalf("enqueue mode = %#o, want 0222", got)
	}
	if got := files["dequeue"].Mode; got != 0o444 {
		t.Fatalf("dequeue mode = %#o, want 0444", got)
	}
	if got := files["size"].Meta.Type; got != MetaValueQueueStatus {
		t.Fatalf("size meta type = %q, want %q", got, MetaValueQueueStatus)
	}
	if got := files["peek"].Meta.Type; got != MetaValueQueueControl {
		t.Fatalf("peek meta type = %q, want %q", got, MetaValueQueueControl)
	}
}
