// adr: 733
package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func requestCapture(t *testing.T, store *state.MemStore, ins state.Instance) state.CrashCapture {
	t.Helper()
	app, err := store.AppByID(context.Background(), ins.AppID)
	if err != nil {
		t.Fatal(err)
	}
	c, err := store.RequestManualCrashCapture(context.Background(), app.AccountID, ins.AppID, time.Minute, time.Now())
	if err != nil {
		t.Fatalf("RequestManualCrashCapture: %v", err)
	}
	claimed, err := store.ClaimNextCrashCapture(context.Background(), time.Now())
	if err != nil || claimed.ID != c.ID {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	return claimed
}

// A capture snapshots the running instance in place: the instance keeps
// serving and no snapshots row appears, so no wake can restore it.
func TestCaptureCrash_SnapshotsInPlaceWithoutASnapshotRow(t *testing.T) {
	store := state.NewMemStore()
	rec := &recordingStopVMM{fakeVMM: &fakeVMM{}}
	e := newEngine(t, store, rec, &fakeNotifier{}, "1.10.0")
	ins := seedRunningInstanceWithMode(t, e, store, string(state.InstanceModeNormal))
	capture := requestCapture(t, store, ins)

	done, err := e.CaptureCrash(context.Background(), capture)
	if err != nil {
		t.Fatalf("CaptureCrash: %v", err)
	}
	if done.ID != capture.ID || done.StorageKey == "" || done.VMStateStorageKey == "" || done.FCVersion != "1.10.0" {
		t.Fatalf("capture result = %+v", done)
	}
	if !done.ExpiresAt.After(done.CapturedAt.Add(api.CrashCaptureRetention - time.Minute)) {
		t.Errorf("expires_at %v is not retention after %v", done.ExpiresAt, done.CapturedAt)
	}
	rec.mu.Lock()
	warm, destroys := rec.warmSnapshots, rec.destroys
	rec.mu.Unlock()
	if warm != 1 || destroys != 0 {
		t.Fatalf("warm snapshots=%d destroys=%d, want one in-place capture", warm, destroys)
	}
	if got, _ := store.InstanceByID(context.Background(), ins.ID); got.State != string(state.StateRunning) {
		t.Errorf("instance state after capture = %s, want running", got.State)
	}
	for _, tier := range []string{state.SnapshotTierWarm, state.SnapshotTierInit} {
		if _, err := store.LatestSnapshotForTier(context.Background(), ins.DeploymentID, tier); !errors.Is(err, state.ErrNotFound) {
			t.Errorf("a %s snapshots row exists after a crash capture (err=%v)", tier, err)
		}
	}
}

func TestCaptureCrash_RefusesAForkOrStoppedInstance(t *testing.T) {
	store := state.NewMemStore()
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	ins := seedRunningInstanceWithMode(t, e, store, string(state.InstanceModeNormal))
	capture := requestCapture(t, store, ins)
	e.transition(context.Background(), ins.ID, ins.AppID, state.StateStopped)
	if _, err := e.CaptureCrash(context.Background(), capture); !errors.Is(err, ErrCrashInstanceGone) {
		t.Fatalf("stopped instance err = %v, want ErrCrashInstanceGone", err)
	}
	capture.InstanceID = "missing"
	if _, err := e.CaptureCrash(context.Background(), capture); !errors.Is(err, ErrCrashInstanceGone) {
		t.Fatalf("missing instance err = %v, want ErrCrashInstanceGone", err)
	}
}

type fakeCrashRuntime struct{ err error }

func (f fakeCrashRuntime) CaptureCrash(_ context.Context, c state.CrashCapture) (state.CompleteCrashCaptureParams, error) {
	if f.err != nil {
		return state.CompleteCrashCaptureParams{}, f.err
	}
	now := time.Now()
	return state.CompleteCrashCaptureParams{ID: c.ID, StorageKey: "snap/d/warm/captures/" + c.ID + "/v2/mem",
		VMStateStorageKey: "snap/d/warm/captures/" + c.ID + "/v2/vmstate", FCVersion: "1.10.0", MemBytes: 1,
		CapturedAt: now, ExpiresAt: now.Add(time.Hour)}, nil
}

func TestCrashCaptureCoordinator_ReadyAndFailed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		want     state.CrashCaptureStatus
		wantCode string
	}{
		{"captured", nil, state.CrashCaptureReady, ""},
		{"instance gone", ErrCrashInstanceGone, state.CrashCaptureFailed, "instance_gone"},
		{"vmm error", errors.New("boom"), state.CrashCaptureFailed, "capture_failed"},
		{"remote storage", ErrCrashStorageRemote, state.CrashCaptureFailed, "storage_unsupported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			acct, app, dep := seedApp(t, store, api.PlanPro, 256, 1)
			if _, err := store.CreateInstanceWithMode(context.Background(), app.ID, dep.ID, string(state.StateRunning), 256, "node-1", "wake-1", string(state.InstanceModeNormal)); err != nil {
				t.Fatal(err)
			}
			c, err := store.RequestManualCrashCapture(context.Background(), acct.ID, app.ID, time.Minute, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			NewCrashCaptureCoordinator(store, fakeCrashRuntime{err: tc.err}, time.Second, nil).Tick(context.Background())
			got, err := store.CrashCaptureByID(context.Background(), acct.ID, app.ID, c.ID)
			if err != nil || got.Status != tc.want || (tc.wantCode != "" && (got.FailureCode == nil || *got.FailureCode != tc.wantCode)) {
				t.Fatalf("capture = %+v, %v; want %s %s", got, err, tc.want, tc.wantCode)
			}
		})
	}
}

// A fork pinned to a crash capture restores that capture, not the
// deployment's newest snapshot.
func TestRestoreFork_FromACrashCapture(t *testing.T) {
	store := state.NewMemStore()
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	fork := seedForkTarget(t, store, true) // also seeds an ordinary init snapshot
	app, _ := store.AppByID(context.Background(), fork.AppID)
	ins, err := store.CreateInstanceWithMode(context.Background(), fork.AppID, fork.DeploymentID, string(state.StateRunning), 256, "node-1", "wake-1", string(state.InstanceModeNormal))
	if err != nil {
		t.Fatal(err)
	}
	capture := requestCapture(t, store, ins)
	done, _ := fakeCrashRuntime{}.CaptureCrash(context.Background(), capture)
	if _, err := store.CompleteCrashCapture(context.Background(), done); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RequestAppForkCancellation(context.Background(), app.AccountID, app.ID, fork.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, step := range []func(context.Context, string, time.Time) (state.CrashCapture, error){
		func(ctx context.Context, id string, at time.Time) (state.CrashCapture, error) {
			return store.MarkCrashCaptureEncrypted(ctx, id, []byte("sealed"), at)
		},
		store.FinishCrashCapturePurge,
	} {
		if _, err := step(context.Background(), capture.ID, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	pinned, err := store.CreateAppFork(context.Background(), state.CreateAppForkParams{
		AccountID: app.AccountID, AppID: app.ID, CrashCaptureID: capture.ID, RequestedBy: "user:test",
		TTLSeconds: 600, MaxPerApp: 1, MaxPerAccount: 2, CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("fork of capture: %v", err)
	}
	// Encrypted and purged before the fork (ADR-733): nothing to restore
	// until imaged stages it.
	if _, err := e.RestoreFork(context.Background(), pinned); !errors.Is(err, ErrForkNoCapture) {
		t.Fatalf("RestoreFork of a sealed capture err = %v, want ErrForkNoCapture", err)
	}
	for _, step := range []func(context.Context, string, time.Time) (state.CrashCapture, error){
		store.BeginCrashCaptureStage, store.FinishCrashCaptureStage,
	} {
		if _, err := step(context.Background(), capture.ID, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.RestoreFork(context.Background(), pinned); err != nil {
		t.Fatalf("RestoreFork: %v", err)
	}
	vmm.mu.Lock()
	ref := vmm.lastSnapRef
	vmm.mu.Unlock()
	if ref.StorageKey != done.StorageKey {
		t.Fatalf("restored %q, want the crash capture %q", ref.StorageKey, done.StorageKey)
	}
}
