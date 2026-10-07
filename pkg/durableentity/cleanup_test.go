// adr: 678
package durableentity

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func (s *memoryStore) ListEntityObjects(ctx context.Context, prefix, cursor string, limit int32) (CleanupObjects, error) {
	if err := ctx.Err(); err != nil {
		return CleanupObjects{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var keys []string
	for key := range s.objects {
		if strings.HasPrefix(key, prefix) && key > cursor {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	page := CleanupObjects{Keys: keys, Sizes: make(map[string]int64)}
	if len(keys) > int(limit) {
		page.Keys = keys[:limit]
		page.NextCursor = page.Keys[len(page.Keys)-1]
	}
	for _, key := range page.Keys {
		page.Sizes[key] = int64(len(s.objects[key].body))
	}
	return page, nil
}

func (s *memoryStore) DeleteEntityObject(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}

func collectAll(t *testing.T, m *Manager, claim Claim) int {
	t.Helper()
	cursor, deleted := "", 0
	for {
		page, err := m.Collect(t.Context(), claim, cursor)
		if err != nil || page.Failed != 0 {
			t.Fatal(page, err)
		}
		if page.Deleted+page.Retained > api.DurableEntityCleanupPageSize {
			t.Fatal("unbounded cleanup page", page)
		}
		deleted += page.Deleted
		if page.NextCursor == "" {
			return deleted
		}
		cursor = page.NextCursor
	}
}

func TestCleanupReclaimsSupersededObjectsButPreservesStateAndReplay(t *testing.T) {
	f := newFixture(t)
	for i := range 12 {
		if _, err := f.manager.Execute(t.Context(), f.claim, request(fmt.Sprint(i)), increment); err != nil {
			t.Fatal(err)
		}
	}
	unknown := f.id.prefix() + "snapshots/operator-note"
	if _, err := f.store.Put(t.Context(), unknown, []byte(`{}`), ""); err != nil {
		t.Fatal(err)
	}
	if deleted := collectAll(t, f.manager, f.claim); deleted < 11 {
		t.Fatal("superseded state was retained", deleted)
	}
	if _, _, err := f.store.Get(t.Context(), unknown, 1024); err != nil {
		t.Fatal("unknown object deleted", err)
	}
	for i := range 12 {
		result, err := f.manager.Execute(t.Context(), f.claim, request(fmt.Sprint(i)), increment)
		if err != nil || !result.Replayed || result.Version != uint64(i+1) {
			t.Fatal(result, err)
		}
	}
	if deleted := collectAll(t, f.manager, f.claim); deleted != 0 {
		t.Fatal("live objects deleted on repeated sweep", deleted)
	}
	assertCount(t, t.Context(), f.manager, f.id, 12, 12)
}

func TestCleanupFencesEveryOlderPublicationPhase(t *testing.T) {
	for _, phase := range []string{"callback", "snapshot-upload", "manifest-cas"} {
		t.Run(phase, func(t *testing.T) {
			f := newFixture(t)
			if _, err := f.manager.Execute(t.Context(), f.claim, request("one"), increment); err != nil {
				t.Fatal(err)
			}
			collector := openManager(t, f.store, f.clock)
			triggered := false
			collect := func() {
				if !triggered {
					triggered = true
					collectAll(t, collector, f.claim)
				}
			}
			store := wrappedStore{ObjectStore: f.store, put: func(ctx context.Context, key string, body []byte, etag string) (string, error) {
				if phase == "manifest-cas" && strings.HasSuffix(key, "/manifest.json") {
					collect()
				}
				version, err := f.store.Put(ctx, key, body, etag)
				if phase == "snapshot-upload" && strings.Contains(key, "/snapshots/") {
					collect()
				}
				return version, err
			}}
			writer := openManager(t, store, f.clock)
			_, err := writer.Execute(t.Context(), f.claim, request("late"), func(ctx context.Context, view View) (Transition, error) {
				if phase == "callback" {
					collect()
				}
				return increment(ctx, view)
			})
			if !triggered || !errors.Is(err, ErrConflict) {
				t.Fatal(triggered, err)
			}
			collectAll(t, collector, f.claim)
			result, err := collector.Execute(t.Context(), f.claim, request("late"), increment)
			if err != nil || result.Replayed || result.Version != 2 {
				t.Fatal(result, err)
			}
			assertCount(t, t.Context(), collector, f.id, 2, 2)
		})
	}
}

type cleanupFaultStore struct {
	*memoryStore
	list   func(context.Context, string, string, int32) (CleanupObjects, error)
	remove func(context.Context, string) error
	put    func(context.Context, string, []byte, string) (string, error)
}

func (s cleanupFaultStore) Put(ctx context.Context, key string, body []byte, etag string) (string, error) {
	if s.put != nil {
		return s.put(ctx, key, body, etag)
	}
	return s.memoryStore.Put(ctx, key, body, etag)
}
func (s cleanupFaultStore) ListEntityObjects(ctx context.Context, prefix, cursor string, limit int32) (CleanupObjects, error) {
	if s.list != nil {
		return s.list(ctx, prefix, cursor, limit)
	}
	return s.memoryStore.ListEntityObjects(ctx, prefix, cursor, limit)
}
func (s cleanupFaultStore) DeleteEntityObject(ctx context.Context, key string) error {
	if s.remove != nil {
		return s.remove(ctx, key)
	}
	return s.memoryStore.DeleteEntityObject(ctx, key)
}

func TestUncertainCleanupBarrierNeverDeletes(t *testing.T) {
	f := newFixture(t)
	orphan := f.id.prefix() + "snapshots/" + uuid.NewString() + ".json"
	if _, err := f.store.Put(t.Context(), orphan, []byte(`{}`), ""); err != nil {
		t.Fatal(err)
	}
	store := cleanupFaultStore{memoryStore: f.store, put: func(ctx context.Context, key string, body []byte, etag string) (string, error) {
		version, err := f.store.Put(ctx, key, body, etag)
		if err == nil && strings.HasSuffix(key, "/manifest.json") {
			return "", errors.New("accepted barrier response lost")
		}
		return version, err
	}, remove: func(context.Context, string) error { t.Fatal("delete followed an uncertain barrier"); return nil }}
	m := openManager(t, store, f.clock)
	if _, err := m.Collect(t.Context(), f.claim, ""); !errors.Is(err, ErrUncertain) {
		t.Fatal(err)
	}
	if _, _, err := f.store.Get(t.Context(), orphan, 1024); err != nil {
		t.Fatal(err)
	}
	if deleted := collectAll(t, f.manager, f.claim); deleted != 1 {
		t.Fatal(deleted)
	}
}

func TestCleanupRejectsMalformedListingAndUnsafeAuthority(t *testing.T) {
	for _, page := range []CleanupObjects{{Keys: []string{"outside"}}, {Keys: make([]string, api.DurableEntityCleanupPageSize+1)}, {NextCursor: "next"}, {Keys: []string{"outside"}, NextCursor: strings.Repeat("x", api.MaxObjectS3ListCursorBytes+1)}} {
		f := newFixture(t)
		store := cleanupFaultStore{memoryStore: f.store, list: func(context.Context, string, string, int32) (CleanupObjects, error) { return page, nil }, remove: func(context.Context, string) error { t.Fatal("unsafe delete"); return nil }}
		m := openManager(t, store, f.clock)
		if _, err := m.Collect(t.Context(), f.claim, ""); !errors.Is(err, ErrCorrupt) {
			t.Fatal(err)
		}
		if err := f.manager.Release(t.Context(), f.claim); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Collect(t.Context(), f.claim, ""); !errors.Is(err, ErrStaleOwner) {
			t.Fatal(err)
		}
	}
}
