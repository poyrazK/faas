// adr: 678
package durableentity

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestUncertainDeleteRecoversWithoutChangingReceipts(t *testing.T) {
	f := newFixture(t)
	for _, id := range []string{"one", "two"} {
		if _, err := f.manager.Execute(t.Context(), f.claim, request(id), increment); err != nil {
			t.Fatal(err)
		}
	}
	failed := false
	store := cleanupFaultStore{memoryStore: f.store, remove: func(ctx context.Context, key string) error {
		if err := f.store.DeleteEntityObject(ctx, key); err != nil {
			return err
		}
		if !failed {
			failed = true
			return errors.New("accepted delete response lost")
		}
		return nil
	}}
	m := openManager(t, store, f.clock)
	page, err := m.Collect(t.Context(), f.claim, "")
	if err != nil || page.Failed != 1 || !failed {
		t.Fatal(page, err)
	}
	collectAll(t, f.manager, f.claim)
	for _, id := range []string{"one", "two"} {
		result, err := f.manager.Execute(t.Context(), f.claim, request(id), increment)
		if err != nil || !result.Replayed {
			t.Fatal(result, err)
		}
	}
	assertCount(t, t.Context(), f.manager, f.id, 2, 2)
}

func TestCleanupProtectsNewGenerationUploads(t *testing.T) {
	f := newFixture(t)
	orphan := f.id.prefix() + "snapshots/1/" + uuid.NewString() + ".json"
	store := cleanupFaultStore{memoryStore: f.store, list: func(ctx context.Context, prefix, cursor string, limit int32) (CleanupObjects, error) {
		if _, err := f.store.Put(ctx, orphan, []byte(`{}`), ""); err != nil {
			return CleanupObjects{}, err
		}
		return CleanupObjects{Keys: []string{orphan}}, nil
	}}
	m := openManager(t, store, f.clock)
	page, err := m.Collect(t.Context(), f.claim, "")
	if err != nil || page.Deleted != 0 || page.Retained != 1 {
		t.Fatal(page, err)
	}
	if _, _, err := f.store.Get(t.Context(), orphan, 1024); err != nil {
		t.Fatal(err)
	}
	if deleted := collectAll(t, f.manager, f.claim); deleted != 1 {
		t.Fatal(deleted)
	}
}

func TestReadRacingCleanupReportsConflictThenRestores(t *testing.T) {
	f := newFixture(t)
	if _, err := f.manager.Execute(t.Context(), f.claim, request("one"), increment); err != nil {
		t.Fatal(err)
	}
	value, _, err := f.manager.readManifest(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	store := &readRaceStore{ObjectStore: f.store, key: value.SnapshotKey, before: func() {
		if _, err := f.manager.Execute(t.Context(), f.claim, request("two"), increment); err != nil {
			t.Fatal(err)
		}
		collectAll(t, f.manager, f.claim)
	}}
	m := openManager(t, store, f.clock)
	if _, err := m.Read(t.Context(), f.id); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	assertCount(t, t.Context(), m, f.id, 2, 2)
}

type readRaceStore struct {
	ObjectStore
	key    string
	before func()
}

func (s *readRaceStore) Get(ctx context.Context, key string, limit int64) ([]byte, string, error) {
	if key == s.key && s.before != nil {
		before := s.before
		s.before = nil
		before()
	}
	return s.ObjectStore.Get(ctx, key, limit)
}

func TestCleanupConcurrentCommitPreservesSharedReceipts(t *testing.T) {
	f := newFixture(t)
	for _, id := range []string{"one", "two", "three"} {
		if _, err := f.manager.Execute(t.Context(), f.claim, request(id), increment); err != nil {
			t.Fatal(err)
		}
	}
	other := openManager(t, f.store, f.clock)
	store := cleanupFaultStore{memoryStore: f.store, list: func(ctx context.Context, prefix, cursor string, limit int32) (CleanupObjects, error) {
		if _, err := other.Execute(ctx, f.claim, request("concurrent"), increment); err != nil {
			return CleanupObjects{}, err
		}
		return f.store.ListEntityObjects(ctx, prefix, cursor, limit)
	}}
	m := openManager(t, store, f.clock)
	if _, err := m.Collect(t.Context(), f.claim, ""); err != nil {
		t.Fatal(err)
	}
	collectAll(t, other, f.claim)
	for _, id := range []string{"one", "two", "three", "concurrent"} {
		result, err := other.Execute(t.Context(), f.claim, request(id), increment)
		if err != nil || !result.Replayed {
			t.Fatal(result, err)
		}
	}
	assertCount(t, t.Context(), other, f.id, 4, 4)
}

func TestCleanupMissingCommittedIndexNeverDeletes(t *testing.T) {
	f := newFixture(t)
	if _, err := f.manager.Execute(t.Context(), f.claim, request("one"), increment); err != nil {
		t.Fatal(err)
	}
	value, _, err := f.manager.readManifest(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	state, err := f.manager.readSnapshot(t.Context(), value)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.DeleteEntityObject(t.Context(), state.ReceiptRoot.Key); err != nil {
		t.Fatal(err)
	}
	m := openManager(t, cleanupFaultStore{memoryStore: f.store, remove: func(context.Context, string) error { t.Fatal("deleted while index was corrupt"); return nil }}, f.clock)
	if _, err := m.Collect(t.Context(), f.claim, ""); !errors.Is(err, ErrCorrupt) {
		t.Fatal(err)
	}
}

func TestMaintenanceManifestRejectsOldSchema(t *testing.T) {
	f := newFixture(t)
	if _, err := f.manager.Collect(t.Context(), f.claim, ""); err != nil {
		t.Fatal(err)
	}
	body, _, err := f.store.Get(t.Context(), f.id.prefix()+"manifest.json", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	var value manifest
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatal(err)
	}
	if value.Schema != 3 || value.Generation != 1 {
		t.Fatal(value)
	}
	value.Schema = 1
	if validManifest(f.id, value) {
		t.Fatal("generation barrier accepted without schema upgrade")
	}
	value.Schema = 2
	value.SnapshotKey = f.id.prefix() + "snapshots/01/" + uuid.NewString() + ".json"
	value.Version = 1
	value.SnapshotHash = strings.Repeat("a", 64)
	if validManifest(f.id, value) {
		t.Fatal("noncanonical generation path accepted")
	}
}
