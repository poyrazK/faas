// adr: 054
package storage_test

import (
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
)

func TestLocalCachePutSupersedesPendingFill(t *testing.T) {
	const key = "snap/deployment/mem"
	parent := newFakeBackend()
	if err := parent.Put(t.Context(), key, strings.NewReader("old snapshot")); err != nil {
		t.Fatal(err)
	}
	cache, gate := newDeferredCache(t, parent)
	release := sync.OnceFunc(func() { close(gate.release) })
	t.Cleanup(func() {
		release()
		cache.WaitDeferred()
	})
	_, original := getString(t, storage.WithDeferredCacheDurability(t.Context()), cache, key)
	t.Cleanup(func() { _ = original.Close() })
	<-gate.started
	if err := cache.Put(t.Context(), key, strings.NewReader("new snapshot")); err != nil {
		t.Fatal(err)
	}
	checkCurrent := func(stage string) {
		t.Helper()
		got, reader := getString(t, t.Context(), cache, key)
		_ = reader.Close()
		if got != "new snapshot" {
			t.Errorf("%s: cache returned %q after successful Put, want new snapshot", stage, got)
		}
	}
	checkCurrent("old fill still syncing")
	release()
	cache.WaitDeferred()
	checkCurrent("old fill finished syncing")
	if data, err := io.ReadAll(original); err != nil || string(data) != "old snapshot" {
		t.Errorf("original reader = %q, %v; superseding a fill must preserve its open reader", data, err)
	}
	if temps := cacheTemps(t, cache); len(temps) != 0 {
		t.Errorf("superseded fill left temporary files: %v", temps)
	}
}
