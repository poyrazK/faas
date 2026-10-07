// adr: 638
package durableentity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type maintenanceFaultStore struct {
	cleanupFaultStore
	prefixes func(context.Context, string, string, int32) (EntityPrefixPage, error)
}

func (s maintenanceFaultStore) ListEntityPrefixes(ctx context.Context, prefix, cursor string, limit int32) (EntityPrefixPage, error) {
	if s.prefixes != nil {
		return s.prefixes(ctx, prefix, cursor, limit)
	}
	return s.memoryStore.ListEntityPrefixes(ctx, prefix, cursor, limit)
}

func maintenanceIDs(f fixture, count int) []ID {
	ids := make([]ID, count)
	for i := range ids {
		ids[i] = f.id
		ids[i].Key = fmt.Sprintf("maintenance-%d", i)
	}
	slices.SortFunc(ids, func(a, b ID) int { return strings.Compare(a.prefix(), b.prefix()) })
	return ids
}

func seedMaintenanceEntity(t *testing.T, m *Manager, id ID, count int) {
	t.Helper()
	claim, err := m.Acquire(t.Context(), id, "seed")
	if err != nil {
		t.Fatal(err)
	}
	for i := range count {
		if _, err := m.Execute(t.Context(), claim, request(fmt.Sprint(i)), increment); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Release(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
}

func TestMaintenanceFairRotationResumesAcrossManagersAndSkipsBusy(t *testing.T) {
	f := newFixture(t)
	if err := f.manager.Release(t.Context(), f.claim); err != nil {
		t.Fatal(err)
	}
	ids := maintenanceIDs(f, api.DurableEntityMaintenanceScanPageSize+2)
	for i, id := range ids {
		count := 2
		if i == 0 {
			count = 25 // Several cleanup pages, but only one per rotation.
		}
		seedMaintenanceEntity(t, f.manager, id, count)
	}
	busy, err := f.manager.Acquire(t.Context(), ids[0], "caller")
	if err != nil {
		t.Fatal(err)
	}
	visits := map[string][]string{}
	store := maintenanceFaultStore{cleanupFaultStore: cleanupFaultStore{memoryStore: f.store,
		list: func(ctx context.Context, prefix, cursor string, limit int32) (CleanupObjects, error) {
			if limit != api.DurableEntityCleanupPageSize {
				t.Fatal("unbounded page", limit)
			}
			visits[prefix] = append(visits[prefix], cursor)
			return f.store.ListEntityObjects(ctx, prefix, cursor, limit)
		},
	}}
	apps := map[string]bool{f.id.AppID: true}
	busySeen, deleted, completed := 0, 0, 0
	for step := range 100 {
		// Reconstruct every visit: neither scan nor entity cursors live in RAM.
		m := openManager(t, store, f.clock)
		result, err := m.MaintenanceStep(t.Context(), fmt.Sprintf("worker-%d", step), apps)
		if err != nil || result.Failed != 0 || result.Cleanup.Failed != 0 || result.Visited > 1 {
			t.Fatal(step, result, err)
		}
		busySeen += result.Busy
		deleted += result.Cleanup.Deleted
		if result.SweepCompleted {
			completed++
		}
		if step == len(ids)+1 {
			if err := m.Release(t.Context(), busy); err != nil {
				t.Fatal(err)
			}
		}
	}
	if busySeen == 0 || deleted == 0 || completed < 5 {
		t.Fatal("no rotation, skip or collection", busySeen, deleted, completed)
	}
	for _, id := range ids {
		if len(visits[id.prefix()]) < 5 {
			t.Fatal("large entity monopolized rotation", id, visits)
		}
		count := 2
		if id == ids[0] {
			count = 25
			if len(visits[id.prefix()]) < 2 || visits[id.prefix()][1] == "" {
				t.Fatal("lost per-entity pagination on restart", visits[id.prefix()])
			}
		}
		assertCount(t, t.Context(), f.manager, id, count, uint64(count))
		for i := range count {
			result, err := f.manager.Invoke(t.Context(), id, "replay", request(fmt.Sprint(i)), increment)
			if err != nil || !result.Replayed || result.Version != uint64(i+1) {
				t.Fatal("cleanup lost replay", id, i, result, err)
			}
		}
	}
}

func TestMaintenanceCompetingWorkersAndExpiredOwnerRecovery(t *testing.T) {
	for _, takeover := range []bool{false, true} {
		t.Run(fmt.Sprint(takeover), func(t *testing.T) {
			f := newFixture(t)
			if err := f.manager.Release(t.Context(), f.claim); err != nil {
				t.Fatal(err)
			}
			seedMaintenanceEntity(t, f.manager, f.id, 3)
			apps := map[string]bool{f.id.AppID: true}
			other := openManager(t, f.store, f.clock)
			interleaved := false
			store := maintenanceFaultStore{cleanupFaultStore: cleanupFaultStore{memoryStore: f.store,
				list: func(ctx context.Context, prefix, cursor string, limit int32) (CleanupObjects, error) {
					if !interleaved {
						interleaved = true
						if takeover {
							f.clock.Add(int64(api.MaxDurableEntityLease + time.Second))
						}
						result, err := other.MaintenanceStep(ctx, "successor", apps)
						if takeover && (err != nil || !result.Recovered || result.Failed > 0) || !takeover && !errors.Is(err, ErrBusy) {
							t.Fatal(result, err)
						}
					}
					return f.store.ListEntityObjects(ctx, prefix, cursor, limit)
				},
			}}
			m := openManager(t, store, f.clock)
			_, err := m.MaintenanceStep(t.Context(), "first", apps)
			if !interleaved || takeover && !errors.Is(err, ErrStaleOwner) || !takeover && err != nil {
				t.Fatal(interleaved, err)
			}
			assertCount(t, t.Context(), other, f.id, 3, 3)
		})
	}
}

func TestMaintenanceLostCheckpointAcknowledgements(t *testing.T) {
	for _, phase := range []string{"claim", "finish", "finish-rejected"} {
		t.Run(phase, func(t *testing.T) {
			f := newFixture(t)
			if err := f.manager.Release(t.Context(), f.claim); err != nil {
				t.Fatal(err)
			}
			id := f.id
			seedMaintenanceEntity(t, f.manager, id, 3)
			apps := map[string]bool{f.id.AppID: true}
			lost, deletes := false, 0
			store := maintenanceFaultStore{cleanupFaultStore: cleanupFaultStore{memoryStore: f.store,
				put: func(ctx context.Context, key string, body []byte, etag string) (string, error) {
					if strings.HasPrefix(key, "gregale/durable-entities/v1/maintenance/") && phase == "finish-rejected" && !lost {
						var checkpoint maintenanceCheckpoint
						if err := json.Unmarshal(body, &checkpoint); err != nil {
							t.Fatal(err)
						}
						if checkpoint.Owner == "" {
							lost = true
							return "", errors.New("crashed before saving checkpoint progress")
						}
					}
					version, err := f.store.Put(ctx, key, body, etag)
					if err == nil && strings.HasPrefix(key, "gregale/durable-entities/v1/maintenance/") && !lost {
						var checkpoint maintenanceCheckpoint
						if err := json.Unmarshal(body, &checkpoint); err != nil {
							t.Fatal(err)
						}
						if phase == "claim" && checkpoint.Owner != "" || phase == "finish" && checkpoint.Owner == "" {
							lost = true
							return "", errors.New("saved checkpoint response lost")
						}
					}
					return version, err
				}, remove: func(ctx context.Context, key string) error {
					deletes++
					return f.store.DeleteEntityObject(ctx, key)
				},
			}}
			m := openManager(t, store, f.clock)
			if _, err := m.MaintenanceStep(t.Context(), "lost-response", apps); !errors.Is(err, ErrUncertain) || !lost {
				t.Fatal(lost, err)
			}
			if phase == "claim" || phase == "finish-rejected" {
				if phase == "claim" && deletes != 0 {
					t.Fatal("uncertain authority deleted objects")
				}
				if _, err := m.MaintenanceStep(t.Context(), "retry", apps); !errors.Is(err, ErrBusy) {
					t.Fatal(err)
				}
				f.clock.Add(int64(api.MaxDurableEntityLease + time.Second))
			}
			result, err := m.MaintenanceStep(t.Context(), "restarted", apps)
			if err != nil || result.Failed != 0 || result.Recovered != (phase != "finish") {
				t.Fatal(result, err)
			}
			assertCount(t, t.Context(), m, id, 3, 3)
		})
	}
}
