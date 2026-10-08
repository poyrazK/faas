// adr: 712
package durableentity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func inventoryAll(t *testing.T, f fixture, store ObjectStore) InventoryResult {
	t.Helper()
	for range 200 {
		// Reopen every visit to prove bounded progress survives process restart.
		result, err := openManager(t, store, f.clock).Inventory(t.Context(), f.claim)
		if err != nil {
			t.Fatal(result, err)
		}
		if result.Complete {
			return result
		}
	}
	t.Fatal("inventory did not complete")
	return InventoryResult{}
}

// Count raw reachable bodies independently of the commit delta calculation.
func reachableUsage(t *testing.T, f fixture) StorageUsage {
	t.Helper()
	base, _, err := f.manager.readManifest(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	u := StorageUsage{}
	if base.Version == 0 {
		return u
	}
	body, _, err := f.store.Get(t.Context(), base.SnapshotKey, api.MaxDurableEntitySnapshotBytes)
	if err != nil {
		t.Fatal(err)
	}
	var state snapshot
	if err := json.Unmarshal(body, &state); err != nil {
		t.Fatal(err)
	}
	u.SnapshotBytes, u.ReceiptCount = int64(len(body)), uint64(len(state.Receipts))
	if state.LegacyReceipts != nil {
		body, _, err := f.store.Get(t.Context(), state.LegacyReceipts.Key, api.MaxDurableEntitySnapshotBytes)
		if err != nil {
			t.Fatal(err)
		}
		var block legacyReceipts
		if err := json.Unmarshal(body, &block); err != nil {
			t.Fatal(err)
		}
		u.LegacyBytes, u.ReceiptCount = int64(len(body)), u.ReceiptCount+uint64(len(block.Receipts))
	}
	var visit func(journalRef)
	visit = func(ref journalRef) {
		body, _, err := f.store.Get(t.Context(), ref.Key, api.MaxDurableEntityReceiptBytes)
		if err != nil {
			t.Fatal(err)
		}
		var node journalNode
		if err := json.Unmarshal(body, &node); err != nil {
			t.Fatal(err)
		}
		if node.Receipt != nil {
			u.ReceiptBytes += int64(len(body))
			u.ReceiptCount++
			return
		}
		u.IndexBytes += int64(len(body))
		for _, child := range node.Children {
			visit(child)
		}
	}
	if state.ReceiptRoot != nil {
		visit(*state.ReceiptRoot)
	}
	return u
}

func TestStorageAccountingMatchesReachableBodiesAcrossPathCopiesAndCleanup(t *testing.T) {
	f := newFixture(t)
	for i := range 90 {
		if _, err := f.manager.Execute(t.Context(), f.claim, request(fmt.Sprint(i)), increment); err != nil {
			t.Fatal(err)
		}
		base, _, err := f.manager.readManifest(t.Context(), f.id)
		if err != nil || base.Schema != 4 || base.StorageUsage == nil || !sameUsage(*base.StorageUsage, reachableUsage(t, f)) {
			t.Fatal(i, base.StorageUsage, err)
		}
	}
	before := reachableUsage(t, f)
	if deleted := collectAll(t, f.manager, f.claim); deleted == 0 {
		t.Fatal("expected superseded objects")
	}
	result := inventoryAll(t, f, f.store)
	if !sameUsage(before, result.Usage) || !result.CurrentBytesKnown || result.CurrentBytes <= before.TotalBytes() {
		t.Fatal(result, before)
	}
}

func TestStorageLimitRejectsBeforeUploadAndReplaysAtCapacity(t *testing.T) {
	f := newFixture(t)
	first, err := f.manager.Execute(t.Context(), f.claim, request("first"), increment)
	if err != nil {
		t.Fatal(err)
	}
	usage := reachableUsage(t, f)
	if err := f.manager.SetStorageLimit(t.Context(), f.claim, usage.TotalBytes()); err != nil {
		t.Fatal(err)
	}
	uploads := 0
	store := wrappedStore{ObjectStore: f.store, put: func(ctx context.Context, key string, body []byte, etag string) (string, error) {
		uploads++
		return f.store.Put(ctx, key, body, etag)
	}}
	m := openManager(t, store, f.clock)
	uploads = 0 // Exclude startup capability probes.
	_, err = m.Execute(t.Context(), f.claim, request("second"), increment)
	var limit *LimitError
	if !errors.As(err, &limit) || limit.Limit != usage.TotalBytes() || limit.Observed <= limit.Limit || uploads != 0 {
		t.Fatal("over-cap transition uploaded or committed", uploads, err)
	}
	// The observed projection is exact, so that precise cap accepts a retry.
	if err := m.SetStorageLimit(t.Context(), f.claim, limit.Observed); err != nil {
		t.Fatal(err)
	}
	if second, err := m.Execute(t.Context(), f.claim, request("second"), increment); err != nil || second.Version != 2 {
		t.Fatal("exact boundary rejected", second, err)
	}
	if err := m.SetStorageLimit(t.Context(), f.claim, 1); err != nil {
		t.Fatal(err)
	}
	replay, err := m.Execute(t.Context(), f.claim, request("first"), func(context.Context, View) (Transition, error) {
		t.Fatal("full entity replay executed callback")
		return Transition{}, nil
	})
	if err != nil || !replay.Replayed || replay.Version != first.Version {
		t.Fatal(replay, err)
	}
	if err := m.SetStorageLimit(t.Context(), f.claim, 0); err != nil {
		t.Fatal(err)
	}
	second, err := m.Execute(t.Context(), f.claim, request("second"), increment)
	if err != nil || second.Version != 2 || !second.Replayed {
		t.Fatal(second, err)
	}
	assertCount(t, t.Context(), m, f.id, 2, 2)
}

func TestStorageCapTightenedDuringEveryPublicationPhase(t *testing.T) {
	for _, phase := range []string{"callback", "upload", "manifest-cas"} {
		t.Run(phase, func(t *testing.T) {
			f := newFixture(t)
			operator := openManager(t, f.store, f.clock)
			triggered := false
			tighten := func() {
				if !triggered {
					triggered = true
					if err := operator.SetStorageLimit(t.Context(), f.claim, 1); err != nil {
						t.Fatal(err)
					}
				}
			}
			store := wrappedStore{ObjectStore: f.store, put: func(ctx context.Context, key string, body []byte, etag string) (string, error) {
				if phase == "manifest-cas" && strings.HasSuffix(key, "/manifest.json") || phase == "upload" && strings.Contains(key, "/receipts/") {
					tighten()
				}
				return f.store.Put(ctx, key, body, etag)
			}}
			writer := openManager(t, store, f.clock)
			_, err := writer.Execute(t.Context(), f.claim, request("first"), func(ctx context.Context, v View) (Transition, error) {
				if phase == "callback" {
					tighten()
				}
				return increment(ctx, v)
			})
			if !triggered || phase == "manifest-cas" && !errors.Is(err, ErrConflict) || phase != "manifest-cas" && !errors.Is(err, ErrLimit) {
				t.Fatal(triggered, err)
			}
			assertCount(t, t.Context(), operator, f.id, 0, 0)
		})
	}
}

func TestStorageConfiguredLimitPersistsAcrossUncappedOwners(t *testing.T) {
	f := newFixture(t)
	if err := f.manager.SetStorageLimit(t.Context(), f.claim, 500); err != nil {
		t.Fatal(err)
	}
	for _, configured := range []int64{0, 800, 200} {
		if err := f.manager.Release(t.Context(), f.claim); err != nil {
			t.Fatal(err)
		}
		m, err := Open(t.Context(), f.store, Options{RetainedBytesLimit: configured, Now: func() time.Time { return time.Unix(0, f.clock.Load()) }})
		if err != nil {
			t.Fatal(err)
		}
		f.claim, err = m.Acquire(t.Context(), f.id, "next")
		if err != nil {
			t.Fatal(err)
		}
		base, _, err := m.readManifest(t.Context(), f.id)
		want := int64(500)
		if configured == 200 {
			want = 200
		}
		if err != nil || base.StorageLimitBytes != want {
			t.Fatal(base, err)
		}
		f.manager = m
	}
	bad := f.claim
	bad.Token = "obsolete"
	if err := f.manager.SetStorageLimit(t.Context(), bad, 0); !errors.Is(err, ErrStaleOwner) {
		t.Fatal(err)
	}
}

func TestStorageLimitAllowsStateShrinkAndGuardsOverflow(t *testing.T) {
	f := newFixture(t)
	large := json.RawMessage(`"` + strings.Repeat("x", 12000) + `"`)
	_, err := f.manager.Execute(t.Context(), f.claim, request("large"), func(context.Context, View) (Transition, error) {
		return Transition{Data: large, Result: json.RawMessage(`1`)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	before := reachableUsage(t, f)
	if err := f.manager.SetStorageLimit(t.Context(), f.claim, before.TotalBytes()); err != nil {
		t.Fatal(err)
	}
	_, err = f.manager.Execute(t.Context(), f.claim, request("shrink"), func(context.Context, View) (Transition, error) {
		return Transition{Data: json.RawMessage(`{}`), Result: json.RawMessage(`1`)}, nil
	})
	if err != nil || reachableUsage(t, f).TotalBytes() >= before.TotalBytes() {
		t.Fatal(err)
	}
	for _, usage := range []StorageUsage{{ReceiptBytes: math.MaxInt64}, {ReceiptCount: math.MaxUint64}, {IndexBytes: 1}} {
		delta := journalDelta{leafBytes: 1}
		if usage.IndexBytes == 1 {
			delta.indexBytes = -2
		}
		if _, err := projectedUsage(manifest{StorageUsage: &usage}, delta, 1); !errors.Is(err, ErrLimit) {
			t.Fatal("overflow or underflow accepted", usage, err)
		}
	}
}

func TestCommitUsesAccountingBootstrappedDuringCallback(t *testing.T) {
	f := newFixture(t)
	if _, err := f.manager.Execute(t.Context(), f.claim, request("first"), increment); err != nil {
		t.Fatal(err)
	}
	forgetStorageAccounting(t, f)
	operator := openManager(t, f.store, f.clock)
	_, err := f.manager.Execute(t.Context(), f.claim, request("second"), func(ctx context.Context, view View) (Transition, error) {
		result, err := operator.Inventory(ctx, f.claim)
		if err != nil || !result.LogicalComplete {
			t.Fatal(result, err)
		}
		return increment(ctx, view)
	})
	base, _, readErr := f.manager.readManifest(t.Context(), f.id)
	if err != nil || readErr != nil || base.StorageUsage == nil || base.StorageUsage.ReceiptCount != 2 || !sameUsage(*base.StorageUsage, reachableUsage(t, f)) {
		t.Fatal(base.StorageUsage, err, readErr)
	}
}

func TestStorageAccountingSurvivesLostPublicationAcknowledgement(t *testing.T) {
	f := newFixture(t)
	store := wrappedStore{ObjectStore: f.store, put: func(ctx context.Context, key string, body []byte, etag string) (string, error) {
		version, err := f.store.Put(ctx, key, body, etag)
		if err == nil && strings.HasSuffix(key, "/manifest.json") {
			return "", errors.New("publication accepted; acknowledgement lost")
		}
		return version, err
	}}
	if _, err := openManager(t, store, f.clock).Execute(t.Context(), f.claim, request("first"), increment); !errors.Is(err, ErrUncertain) {
		t.Fatal(err)
	}
	result, err := f.manager.Execute(t.Context(), f.claim, request("first"), increment)
	if err != nil || !result.Replayed || result.Version != 1 {
		t.Fatal(result, err)
	}
	usage := inventoryAll(t, f, f.store).Usage
	if usage.ReceiptCount != 1 || !sameUsage(usage, reachableUsage(t, f)) {
		t.Fatal("recovery charged the same receipt twice", usage)
	}
}
