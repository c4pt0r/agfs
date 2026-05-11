package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/c4pt0r/agfs/agfs-server/pkg/mountablefs"
	"github.com/c4pt0r/agfs/agfs-server/pkg/plugin/api"
	"github.com/c4pt0r/agfs/agfs-server/pkg/plugins/memfs"
)

func TestHealthAndReadyReflectMountLifecycle(t *testing.T) {
	tracker := NewMountStatusTracker()
	tracker.Track("localfs", "localfs", "/local", map[string]interface{}{"local_dir": "/missing"})

	h := NewHandler(memfs.NewMemoryFS(), nil)
	h.SetMountStatusTracker(tracker)

	healthRec := httptest.NewRecorder()
	h.Health(healthRec, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	health := decodeHealthResponse(t, healthRec)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("health should remain HTTP 200 while starting, got %d", healthRec.Code)
	}
	if health.Status != "starting" || health.Ready || health.Degraded {
		t.Fatalf("unexpected starting health response: %+v", health)
	}
	if health.Mounts.Pending != 1 || health.Mounts.Failed != 0 {
		t.Fatalf("unexpected starting mount summary: %+v", health.Mounts)
	}

	readyRec := httptest.NewRecorder()
	h.Ready(readyRec, httptest.NewRequest(http.MethodGet, "/api/v1/ready", nil))
	if readyRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready should be 503 while mounts are pending, got %d", readyRec.Code)
	}

	tracker.SetFailed("/local", errors.New("local_dir does not exist"))
	healthRec = httptest.NewRecorder()
	h.Health(healthRec, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	health = decodeHealthResponse(t, healthRec)
	if health.Status != "degraded" || health.Ready || !health.Degraded {
		t.Fatalf("unexpected degraded health response: %+v", health)
	}
	if health.Mounts.Failed != 1 || health.Mounts.Pending != 0 {
		t.Fatalf("unexpected degraded mount summary: %+v", health.Mounts)
	}

	tracker.SetMounted("/local")
	readyRec = httptest.NewRecorder()
	h.Ready(readyRec, httptest.NewRequest(http.MethodGet, "/api/v1/ready", nil))
	ready := decodeHealthResponse(t, readyRec)
	if readyRec.Code != http.StatusOK {
		t.Fatalf("ready should be 200 once mounts succeed, got %d", readyRec.Code)
	}
	if ready.Status != "healthy" || !ready.Ready || ready.Degraded {
		t.Fatalf("unexpected ready response: %+v", ready)
	}
}

func TestListMountsIncludesPendingAndFailedConfiguredMounts(t *testing.T) {
	tracker := NewMountStatusTracker()
	tracker.Track("localfs", "localfs", "/local", map[string]interface{}{"local_dir": "/missing"})
	tracker.SetFailed("/local", errors.New("local_dir does not exist"))
	tracker.Track("queuefs", "jobs", "/queue", map[string]interface{}{"backend": "sqlite"})

	ph := NewPluginHandler(mountablefs.NewMountableFS(api.PoolConfig{}))
	ph.SetMountStatusTracker(tracker)

	rec := httptest.NewRecorder()
	ph.ListMounts(rec, httptest.NewRequest(http.MethodGet, "/api/v1/mounts", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var response ListMountsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode mount response: %v", err)
	}
	if len(response.Mounts) != 2 {
		t.Fatalf("expected 2 tracked mounts, got %d: %+v", len(response.Mounts), response.Mounts)
	}
	if response.Mounts[0].Path != "/local" || response.Mounts[0].Status != MountStatusFailed || response.Mounts[0].Error == "" {
		t.Fatalf("failed mount status missing from response: %+v", response.Mounts[0])
	}
	if response.Mounts[1].Path != "/queue" || response.Mounts[1].Status != MountStatusPending {
		t.Fatalf("pending mount status missing from response: %+v", response.Mounts[1])
	}
}

func decodeHealthResponse(t *testing.T, rec *httptest.ResponseRecorder) HealthResponse {
	t.Helper()
	var response HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode health response: %v", err)
	}
	return response
}
