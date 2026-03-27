package queuefs

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/c4pt0r/agfs/agfs-server/pkg/filesystem"
)

func TestQueueFSOpenWriteAndTruncateRegression(t *testing.T) {
	fs := newTestQueueFS(t)

	if err := fs.Mkdir("/jobs", 0o755); err != nil {
		t.Fatalf("mkdir /jobs: %v", err)
	}
	if err := fs.Truncate("/jobs/enqueue", 0); err != nil {
		t.Fatalf("truncate enqueue: %v", err)
	}

	writer, err := fs.OpenWrite("/jobs/enqueue")
	if err != nil {
		t.Fatalf("openwrite enqueue: %v", err)
	}
	if _, err := writer.Write([]byte("chunk-1")); err != nil {
		t.Fatalf("write first chunk: %v", err)
	}
	if _, err := writer.Write([]byte("+chunk-2")); err != nil {
		t.Fatalf("write second chunk: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close enqueue writer: %v", err)
	}

	msg := mustReadMessage(t, fs, "/jobs/dequeue")
	if msg.Data != "chunk-1+chunk-2" {
		t.Fatalf("openwrite enqueue payload = %q, want concatenated payload", msg.Data)
	}

	if _, err := fs.Write("/jobs/enqueue", []byte("keep"), -1, filesystem.WriteFlagAppend); err != nil {
		t.Fatalf("enqueue keep: %v", err)
	}
	clearWriter, err := fs.OpenWrite("/jobs/clear")
	if err != nil {
		t.Fatalf("openwrite clear: %v", err)
	}
	if err := clearWriter.Close(); err != nil {
		t.Fatalf("close clear writer: %v", err)
	}

	sizeData := mustReadAll(t, fs, "/jobs/size")
	if got := string(sizeData); got != "0" {
		t.Fatalf("queue size after openwrite clear = %q, want 0", got)
	}
}

func TestQueueFSHandleRegression(t *testing.T) {
	fs := newTestQueueFS(t)

	if err := fs.Mkdir("/jobs", 0o755); err != nil {
		t.Fatalf("mkdir /jobs: %v", err)
	}

	enqueueHandle, err := fs.OpenHandle("/jobs/enqueue", filesystem.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open enqueue handle: %v", err)
	}
	if _, err := enqueueHandle.Write([]byte("payload")); err != nil {
		t.Fatalf("write enqueue handle: %v", err)
	}

	stat, err := enqueueHandle.Stat()
	if err != nil {
		t.Fatalf("stat enqueue handle: %v", err)
	}
	if stat.Mode != 0o222 {
		t.Fatalf("enqueue handle mode = %#o, want 0222", stat.Mode)
	}
	if enqueueHandle.Flags() != filesystem.O_WRONLY {
		t.Fatalf("enqueue handle flags = %v, want O_WRONLY", enqueueHandle.Flags())
	}

	sizeHandle, err := fs.OpenHandle("/jobs/size", filesystem.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("open size handle: %v", err)
	}
	buf := make([]byte, 4)
	n, err := sizeHandle.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("read size handle: %v", err)
	}
	if got := string(buf[:n]); got != "1" {
		t.Fatalf("size handle read = %q, want 1", got)
	}
	if gotHandle, err := fs.GetHandle(sizeHandle.ID()); err != nil || gotHandle.ID() != sizeHandle.ID() {
		t.Fatalf("get size handle = (%v, %v), want same handle", gotHandle, err)
	}

	dequeueHandle, err := fs.OpenHandle("/jobs/dequeue", filesystem.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("open dequeue handle: %v", err)
	}

	firstChunk := make([]byte, 8)
	n, err = dequeueHandle.Read(firstChunk)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("read dequeue first chunk: %v", err)
	}
	secondChunk := make([]byte, 256)
	n2, err := dequeueHandle.Read(secondChunk)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("read dequeue second chunk: %v", err)
	}
	if _, err := dequeueHandle.Read(secondChunk); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF after cached dequeue payload, got %v", err)
	}

	payload := append(firstChunk[:n], secondChunk[:n2]...)
	var msg QueueMessage
	if err := json.Unmarshal(payload, &msg); err != nil {
		t.Fatalf("unmarshal dequeue payload: %v (payload=%q)", err, string(payload))
	}
	if msg.Data != "payload" {
		t.Fatalf("dequeue handle payload = %q, want payload", msg.Data)
	}

	sizeData := mustReadAll(t, fs, "/jobs/size")
	if got := string(sizeData); got != "0" {
		t.Fatalf("queue size after dequeue handle = %q, want 0", got)
	}

	if _, err := sizeHandle.Write([]byte("x")); err == nil || !strings.Contains(err.Error(), "cannot write") {
		t.Fatalf("write size handle error = %v, want cannot write", err)
	}

	if err := dequeueHandle.Close(); err != nil {
		t.Fatalf("close dequeue handle: %v", err)
	}
	if err := fs.CloseHandle(sizeHandle.ID()); err != nil {
		t.Fatalf("close size handle by id: %v", err)
	}
	if _, err := fs.GetHandle(sizeHandle.ID()); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("get closed size handle error = %v, want ErrNotFound", err)
	}

	if err := enqueueHandle.Close(); err != nil {
		t.Fatalf("close enqueue handle: %v", err)
	}
	if err := fs.CloseHandle(enqueueHandle.ID()); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("close already closed enqueue handle error = %v, want ErrNotFound", err)
	}
}
