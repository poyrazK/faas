package snapshothipd

// adr: 20261115140000 — revalidation fetches an evicted live replica back
// when the cache has the re-fetch margin, at most once per cooldown.

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type refetchMetrics struct{ outcomes []string }

func (m *refetchMetrics) ObserveFanout(string, string) {}
func (m *refetchMetrics) ObserveRefetch(o string)      { m.outcomes = append(m.outcomes, o) }

func TestRunnerRefetchesAnEvictedLiveReplicaWithRoom(t *testing.T) {
	const mem, vmstate = "snap/dep-live/mem", "snap/dep-live/vmstate"
	parent := &fakeBackend{objects: map[string][]byte{mem: []byte("memory"), vmstate: []byte("vmstate")}}
	// A budget far above the re-fetch margin: room to take it back.
	cache, err := storage.NewLocalCacheBackend(parent, t.TempDir(), 100<<30)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Put(context.Background(), mem, bytes.NewReader(parent.objects[mem])); err != nil {
		t.Fatal(err)
	}
	job := state.SnapshotReplicaJob{
		SnapshotID: "snap-live", DeploymentID: "dep-live", NodeID: "node-2",
		StorageKey: mem, VMStateStorageKey: vmstate, Attempts: 1, Revalidation: true,
	}
	metrics := &refetchMetrics{}
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	r := New(&fakeReplicaStore{job: job}, cache, "node-2", slog.Default())
	r.metrics = metrics
	r.now = func() time.Time { return now }

	store := &fakeReplicaStore{job: job}
	r.store = store
	r.runWorkTick(context.Background())
	if !store.ready {
		t.Fatalf("ready = false (failed=%v), want the evicted replica fetched back", store.failed)
	}
	if ok, _, _ := storage.CachedLocally(cache, vmstate); !ok {
		t.Fatal("vmstate is not back in the cache")
	}

	// Evicted again inside the cooldown: retired, not fetched again.
	if err := cache.EvictLocal(vmstate); err != nil {
		t.Fatal(err)
	}
	now = now.Add(api.SnapshotReplicaRefetchCooldown / 2)
	gets := len(parent.gets)
	store = &fakeReplicaStore{job: job}
	r.store = store
	r.runWorkTick(context.Background())
	if store.ready || !errors.Is(store.failed, errReplicaEvicted) {
		t.Fatalf("inside the cooldown ready=%v failed=%v, want an eviction retirement", store.ready, store.failed)
	}
	if len(parent.gets) != gets {
		t.Fatalf("inside the cooldown the parent was read again: %v", parent.gets[gets:])
	}
	if want := []string{RefetchRefetched, RefetchCooldown}; len(metrics.outcomes) != 2 || metrics.outcomes[0] != want[0] || metrics.outcomes[1] != want[1] {
		t.Fatalf("refetch outcomes = %v, want %v", metrics.outcomes, want)
	}
}
