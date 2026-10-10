package state_test

// adr: 733
// Sealed captures: vmmd encrypted them at the source, so they are ready
// and sealed from the start, a fork claims them at once (the restoring vmmd
// decrypts), imaged never encrypts, purges or stages them, and expiry
// drops the key.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestCrashCapture_SealedAtSource(t *testing.T) {
	for name, f := range appForkStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
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
				FCVersion: "1.7.0", MemBytes: 1 << 20, CapturedAt: forkT0.Add(time.Second), ExpiresAt: forkT0.Add(time.Hour),
				SealedKey: []byte("sealed-at-source"),
			})
			if err != nil || !ready.Sealed() || string(ready.SealedKey) != "sealed-at-source" || ready.EncryptedAt == nil || !ready.PlaintextReadable() {
				t.Fatalf("sealed completion = %+v, %v", ready, err)
			}

			at := forkT0.Add(2 * time.Second)
			for label, list := range map[string]func() ([]state.CrashCapture, error){
				"encrypt": func() ([]state.CrashCapture, error) { return f.store.CrashCapturesToEncrypt(ctx, at, 10) },
				"purge":   func() ([]state.CrashCapture, error) { return f.store.CrashCapturesToPurge(ctx, 10) },
				"stage":   func() ([]state.CrashCapture, error) { return f.store.CrashCapturesToStage(ctx, at, 10) },
			} {
				if due, err := list(); err != nil || len(due) != 0 {
					t.Fatalf("imaged %s queue = %+v, %v; want a sealed capture left alone", label, due, err)
				}
			}

			p := f.params(at)
			p.CrashCaptureID, p.DeploymentID = ready.ID, ""
			fork, err := f.store.CreateAppFork(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			claimed, err := f.store.ClaimNextAppFork(ctx, "sched-a", at.Add(time.Second), time.Minute)
			if err != nil || claimed.ID != fork.ID {
				t.Fatalf("claim of a fork on a sealed capture = %+v, %v; want it claimable at once", claimed, err)
			}
			if due, err := f.store.CrashCapturesToStage(ctx, at.Add(time.Second), 10); err != nil || len(due) != 0 {
				t.Fatalf("stage queue under an active fork = %+v, %v; want none", due, err)
			}
			if _, err := f.store.BeginCrashCapturePurge(ctx, ready.ID, at.Add(2*time.Second)); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("purge of a sealed capture err = %v, want ErrNotFound", err)
			}

			expired, err := f.store.ExpireCrashCapture(ctx, ready.ID, forkT0.Add(2*time.Hour))
			if err != nil || expired.SealedKey != nil || expired.PlaintextState != state.CrashPlaintextAbsent {
				t.Fatalf("expired = %+v, %v", expired, err)
			}
		})
	}
}
