// adr: 593
package sched_test

// adr: 435, 581. Publication uses the real imaged owner and real store fences.

import (
	"io"
	"log/slog"
	"testing"

	"github.com/onebox-faas/faas/pkg/imaged"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func composedSnapshotImageHandler(t *testing.T, store state.Store, node string) *imaged.Handler {
	t.Helper()
	backend, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return imaged.New(store, nil, nil, nil, "", t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil))).WithStorage(backend).WithNodeName(node)
}

func TestApplicationStandardComposedSnapshots(t *testing.T) {
	store := state.NewMemStore()
	sched.RunApplicationStandardComposedSnapshots(t, store, func(node string) sched.ApplicationStandardSnapshotTestHooks {
		h := composedSnapshotImageHandler(t, store, node)
		return sched.ApplicationStandardSnapshotTestHooks{Publish: h.HandleNotification}
	})
}
