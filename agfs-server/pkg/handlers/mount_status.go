package handlers

import (
	"sort"
	"sync"
	"time"

	"github.com/c4pt0r/agfs/agfs-server/pkg/filesystem"
)

// Mount lifecycle states surfaced by health/readiness and mount-status APIs.
const (
	MountStatusPending = "pending"
	MountStatusMounted = "mounted"
	MountStatusFailed  = "failed"
)

// MountStatusInfo is the machine-readable lifecycle state for one configured mount.
type MountStatusInfo struct {
	Path       string                 `json:"path"`
	PluginName string                 `json:"pluginName"`
	Instance   string                 `json:"instance,omitempty"`
	Status     string                 `json:"status"`
	Error      string                 `json:"error,omitempty"`
	Config     map[string]interface{} `json:"config,omitempty"`
	UpdatedAt  string                 `json:"updatedAt"`
}

// MountSummary counts the current configured mount lifecycle states.
type MountSummary struct {
	Total   int `json:"total"`
	Pending int `json:"pending"`
	Mounted int `json:"mounted"`
	Failed  int `json:"failed"`
}

// MountStatusTracker tracks configured mount lifecycle outside MountableFS, so
// failed or still-pending configured mounts remain visible even when they never
// enter the mount tree.
type MountStatusTracker struct {
	mu       sync.RWMutex
	statuses map[string]MountStatusInfo
}

// NewMountStatusTracker creates an empty mount lifecycle tracker.
func NewMountStatusTracker() *MountStatusTracker {
	return &MountStatusTracker{statuses: make(map[string]MountStatusInfo)}
}

// Track records a configured mount as pending.
func (t *MountStatusTracker) Track(pluginName, instanceName, path string, config map[string]interface{}) {
	if t == nil {
		return
	}
	path = filesystem.NormalizePath(path)
	t.mu.Lock()
	defer t.mu.Unlock()

	t.statuses[path] = MountStatusInfo{
		Path:       path,
		PluginName: pluginName,
		Instance:   instanceName,
		Status:     MountStatusPending,
		Config:     copyConfig(config),
		UpdatedAt:  time.Now().Format(time.RFC3339Nano),
	}
}

// SetMounted records a configured mount as mounted.
func (t *MountStatusTracker) SetMounted(path string) {
	if t == nil {
		return
	}
	t.update(path, MountStatusMounted, "")
}

// SetFailed records a configured mount as failed with a user-visible error.
func (t *MountStatusTracker) SetFailed(path string, err error) {
	if t == nil {
		return
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	t.update(path, MountStatusFailed, msg)
}

// Statuses returns configured mount statuses sorted by path.
func (t *MountStatusTracker) Statuses() []MountStatusInfo {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()

	statuses := make([]MountStatusInfo, 0, len(t.statuses))
	for _, status := range t.statuses {
		status.Config = copyConfig(status.Config)
		statuses = append(statuses, status)
	}
	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].Path < statuses[j].Path
	})
	return statuses
}

// Summary returns aggregate counts for configured mounts.
func (t *MountStatusTracker) Summary() MountSummary {
	statuses := t.Statuses()
	summary := MountSummary{Total: len(statuses)}
	for _, status := range statuses {
		switch status.Status {
		case MountStatusPending:
			summary.Pending++
		case MountStatusMounted:
			summary.Mounted++
		case MountStatusFailed:
			summary.Failed++
		}
	}
	return summary
}

// Ready reports whether all tracked configured mounts mounted successfully.
func (t *MountStatusTracker) Ready() bool {
	summary := t.Summary()
	return summary.Pending == 0 && summary.Failed == 0
}

// Degraded reports whether any tracked configured mount failed.
func (t *MountStatusTracker) Degraded() bool {
	return t.Summary().Failed > 0
}

func (t *MountStatusTracker) update(path, status, errMsg string) {
	path = filesystem.NormalizePath(path)
	t.mu.Lock()
	defer t.mu.Unlock()

	current := t.statuses[path]
	current.Path = path
	current.Status = status
	current.Error = errMsg
	current.UpdatedAt = time.Now().Format(time.RFC3339Nano)
	t.statuses[path] = current
}

func copyConfig(config map[string]interface{}) map[string]interface{} {
	if len(config) == 0 {
		return nil
	}
	copy := make(map[string]interface{}, len(config))
	for k, v := range config {
		copy[k] = v
	}
	return copy
}
