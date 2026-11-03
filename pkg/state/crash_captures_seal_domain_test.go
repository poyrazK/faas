package state_test

// adr: 733
// What the build seal domain's contract check counts (ADR 20261022111812):
// an unexpired capture imaged would still encrypt or stage with the fleet
// key, never one vmmd sealed at the source.

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestCountImagedKeyedCrashCaptures(t *testing.T) {
	for name, f := range appForkStores(t) {
		counter, ok := f.store.(interface {
			CountImagedKeyedCrashCaptures(context.Context) (int, error)
		})
		if !ok {
			continue // the count is a seal-domain query on PgStore alone
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			count := func() int {
				t.Helper()
				n, err := counter.CountImagedKeyedCrashCaptures(ctx)
				if err != nil {
					t.Fatal(err)
				}
				return n
			}
			f.runningInstance(t, state.InstanceModeNormal)
			requested, err := f.store.RequestManualCrashCapture(ctx, f.accountID, f.appID, crashCooldown, forkT0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.ClaimNextCrashCapture(ctx, forkT0); err != nil {
				t.Fatal(err)
			}
			if n := count(); n != 0 {
				t.Fatalf("count with the capture still capturing = %d, want 0", n)
			}
			// No sealed key: captured in plaintext, which imaged would
			// encrypt to the fleet recipient.
			ready, err := f.store.CompleteCrashCapture(ctx, state.CompleteCrashCaptureParams{
				ID: requested.ID, StorageKey: "snap/d/warm/captures/c/v2/mem", VMStateStorageKey: "snap/d/warm/captures/c/v2/vmstate",
				FCVersion: "1.7.0", MemBytes: 1 << 20, CapturedAt: forkT0.Add(time.Second), ExpiresAt: time.Now().Add(time.Hour),
			})
			if err != nil || ready.PlaintextState != state.CrashPlaintextPresent {
				t.Fatalf("plaintext completion = %+v, %v", ready, err)
			}
			if n := count(); n != 1 {
				t.Fatalf("count with a plaintext capture = %d, want 1", n)
			}
			if _, err := f.store.ExpireCrashCapture(ctx, ready.ID, time.Now()); err != nil {
				t.Fatal(err)
			}
			if n := count(); n != 0 {
				t.Fatalf("count once the capture expired = %d, want 0", n)
			}
		})
	}
}

// TestRuntimeCheckQueries pins what the runtime seal domain's contract check
// reads (ADR 20261022111812): the keys of unexpired captures vmmd sealed at
// the source, and the short-lived copies filed under another key id.
func TestRuntimeCheckQueries(t *testing.T) {
	for name, f := range appForkStores(t) {
		reader, ok := f.store.(interface {
			ListSealedCrashCaptureKeys(context.Context, string, int) ([]state.CrashCaptureKey, error)
			CountRuntimeCopiesOffDomain(context.Context, string) (int, error)
		})
		if !ok {
			continue // seal-domain queries on PgStore alone
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			if n, err := reader.CountRuntimeCopiesOffDomain(ctx, "age1domain"); err != nil || n != 0 {
				t.Fatalf("copies with no guard or cutover = %d, %v; want 0", n, err)
			}
			f.runningInstance(t, state.InstanceModeNormal)
			requested, err := f.store.RequestManualCrashCapture(ctx, f.accountID, f.appID, crashCooldown, forkT0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.ClaimNextCrashCapture(ctx, forkT0); err != nil {
				t.Fatal(err)
			}
			ready, err := f.store.CompleteCrashCapture(ctx, state.CompleteCrashCaptureParams{
				ID: requested.ID, StorageKey: "snap/d/warm/captures/c/v2/mem", VMStateStorageKey: "snap/d/warm/captures/c/v2/vmstate",
				FCVersion: "1.7.0", MemBytes: 1 << 20, CapturedAt: forkT0.Add(time.Second), ExpiresAt: time.Now().Add(time.Hour),
				SealedKey: []byte("sealed-at-source"),
			})
			if err != nil || !ready.Sealed() {
				t.Fatalf("sealed completion = %+v, %v", ready, err)
			}
			keys, err := reader.ListSealedCrashCaptureKeys(ctx, "", 10)
			if err != nil || len(keys) != 1 || keys[0].ID != ready.ID || string(keys[0].SealedKey) != "sealed-at-source" {
				t.Fatalf("sealed capture keys = %+v, %v; want the capture's key", keys, err)
			}
			if _, err := f.store.ExpireCrashCapture(ctx, ready.ID, time.Now()); err != nil {
				t.Fatal(err)
			}
			if keys, err := reader.ListSealedCrashCaptureKeys(ctx, "", 10); err != nil || len(keys) != 0 {
				t.Fatalf("keys once the capture expired = %+v, %v; want none", keys, err)
			}
		})
	}
}
