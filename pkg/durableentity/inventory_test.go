// adr: 712
package durableentity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func forgetStorageAccounting(t *testing.T, f fixture) {
	t.Helper()
	base, etag, err := f.manager.readManifest(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	base.Schema, base.StorageUsage, base.StorageLimitBytes = 2, nil, 0
	body, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Put(t.Context(), f.id.prefix()+"manifest.json", body, etag); err != nil {
		t.Fatal(err)
	}
}

func TestInventoryBootstrapsLegacyRootInBoundedRestartablePages(t *testing.T) {
	f := newFixture(t)
	for i := range 70 {
		if _, err := f.manager.Execute(t.Context(), f.claim, request(fmt.Sprint(i)), increment); err != nil {
			t.Fatal(err)
		}
	}
	want := reachableUsage(t, f)
	forgetStorageAccounting(t, f)
	if err := f.manager.SetStorageLimit(t.Context(), f.claim, want.TotalBytes()+10000); err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.Execute(t.Context(), f.claim, request("new"), func(context.Context, View) (Transition, error) {
		t.Fatal("new work ran before legacy accounting completed")
		return Transition{}, nil
	}); !errors.Is(err, ErrInventoryPending) {
		t.Fatal(err)
	}
	if result, err := f.manager.Execute(t.Context(), f.claim, request("0"), increment); err != nil || !result.Replayed {
		t.Fatal(result, err)
	}
	first, err := f.manager.Inventory(t.Context(), f.claim)
	if err != nil || first.LogicalComplete || first.Complete || first.Usage.ReceiptCount >= want.ReceiptCount {
		t.Fatal(first, err)
	}
	result := inventoryAll(t, f, f.store)
	base, _, err := f.manager.readManifest(t.Context(), f.id)
	if err != nil || !sameUsage(want, result.Usage) || base.StorageUsage == nil || !sameUsage(want, *base.StorageUsage) {
		t.Fatal(result, base.StorageUsage, err)
	}
	if _, err := f.manager.Execute(t.Context(), f.claim, request("new"), increment); err != nil {
		t.Fatal(err)
	}
}

func TestInventoryAndAccountingMigrateInlineLegacyReceipts(t *testing.T) {
	f := newFixture(t)
	state := snapshot{Schema: 1, ID: f.id, Version: 1, Data: json.RawMessage(`{"count":1}`), Receipts: map[string]receipt{
		"old": {Fingerprint: digest(request("old").Payload), Result: json.RawMessage(`1`), Version: 1},
	}}
	body, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	base, etag, err := f.manager.readManifest(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	base.Version, base.SnapshotKey, base.SnapshotHash, base.StorageUsage = 1, f.id.prefix()+"snapshots/"+uuid.NewString()+".json", digest(body), nil
	if _, err := f.store.Put(t.Context(), base.SnapshotKey, body, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.putManifest(t.Context(), base, etag); err != nil {
		t.Fatal(err)
	}
	result := inventoryAll(t, f, f.store)
	if result.Usage.ReceiptCount != 1 || result.Usage.ReceiptBytes != 0 || result.Usage.SnapshotBytes != int64(len(body)) {
		t.Fatal(result)
	}
	if _, err := f.manager.Execute(t.Context(), f.claim, request("new"), increment); err != nil {
		t.Fatal(err)
	}
	result = inventoryAll(t, f, f.store)
	base, _, err = f.manager.readManifest(t.Context(), f.id)
	if err != nil || result.Usage.LegacyBytes == 0 || result.Usage.ReceiptCount != 2 || !sameUsage(result.Usage, reachableUsage(t, f)) || !sameUsage(result.Usage, *base.StorageUsage) {
		t.Fatal(result, err)
	}
	if replay, err := f.manager.Execute(t.Context(), f.claim, request("old"), increment); err != nil || !replay.Replayed {
		t.Fatal(replay, err)
	}
}

func TestInventoryRootChangeRestartsPartialProof(t *testing.T) {
	f := newFixture(t)
	for i := range 50 {
		if _, err := f.manager.Execute(t.Context(), f.claim, request(fmt.Sprint(i)), increment); err != nil {
			t.Fatal(err)
		}
	}
	forgetStorageAccounting(t, f)
	if result, err := f.manager.Inventory(t.Context(), f.claim); err != nil || result.LogicalComplete {
		t.Fatal(result, err)
	}
	if _, err := f.manager.Execute(t.Context(), f.claim, request("changes-root"), increment); err != nil {
		t.Fatal(err)
	}
	collectAll(t, f.manager, f.claim) // Old partial proof objects may now be gone.
	result := inventoryAll(t, f, f.store)
	if result.Version != 51 || result.Usage.ReceiptCount != 51 || !sameUsage(result.Usage, reachableUsage(t, f)) {
		t.Fatal(result)
	}
}

func TestInventoryDoesNotUseListForCommittedUsage(t *testing.T) {
	for _, quality := range []string{"inflated", "missing", "unsupported", "negative"} {
		t.Run(quality, func(t *testing.T) {
			f := newFixture(t)
			if _, err := f.manager.Execute(t.Context(), f.claim, request("first"), increment); err != nil {
				t.Fatal(err)
			}
			want := reachableUsage(t, f)
			store := cleanupFaultStore{memoryStore: f.store, list: func(ctx context.Context, prefix, cursor string, limit int32) (CleanupObjects, error) {
				if quality == "unsupported" {
					return CleanupObjects{}, ErrUnsupported
				}
				page, err := f.store.ListEntityObjects(ctx, prefix, cursor, limit)
				for _, key := range page.Keys {
					switch quality {
					case "inflated":
						page.Sizes[key] = 500000000
					case "negative":
						page.Sizes[key] = -1
					case "missing":
						delete(page.Sizes, key)
					}
				}
				return page, err
			}, remove: func(context.Context, string) error { t.Fatal("inventory deleted an object"); return nil }}
			result := inventoryAll(t, f, store)
			if !sameUsage(want, result.Usage) || result.CurrentBytesKnown != (quality == "inflated") {
				t.Fatal(result)
			}
		})
	}
}

func TestInventoryUncertainCheckpointWriteResumesWithoutDoubleCounting(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(fmt.Sprint(accepted), func(t *testing.T) {
			f := newFixture(t)
			for i := range 50 {
				if _, err := f.manager.Execute(t.Context(), f.claim, request(fmt.Sprint(i)), increment); err != nil {
					t.Fatal(err)
				}
			}
			forgetStorageAccounting(t, f)
			store := wrappedStore{ObjectStore: f.store, put: func(ctx context.Context, key string, body []byte, etag string) (string, error) {
				if strings.HasSuffix(key, "/inventory.json") {
					if accepted {
						if _, err := f.store.Put(ctx, key, body, etag); err != nil {
							return "", err
						}
					}
					return "", errors.New("lost response")
				}
				return f.store.Put(ctx, key, body, etag)
			}}
			if _, err := openManager(t, store, f.clock).Inventory(t.Context(), f.claim); !errors.Is(err, ErrUncertain) {
				t.Fatal(err)
			}
			result := inventoryAll(t, f, f.store)
			if result.Usage.ReceiptCount != 50 || !sameUsage(result.Usage, reachableUsage(t, f)) {
				t.Fatal(result)
			}
		})
	}
}

func TestInventoryCorruptionFailsClosed(t *testing.T) {
	for _, target := range []string{"leaf", "checkpoint", "usage"} {
		t.Run(target, func(t *testing.T) {
			f := newFixture(t)
			if _, err := f.manager.Execute(t.Context(), f.claim, request("one"), increment); err != nil {
				t.Fatal(err)
			}
			if _, err := f.manager.Inventory(t.Context(), f.claim); err != nil {
				t.Fatal(err)
			}
			base, etag, err := f.manager.readManifest(t.Context(), f.id)
			if err != nil {
				t.Fatal(err)
			}
			switch target {
			case "usage":
				base.StorageUsage.ReceiptBytes++
				if err := f.manager.putManifest(t.Context(), base, etag); err != nil {
					t.Fatal(err)
				}
			case "checkpoint":
				f.store.mu.Lock()
				f.store.objects[f.id.prefix()+"inventory.json"] = memoryObject{body: []byte(`{}`), version: "bad"}
				f.store.mu.Unlock()
			case "leaf":
				state, err := f.manager.readSnapshot(t.Context(), base)
				if err != nil {
					t.Fatal(err)
				}
				f.store.mu.Lock()
				delete(f.store.objects, state.ReceiptRoot.Key)
				delete(f.store.objects, f.id.prefix()+"inventory.json")
				f.store.mu.Unlock()
			}
			if _, err := f.manager.Inventory(t.Context(), f.claim); !errors.Is(err, ErrCorrupt) {
				t.Fatal("corrupt inventory was published", err)
			}
		})
	}
}

func TestInventoryExpiredNativeCursorResetsObservationOnly(t *testing.T) {
	f := newFixture(t)
	for i := range 20 {
		if _, err := f.manager.Execute(t.Context(), f.claim, request(fmt.Sprint(i)), increment); err != nil {
			t.Fatal(err)
		}
	}
	failed := false
	store := cleanupFaultStore{memoryStore: f.store, list: func(ctx context.Context, prefix, cursor string, limit int32) (CleanupObjects, error) {
		if cursor != "" && !failed {
			failed = true
			return CleanupObjects{}, errors.New("expired native cursor")
		}
		return f.store.ListEntityObjects(ctx, prefix, cursor, limit)
	}}
	m := openManager(t, store, f.clock)
	for range 20 {
		_, err := m.Inventory(t.Context(), f.claim)
		if err != nil {
			if !failed {
				t.Fatal(err)
			}
			break
		}
	}
	if !failed {
		t.Fatal("did not paginate current keys")
	}
	base, _, err := m.readManifest(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	p, _, err := m.readInventoryProgress(t.Context(), base)
	if err != nil || !p.LogicalComplete || p.Cursor != "" || p.CurrentBytes != 0 || p.CurrentObjects != 0 {
		t.Fatal("cursor failure lost logical proof", p, err)
	}
	result := inventoryAll(t, f, store)
	if !result.CurrentBytesKnown || !sameUsage(result.Usage, reachableUsage(t, f)) {
		t.Fatal(result)
	}
}

func TestMaintenanceBootstrapsCappedLegacyEntityAndAlternatesInventory(t *testing.T) {
	f := newFixture(t)
	for i := range 50 {
		if _, err := f.manager.Execute(t.Context(), f.claim, request(fmt.Sprint(i)), increment); err != nil {
			t.Fatal(err)
		}
	}
	forgetStorageAccounting(t, f)
	if err := f.manager.SetStorageLimit(t.Context(), f.claim, 1000000); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Release(t.Context(), f.claim); err != nil {
		t.Fatal(err)
	}
	logical, complete, cleanup := false, false, false
	for step := range 50 {
		m := openManager(t, f.store, f.clock)
		result, err := m.MaintenanceStep(t.Context(), "worker", map[string]bool{f.id.AppID: true})
		if err != nil || result.Failed != 0 || result.Cleanup.Failed != 0 {
			t.Fatal(step, result, err)
		}
		if result.Inventory != nil {
			logical = logical || result.Inventory.LogicalComplete
			complete = complete || result.Inventory.Complete
		} else if result.Cleanup.Deleted+result.Cleanup.Retained > 0 {
			if !logical || !complete {
				t.Fatal("cleanup preceded legacy accounting", result)
			}
			cleanup = true
		}
		if cleanup && complete {
			break
		}
	}
	base, _, err := f.manager.readManifest(t.Context(), f.id)
	if err != nil || !cleanup || !complete || base.StorageUsage == nil || base.StorageUsage.ReceiptCount != 50 {
		t.Fatal("maintenance did not complete accounting", base.StorageUsage, cleanup, complete, err)
	}
}
