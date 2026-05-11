//go:build failpoint

package queuefs

import (
	"path/filepath"
	"testing"

	"github.com/pingcap/failpoint"
)

func newSQLiteFailpointTestPlugin(t *testing.T, dbPath string) *QueueFSPlugin {
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
	return plugin
}

func TestQueueFSSQLiteRemoveQueuePartialFailureRegression(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "queuefs-remove-partial.db")
	plugin := newSQLiteFailpointTestPlugin(t, dbPath)
	backend, ok := plugin.backend.(*TiDBBackend)
	if !ok {
		t.Fatalf("unexpected backend type %T", plugin.backend)
	}

	for _, queueName := range []string{"jobs", "logs", "alerts"} {
		if err := backend.CreateQueue(queueName); err != nil {
			t.Fatalf("create queue %s: %v", queueName, err)
		}
	}

	// Inject a DROP TABLE failure on the second removal attempt so the test can
	// verify partial-success handling deterministically without relying on backend-
	// specific locking or permission behavior.
	failpointPath := "github.com/c4pt0r/agfs/agfs-server/pkg/plugins/queuefs/queuefsRemoveQueueDropError"
	if err := failpoint.Enable(failpointPath, "return(2)"); err != nil {
		t.Fatalf("enable failpoint: %v", err)
	}
	t.Cleanup(func() {
		_ = failpoint.Disable(failpointPath)
	})

	err := backend.RemoveQueue("")
	if err == nil {
		t.Fatalf("RemoveQueue unexpectedly succeeded after enabling %s; expected injected DROP TABLE failure", failpointPath)
	}

	// Successful drops should be removed from both registry and cache, while the
	// failed drop remains visible so queuefs_registry stays aligned with surviving
	// physical tables.
	remaining := 0
	for _, name := range []string{"jobs", "logs", "alerts"} {
		exists, existsErr := backend.QueueExists(name)
		if existsErr != nil {
			t.Fatalf("QueueExists(%s): %v", name, existsErr)
		}
		if exists {
			remaining++
			if _, ok := backend.tableCache[name]; !ok {
				t.Fatalf("expected cache to retain failed queue %q", name)
			}
			continue
		}
		if _, ok := backend.tableCache[name]; ok {
			t.Fatalf("expected cache entry for removed queue %q to be cleared", name)
		}
	}
	if remaining != 1 {
		t.Fatalf("remaining queues = %d, want 1", remaining)
	}

	queues, listErr := backend.ListQueues("")
	if listErr != nil {
		t.Fatalf("ListQueues: %v", listErr)
	}
	if len(queues) != 1 {
		t.Fatalf("visible queues after partial failure = %v, want 1 remaining queue", queues)
	}
}
