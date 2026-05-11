package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/c4pt0r/agfs/agfs-server/pkg/filesystem"
	"github.com/c4pt0r/agfs/agfs-server/pkg/mountablefs"
	"github.com/c4pt0r/agfs/agfs-server/pkg/plugin/api"
	"github.com/c4pt0r/agfs/agfs-server/pkg/plugins/memfs"
)

func newBodyLimitTestHandler(maxBytes int64) (*Handler, *memfs.MemoryFS) {
	fs := memfs.NewMemoryFS()
	h := NewHandler(fs, nil)
	h.SetMaxRequestBodyBytes(maxBytes)
	return h, fs
}

func decodeErrorResponse(t *testing.T, rec *httptest.ResponseRecorder) ErrorResponse {
	t.Helper()
	var resp ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	return resp
}

func TestWriteFileRejectsOverLimitRequestBody(t *testing.T) {
	h, fs := newBodyLimitTestHandler(4)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/files?path=/big.txt", strings.NewReader("12345"))
	rec := httptest.NewRecorder()
	h.WriteFile(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status 413, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := decodeErrorResponse(t, rec).Error; !strings.Contains(got, "maximum size of 4 bytes") {
		t.Fatalf("unexpected error response: %q", got)
	}
	if data, err := fs.Read("/big.txt", 0, -1); err == nil || len(data) != 0 {
		t.Fatalf("expected over-limit write to leave file absent, data=%q err=%v", string(data), err)
	}
}

func TestWriteFileAllowsNormalRequestBody(t *testing.T) {
	h, fs := newBodyLimitTestHandler(16)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/files?path=/ok.txt", strings.NewReader("hello"))
	rec := httptest.NewRecorder()
	h.WriteFile(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	data, err := fs.Read("/ok.txt", 0, -1)
	if err != nil && err != io.EOF {
		t.Fatalf("read failed: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("expected written data %q, got %q", "hello", string(data))
	}
}

func TestWriteAliasRejectsOverLimitJSONBody(t *testing.T) {
	h, _ := newBodyLimitTestHandler(12)
	mux := http.NewServeMux()
	h.SetupRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/write?path=/json.txt", strings.NewReader(`{"data":"too-large"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status 413, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleWriteRejectsOverLimitRequestBody(t *testing.T) {
	h, fs := newBodyLimitTestHandler(4)
	handle, err := fs.OpenHandle("/handle.txt", filesystem.O_RDWR|filesystem.O_CREATE, 0644)
	if err != nil {
		t.Fatalf("open handle failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/v1/handles/1/write", strings.NewReader("12345"))
	rec := httptest.NewRecorder()
	h.HandleWrite(rec, req, strconv.FormatInt(handle.ID(), 10))

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status 413, got %d: %s", rec.Code, rec.Body.String())
	}
	data, err := fs.Read("/handle.txt", 0, -1)
	if err != nil && err != io.EOF {
		t.Fatalf("read failed: %v", err)
	}
	if len(data) != 0 {
		t.Fatalf("expected handle write to leave file empty, got %q", string(data))
	}
}

func TestPluginHandlerRejectsOverLimitJSONBody(t *testing.T) {
	mfs := mountablefs.NewMountableFS(api.PoolConfig{})
	ph := NewPluginHandler(mfs)
	ph.SetMaxRequestBodyBytes(8)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/mount", strings.NewReader(`{"fstype":"memfs"}`))
	rec := httptest.NewRecorder()
	ph.Mount(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status 413, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRequestBodyLimitDefaultsAndOverrides(t *testing.T) {
	h, _ := newBodyLimitTestHandler(0)
	if got := h.MaxRequestBodyBytes(); got != DefaultMaxRequestBodyBytes {
		t.Fatalf("expected default limit %d, got %d", DefaultMaxRequestBodyBytes, got)
	}
	h.SetMaxRequestBodyBytes(1024)
	if got := h.MaxRequestBodyBytes(); got != 1024 {
		t.Fatalf("expected override limit 1024, got %d", got)
	}
}
