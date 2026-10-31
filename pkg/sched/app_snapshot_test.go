// spec: §4.3 — schedd's app pollers (reaper, scale-up, targets and floor
// triggers) list the apps they own every tick.

package sched_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// listingStore counts full app reads. versionedListingStore adds
// state.AppsListVersionReader.
type listingStore struct {
	apps  map[string][]state.App
	reads map[string]int
}

func (s *listingStore) ListAllApps(context.Context) ([]state.App, error) {
	s.reads[""]++
	return append([]state.App(nil), s.apps[""]...), nil
}

func (s *listingStore) ListAppsByNodeID(_ context.Context, nodeID string) ([]state.App, error) {
	s.reads[nodeID]++
	return append([]state.App(nil), s.apps[nodeID]...), nil
}

type versionedListingStore struct {
	*listingStore
	versions   map[string]state.AppsListVersion
	versionErr error
}

func (s *versionedListingStore) AppsListVersion(_ context.Context, nodeID string) (state.AppsListVersion, error) {
	if s.versionErr != nil {
		return state.AppsListVersion{}, s.versionErr
	}
	return s.versions[nodeID], nil
}

func snapshotApp(slug string) state.App {
	scaled := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	return state.App{
		ID: slug + "-id", Slug: slug, LastScaleOutAt: &scaled, EgressPorts: []int{5432},
		Manifest: state.AppManifest{Env: map[string]string{"MODE": "prod"}},
	}
}

func newVersionedListingStore() *versionedListingStore {
	return &versionedListingStore{
		listingStore: &listingStore{
			apps:  map[string][]state.App{"": {snapshotApp("a"), snapshotApp("b")}, "node-1": {snapshotApp("a")}},
			reads: map[string]int{},
		},
		versions: map[string]state.AppsListVersion{"": {Count: 2, RowVersions: 10}, "node-1": {Count: 1, RowVersions: 4}},
	}
}

// The snapshot rereads a list only when its version changed, and keeps each
// node's list apart from the all-apps list.
func TestAppSnapshotRereadsOnlyWhenTheVersionChanges(t *testing.T) {
	ctx := context.Background()
	store := newVersionedListingStore()
	snap := sched.NewAppSnapshot(store)

	for range 3 {
		apps, err := snap.ListAllApps(ctx)
		if err != nil || len(apps) != 2 {
			t.Fatalf("ListAllApps = %d apps (err %v), want 2", len(apps), err)
		}
		if _, err := snap.ListAppsByNodeID(ctx, "node-1"); err != nil {
			t.Fatalf("ListAppsByNodeID: %v", err)
		}
	}
	if store.reads[""] != 1 || store.reads["node-1"] != 1 {
		t.Fatalf("full reads = %v, want one per list while the version holds", store.reads)
	}

	store.apps[""] = append(store.apps[""], snapshotApp("c"))
	store.versions[""] = state.AppsListVersion{Count: 3, RowVersions: 17}
	apps, err := snap.ListAllApps(ctx)
	if err != nil || len(apps) != 3 {
		t.Fatalf("ListAllApps after a change = %d apps (err %v), want 3", len(apps), err)
	}
	if store.reads[""] != 2 || store.reads["node-1"] != 1 {
		t.Fatalf("full reads = %v, want the changed list reread and the node list kept", store.reads)
	}
}

// Every caller gets its own copy: filtering in place (the reaper) or writing
// through a pointer, slice or map field leaves the next caller's list intact.
func TestAppSnapshotCallersShareNoMemory(t *testing.T) {
	ctx := context.Background()
	snap := sched.NewAppSnapshot(newVersionedListingStore())

	first, err := snap.ListAllApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	*first[0].LastScaleOutAt = time.Time{}
	first[0].EgressPorts[0] = 1
	first[0].Manifest.Env["MODE"] = "changed"
	first[0] = first[1] // the reaper's in-place filter

	second, err := snap.ListAllApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := snapshotApp("a")
	got := second[0]
	if got.Slug != "a" || !got.LastScaleOutAt.Equal(*want.LastScaleOutAt) || got.EgressPorts[0] != 5432 || got.Manifest.Env["MODE"] != "prod" {
		t.Fatalf("second caller saw the first caller's writes: %+v", got)
	}
}

// A store without the version, or a failed version read, falls back to
// reading the rows every time.
func TestAppSnapshotReadsDirectlyWithoutAVersion(t *testing.T) {
	ctx := context.Background()
	plain := &listingStore{apps: map[string][]state.App{"": {snapshotApp("a")}}, reads: map[string]int{}}
	snap := sched.NewAppSnapshot(plain)
	for range 2 {
		if _, err := snap.ListAllApps(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if plain.reads[""] != 2 {
		t.Fatalf("plain store read %d times, want every call", plain.reads[""])
	}

	versioned := newVersionedListingStore()
	versioned.versionErr = errors.New("version unavailable")
	snap = sched.NewAppSnapshot(versioned)
	for range 2 {
		if _, err := snap.ListAllApps(ctx); err != nil {
			t.Fatal(err)
		}
	}
	versioned.versionErr = nil
	if _, err := snap.ListAllApps(ctx); err != nil {
		t.Fatal(err)
	}
	if versioned.reads[""] != 3 {
		t.Fatalf("read %d times, want every call after a failed version read and one to reload", versioned.reads[""])
	}
}
