package state_test

// adr: 733
// Crash capture rules on both stores: opt-in for the 5xx trigger, running
// non-fork instances only, one in flight, cooldown, lifecycle and forks.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

const crashCooldown = 10 * time.Minute

func (f appForkFixture) runningInstance(t *testing.T, mode state.InstanceMode) state.Instance {
	t.Helper()
	ins, err := f.store.CreateInstanceWithMode(context.Background(), f.appID, f.liveDep, string(state.StateRunning), 512, f.nodeID, uuid.NewString(), string(mode))
	if err != nil {
		t.Fatalf("CreateInstanceWithMode: %v", err)
	}
	return ins
}

func TestCrashCapture_HTTPTriggerNeedsOptIn(t *testing.T) {
	for name, f := range appForkStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			ins := f.runningInstance(t, state.InstanceModeNormal)
			if _, err := f.store.RequestHTTPCrashCapture(ctx, f.appID, ins.ID, 500, "/boom", crashCooldown, forkT0); !errors.Is(err, state.ErrCrashCaptureRefused) {
				t.Fatalf("without opt-in err = %v, want refused", err)
			}
			if _, err := f.store.SetCrashSnapshotSettings(ctx, f.accountID, f.appID, true, forkT0); err != nil {
				t.Fatal(err)
			}
			c, err := f.store.RequestHTTPCrashCapture(ctx, f.appID, ins.ID, 503, "/boom", crashCooldown, forkT0)
			if err != nil || c.Status != state.CrashCaptureRequested || c.InstanceID != ins.ID || c.StatusCode == nil || *c.StatusCode != 503 {
				t.Fatalf("opted-in capture = %+v, %v", c, err)
			}
		})
	}
}

func TestCrashCapture_InFlightAndCooldown(t *testing.T) {
	for name, f := range appForkStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			ins := f.runningInstance(t, state.InstanceModeNormal)
			if _, err := f.store.SetCrashSnapshotSettings(ctx, f.accountID, f.appID, true, forkT0); err != nil {
				t.Fatal(err)
			}
			first, err := f.store.RequestHTTPCrashCapture(ctx, f.appID, ins.ID, 500, "/a", crashCooldown, forkT0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.RequestManualCrashCapture(ctx, f.accountID, f.appID, crashCooldown, forkT0.Add(time.Second)); !errors.Is(err, state.ErrCrashCaptureRefused) {
				t.Fatalf("second while in flight err = %v, want refused", err)
			}
			if _, err := f.store.FailCrashCapture(ctx, first.ID, "test", "failed on purpose", forkT0.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.RequestManualCrashCapture(ctx, f.accountID, f.appID, crashCooldown, forkT0.Add(5*time.Minute)); !errors.Is(err, state.ErrCrashCaptureRefused) {
				t.Fatalf("within cooldown err = %v, want refused", err)
			}
			if _, err := f.store.RequestManualCrashCapture(ctx, f.accountID, f.appID, crashCooldown, forkT0.Add(11*time.Minute)); err != nil {
				t.Fatalf("after cooldown: %v", err)
			}
		})
	}
}

func TestCrashCapture_NeverCapturesAFork(t *testing.T) {
	for name, f := range appForkStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			fork := f.runningInstance(t, state.InstanceModeFork)
			if _, err := f.store.SetCrashSnapshotSettings(ctx, f.accountID, f.appID, true, forkT0); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.RequestHTTPCrashCapture(ctx, f.appID, fork.ID, 500, "/", crashCooldown, forkT0); !errors.Is(err, state.ErrCrashCaptureRefused) {
				t.Fatalf("5xx on a fork err = %v, want refused", err)
			}
			if _, err := f.store.RequestManualCrashCapture(ctx, f.accountID, f.appID, crashCooldown, forkT0); !errors.Is(err, state.ErrCrashCaptureRefused) {
				t.Fatalf("manual with only a fork running err = %v, want refused", err)
			}
		})
	}
}

// The lifecycle end to end: claim, complete, fork it, expire. A fork of a
// capture is pinned to it and to its deployment.
func TestCrashCapture_LifecycleAndFork(t *testing.T) {
	for name, f := range appForkStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			ins := f.runningInstance(t, state.InstanceModeNormal)
			requested, err := f.store.RequestManualCrashCapture(ctx, f.accountID, f.appID, crashCooldown, forkT0)
			if err != nil {
				t.Fatal(err)
			}
			if requested.InstanceID != ins.ID {
				t.Fatalf("manual capture targeted %s, want %s", requested.InstanceID, ins.ID)
			}
			claimed, err := f.store.ClaimNextCrashCapture(ctx, forkT0.Add(time.Second))
			if err != nil || claimed.ID != requested.ID || claimed.Status != state.CrashCaptureCapturing {
				t.Fatalf("claim = %+v, %v", claimed, err)
			}
			if _, err := f.store.ClaimNextCrashCapture(ctx, forkT0.Add(2*time.Second)); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("second claim err = %v, want ErrNotFound", err)
			}
			// A fork of a capture that is not ready yet is refused.
			p := f.params(forkT0.Add(3 * time.Second))
			p.CrashCaptureID, p.DeploymentID = requested.ID, ""
			if _, err := f.store.CreateAppFork(ctx, p); !errors.Is(err, state.ErrAppForkDeploymentUnavailable) {
				t.Fatalf("fork of a capturing capture err = %v", err)
			}
			ready, err := f.store.CompleteCrashCapture(ctx, state.CompleteCrashCaptureParams{
				ID: requested.ID, StorageKey: "snap/d/warm/captures/c/v2/mem", VMStateStorageKey: "snap/d/warm/captures/c/v2/vmstate",
				FCVersion: "1.7.0", MemBytes: 1 << 20, CapturedAt: forkT0.Add(4 * time.Second), ExpiresAt: forkT0.Add(time.Hour),
			})
			if err != nil || ready.Status != state.CrashCaptureReady {
				t.Fatalf("complete = %+v, %v", ready, err)
			}
			snap, ok := ready.Snapshot()
			if !ok || snap.StorageKey == "" || snap.FCVersion != "1.7.0" {
				t.Fatalf("restore projection = %+v, %v", snap, ok)
			}
			fork, err := f.store.CreateAppFork(ctx, p)
			if err != nil || fork.CrashCaptureID == nil || *fork.CrashCaptureID != ready.ID || fork.DeploymentID != f.liveDep {
				t.Fatalf("fork of capture = %+v, %v", fork, err)
			}
			due, err := f.store.ExpiredCrashCaptures(ctx, forkT0.Add(2*time.Hour), 10)
			if err != nil || len(due) != 1 || due[0].ID != ready.ID {
				t.Fatalf("expired = %+v, %v", due, err)
			}
			if expired, err := f.store.ExpireCrashCapture(ctx, ready.ID, forkT0.Add(2*time.Hour)); err != nil || expired.Status != state.CrashCaptureExpired {
				t.Fatalf("expire = %+v, %v", expired, err)
			}
			if list, err := f.store.ListCrashCaptures(ctx, f.accountID, f.appID, 10); err != nil || len(list) != 1 {
				t.Fatalf("list = %+v, %v", list, err)
			}
		})
	}
}

func TestCrashCapture_StaleCapturesFail(t *testing.T) {
	for name, f := range appForkStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			f.runningInstance(t, state.InstanceModeNormal)
			if _, err := f.store.RequestManualCrashCapture(ctx, f.accountID, f.appID, crashCooldown, forkT0); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.ClaimNextCrashCapture(ctx, forkT0); err != nil {
				t.Fatal(err)
			}
			stale, err := f.store.FailStaleCrashCaptures(ctx, forkT0.Add(time.Minute), forkT0.Add(6*time.Minute))
			if err != nil || len(stale) != 1 || stale[0].Status != state.CrashCaptureFailed || *stale[0].FailureCode != "capture_timeout" {
				t.Fatalf("stale = %+v, %v", stale, err)
			}
		})
	}
}

// readyCrashCapture drives a manual capture of a running instance to ready.
func (f appForkFixture) readyCrashCapture(t *testing.T) state.CrashCapture {
	t.Helper()
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
	})
	if err != nil || ready.PlaintextState != state.CrashPlaintextPresent || !ready.PlaintextReadable() {
		t.Fatalf("complete = %+v, %v", ready, err)
	}
	return ready
}

// TestCrashCapture_EncryptionAtRest walks the ADR-733 plaintext lifecycle:
// encrypt and purge, stage only for an active fork (which is not claimable
// until staged), keep the plaintext while the fork is active, purge after,
// and drop the sealed key at expiry.
func TestCrashCapture_EncryptionAtRest(t *testing.T) {
	for name, f := range appForkStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			ready := f.readyCrashCapture(t)
			at := func(s int) time.Time { return forkT0.Add(time.Duration(s) * time.Second) }
			if ids, err := f.store.LiveCrashCaptureDeploymentIDs(ctx); err != nil || len(ids) != 1 || ids[0] != f.liveDep {
				t.Fatalf("live capture deployments = %v, %v", ids, err)
			}
			if due, err := f.store.CrashCapturesToEncrypt(ctx, at(2), 10); err != nil || len(due) != 1 {
				t.Fatalf("to encrypt = %+v, %v", due, err)
			}
			enc, err := f.store.MarkCrashCaptureEncrypted(ctx, ready.ID, []byte("sealed"), at(3))
			if err != nil || enc.PlaintextState != state.CrashPlaintextPurging || enc.EncryptedAt == nil || string(enc.SealedKey) != "sealed" {
				t.Fatalf("encrypted = %+v, %v", enc, err)
			}
			if _, err := f.store.MarkCrashCaptureEncrypted(ctx, ready.ID, []byte("again"), at(3)); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("second encrypt err = %v, want ErrNotFound", err)
			}
			if due, err := f.store.CrashCapturesToPurge(ctx, 10); err != nil || len(due) != 1 {
				t.Fatalf("to purge = %+v, %v", due, err)
			}
			if c, err := f.store.FinishCrashCapturePurge(ctx, ready.ID, at(4)); err != nil || c.PlaintextState != state.CrashPlaintextAbsent {
				t.Fatalf("purged = %+v, %v", c, err)
			}

			p := f.params(at(5))
			p.CrashCaptureID, p.DeploymentID = ready.ID, ""
			fork, err := f.store.CreateAppFork(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.ClaimNextAppFork(ctx, "sched-a", at(6), time.Minute); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("claim of a fork on a sealed capture err = %v, want ErrNotFound", err)
			}
			if due, err := f.store.CrashCapturesToStage(ctx, at(6), 10); err != nil || len(due) != 1 {
				t.Fatalf("to stage = %+v, %v", due, err)
			}
			if c, err := f.store.BeginCrashCaptureStage(ctx, ready.ID, at(7)); err != nil || c.PlaintextState != state.CrashPlaintextStaging {
				t.Fatalf("staging = %+v, %v", c, err)
			}
			if _, err := f.store.ClaimNextAppFork(ctx, "sched-a", at(7), time.Minute); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("claim while staging err = %v, want ErrNotFound", err)
			}
			if c, err := f.store.FinishCrashCaptureStage(ctx, ready.ID, at(8)); err != nil || c.PlaintextState != state.CrashPlaintextStaged {
				t.Fatalf("staged = %+v, %v", c, err)
			}
			claimed, err := f.store.ClaimNextAppFork(ctx, "sched-a", at(9), time.Minute)
			if err != nil || claimed.ID != fork.ID {
				t.Fatalf("claim of a staged fork = %+v, %v", claimed, err)
			}
			if _, err := f.store.BeginCrashCapturePurge(ctx, ready.ID, at(10)); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("purge under an active fork err = %v, want ErrNotFound", err)
			}
			if due, err := f.store.CrashCapturesToPurge(ctx, 10); err != nil || len(due) != 0 {
				t.Fatalf("to purge under an active fork = %+v, %v", due, err)
			}
			if _, err := f.store.FinishAppFork(ctx, state.FinishAppForkParams{
				ForkID: fork.ID, LeaseToken: *claimed.LeaseToken, Status: state.AppForkCancelled, FinishedAt: at(11),
			}); err != nil {
				t.Fatal(err)
			}
			if c, err := f.store.BeginCrashCapturePurge(ctx, ready.ID, at(12)); err != nil || c.PlaintextState != state.CrashPlaintextPurging {
				t.Fatalf("purge after the fork = %+v, %v", c, err)
			}
			if _, err := f.store.FinishCrashCapturePurge(ctx, ready.ID, at(13)); err != nil {
				t.Fatal(err)
			}
			expired, err := f.store.ExpireCrashCapture(ctx, ready.ID, forkT0.Add(2*time.Hour))
			if err != nil || expired.SealedKey != nil || expired.PlaintextState != state.CrashPlaintextAbsent {
				t.Fatalf("expired = %+v, %v", expired, err)
			}
			if ids, err := f.store.LiveCrashCaptureDeploymentIDs(ctx); err != nil || len(ids) != 0 {
				t.Fatalf("live capture deployments after expiry = %v, %v", ids, err)
			}
		})
	}
}

// TestCrashCapture_EncryptKeepsPlaintextForAnActiveFork: a capture
// encrypted while a fork is pinned to it stays staged for that fork.
func TestCrashCapture_EncryptKeepsPlaintextForAnActiveFork(t *testing.T) {
	for name, f := range appForkStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			ready := f.readyCrashCapture(t)
			p := f.params(forkT0.Add(2 * time.Second))
			p.CrashCaptureID, p.DeploymentID = ready.ID, ""
			if _, err := f.store.CreateAppFork(ctx, p); err != nil {
				t.Fatal(err)
			}
			enc, err := f.store.MarkCrashCaptureEncrypted(ctx, ready.ID, []byte("sealed"), forkT0.Add(3*time.Second))
			if err != nil || enc.PlaintextState != state.CrashPlaintextStaged {
				t.Fatalf("encrypted under a fork = %+v, %v", enc, err)
			}
		})
	}
}
