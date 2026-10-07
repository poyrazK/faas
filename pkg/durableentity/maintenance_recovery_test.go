// adr: 638
package durableentity

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMaintenanceExpiredCursorsRestartWithoutDroppingCurrentRoots(t *testing.T) {
	f := newFixture(t)
	if _, err := f.manager.Execute(t.Context(), f.claim, request("one"), increment); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Release(t.Context(), f.claim); err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.putMaintenanceObject(t.Context(), f.id.prefix()+"maintenance.json", cleanupHint{Schema: 1, Entity: f.id, Cursor: "expired"}, ""); err != nil {
		t.Fatal(err)
	}
	store := maintenanceFaultStore{cleanupFaultStore: cleanupFaultStore{memoryStore: f.store,
		list: func(ctx context.Context, prefix, cursor string, limit int32) (CleanupObjects, error) {
			if cursor == "expired" {
				return CleanupObjects{}, ErrInvalid
			}
			return f.store.ListEntityObjects(ctx, prefix, cursor, limit)
		},
	}, prefixes: func(ctx context.Context, prefix, cursor string, limit int32) (EntityPrefixPage, error) {
		if cursor == "expired" {
			return EntityPrefixPage{}, ErrInvalid
		}
		return f.store.ListEntityPrefixes(ctx, prefix, cursor, limit)
	}}
	m := openManager(t, store, f.clock)
	apps := map[string]bool{f.id.AppID: true}
	result, err := m.MaintenanceStep(t.Context(), "worker", apps)
	if err != nil || result.Failed != 1 {
		t.Fatal(result, err)
	}
	hint, _, err := m.readCleanupHint(t.Context(), f.id)
	if err != nil || hint.Cursor != "" {
		t.Fatal(hint, err)
	}
	result, err = m.MaintenanceStep(t.Context(), "restart", apps)
	if err != nil || result.Failed != 0 {
		t.Fatal(result, err)
	}
	scope, _ := maintenanceScope(apps)
	body, etag, err := f.store.Get(t.Context(), maintenanceKey(scope), api.MaxDurableEntityMaintenanceBytes)
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint maintenanceCheckpoint
	if err := json.Unmarshal(body, &checkpoint); err != nil {
		t.Fatal(err)
	}
	checkpoint.Cursor = "expired"
	if _, err := m.putMaintenanceObject(t.Context(), maintenanceKey(scope), checkpoint, etag); err != nil {
		t.Fatal(err)
	}
	if _, err := m.MaintenanceStep(t.Context(), "expired-directory-cursor", apps); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	result, err = m.MaintenanceStep(t.Context(), "restart-directory", apps)
	if err != nil || result.Failed != 0 || !result.SweepCompleted {
		t.Fatal(result, err)
	}
	assertCount(t, t.Context(), m, f.id, 1, 1)
	resultReplay, err := m.Invoke(t.Context(), f.id, "replay", request("one"), increment)
	if err != nil || !resultReplay.Replayed || resultReplay.Version != 1 {
		t.Fatal(resultReplay, err)
	}
}

func TestMaintenanceDisallowedAndCorruptEntitiesDoNotBlockOthers(t *testing.T) {
	f := newFixture(t)
	if err := f.manager.Release(t.Context(), f.claim); err != nil {
		t.Fatal(err)
	}
	ids := maintenanceIDs(f, 3)
	ids[1].AppID = "disallowed"
	for _, id := range ids {
		seedMaintenanceEntity(t, f.manager, id, 3)
	}
	base, etag, err := f.manager.readManifest(t.Context(), ids[0])
	if err != nil {
		t.Fatal(err)
	}
	base.SnapshotHash = strings.Repeat("0", sha256HexLength)
	if err := f.manager.putManifest(t.Context(), base, etag); err != nil {
		t.Fatal(err)
	}
	before, _, err := f.store.Get(t.Context(), ids[1].prefix()+"manifest.json", api.MaxDurableEntityManifestBytes)
	if err != nil {
		t.Fatal(err)
	}
	skipped, failed, deleted := 0, 0, 0
	for range 4 {
		result, err := f.manager.MaintenanceStep(t.Context(), "worker", map[string]bool{f.id.AppID: true})
		if err != nil {
			t.Fatal(result, err)
		}
		skipped += result.Skipped
		failed += result.Failed
		deleted += result.Cleanup.Deleted
	}
	if skipped != 1 || failed != 1 || deleted == 0 {
		t.Fatal(skipped, failed, deleted)
	}
	after, _, err := f.store.Get(t.Context(), ids[1].prefix()+"manifest.json", api.MaxDurableEntityManifestBytes)
	if err != nil || string(before) != string(after) {
		t.Fatal("maintenance touched disallowed state", err)
	}
	assertCount(t, t.Context(), f.manager, ids[2], 3, 3)
}

func TestMaintenancePermissionProbeAndInvalidConfigurationFailClosed(t *testing.T) {
	f := newFixture(t)
	deletes := 0
	store := maintenanceFaultStore{cleanupFaultStore: cleanupFaultStore{memoryStore: f.store,
		remove: func(ctx context.Context, key string) error {
			if !strings.HasPrefix(key, "gregale/durable-entities/v1/probes/maintenance/") {
				t.Fatal("startup deleted entity data", key)
			}
			deletes++
			return f.store.DeleteEntityObject(ctx, key)
		},
	}}
	if err := openManager(t, store, f.clock).CheckMaintenance(t.Context()); err != nil || deletes != 1 {
		t.Fatal(deletes, err)
	}
	store.remove = func(context.Context, string) error { return errors.New("permission denied") }
	if err := openManager(t, store, f.clock).CheckMaintenance(t.Context()); err == nil {
		t.Fatal("DELETE failure ignored")
	}
	plain := wrappedStore{ObjectStore: f.store, put: f.store.Put}
	if err := openManager(t, plain, f.clock).CheckMaintenance(t.Context()); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	for _, apps := range []map[string]bool{nil, {}, {"": true}, {"app": false}} {
		if _, err := f.manager.MaintenanceStep(t.Context(), "worker", apps); !errors.Is(err, ErrInvalid) {
			t.Fatal(apps, err)
		}
	}
	scope, _ := maintenanceScope(map[string]bool{"app-a": true})
	if _, err := f.store.Put(t.Context(), maintenanceKey(scope), []byte(`{"schema":1,"scope":"wrong"}`), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.MaintenanceStep(t.Context(), "worker", map[string]bool{"app-a": true}); !errors.Is(err, ErrCorrupt) {
		t.Fatal(err)
	}
}
