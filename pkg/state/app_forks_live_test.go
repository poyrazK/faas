package state_test

// adr: 732

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestAppFork_LiveForkCapturesNow: a live fork and a live_fork capture of
// the newest running instance are written together; the fork waits for the
// capture, and the in-flight, cooldown and fork limits bound how often a
// serving instance is paused.
func TestAppFork_LiveForkCapturesNow(t *testing.T) {
	for name, f := range appForkStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			live := func(at time.Time) state.CreateAppForkParams {
				p := f.params(at)
				p.DeploymentID, p.Live, p.LiveCaptureCooldown = "", true, time.Minute
				return p
			}
			if _, err := f.store.CreateAppFork(ctx, live(forkT0)); !errors.Is(err, state.ErrAppForkLiveCaptureRefused) {
				t.Fatalf("without a running instance err = %v, want refused", err)
			}
			ins := f.runningInstance(t, state.InstanceModeNormal)
			fork, err := f.store.CreateAppFork(ctx, live(forkT0))
			if err != nil || fork.CrashCaptureID == nil || fork.DeploymentID != f.liveDep || fork.Status != state.AppForkQueued {
				t.Fatalf("live fork = %+v, %v", fork, err)
			}
			capture, err := f.store.CrashCaptureForRestore(ctx, *fork.CrashCaptureID)
			if err != nil || capture.Trigger != state.CrashTriggerLiveFork || capture.InstanceID != ins.ID || capture.Status != state.CrashCaptureRequested {
				t.Fatalf("live capture = %+v, %v", capture, err)
			}
			if _, err := f.store.ClaimNextAppFork(ctx, "sched-a", forkT0.Add(time.Second), time.Minute); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("claim before the capture err = %v, want ErrNotFound", err)
			}
			if _, err := f.store.ClaimNextCrashCapture(ctx, forkT0.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.ClaimNextAppFork(ctx, "sched-a", forkT0.Add(2*time.Second), time.Minute); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("claim while capturing err = %v, want ErrNotFound", err)
			}
			if _, err := f.store.CompleteCrashCapture(ctx, state.CompleteCrashCaptureParams{
				ID: capture.ID, StorageKey: "snap/d/warm/captures/c/v2/mem", VMStateStorageKey: "snap/d/warm/captures/c/v2/vmstate",
				FCVersion: "1.7.0", MemBytes: 1 << 20, CapturedAt: forkT0.Add(3 * time.Second), ExpiresAt: forkT0.Add(5 * time.Hour),
			}); err != nil {
				t.Fatal(err)
			}
			claimed, err := f.store.ClaimNextAppFork(ctx, "sched-a", forkT0.Add(4*time.Second), time.Minute)
			if err != nil || claimed.ID != fork.ID {
				t.Fatalf("claim after the capture = %+v, %v", claimed, err)
			}
			if _, err := f.store.FinishAppFork(ctx, state.FinishAppForkParams{
				ForkID: fork.ID, LeaseToken: *claimed.LeaseToken, Status: state.AppForkCancelled, FinishedAt: forkT0.Add(5 * time.Second),
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.CreateAppFork(ctx, live(forkT0.Add(30*time.Second))); !errors.Is(err, state.ErrAppForkLiveCaptureRefused) {
				t.Fatalf("within the cooldown err = %v, want refused", err)
			}
			next, err := f.store.CreateAppFork(ctx, live(forkT0.Add(2*time.Minute)))
			if err != nil {
				t.Fatalf("after the cooldown: %v", err)
			}
			// A full fork limit refuses before anything is captured.
			if _, err := f.store.CreateAppFork(ctx, live(forkT0.Add(10*time.Minute))); err == nil {
				t.Fatal("a second active fork was admitted")
			}
			captures, err := f.store.ListCrashCaptures(ctx, f.accountID, f.appID, 10)
			if err != nil || len(captures) != 2 || captures[0].ID != *next.CrashCaptureID {
				t.Fatalf("captures = %+v, %v; want only the two live captures", captures, err)
			}
		})
	}
}

func TestAppFork_LiveRejectsAPinnedTarget(t *testing.T) {
	f := appForkStores(t)["mem"]
	p := f.params(forkT0)
	p.Live = true
	if _, err := f.store.CreateAppFork(context.Background(), p); !errors.Is(err, state.ErrAppForkInvalid) {
		t.Fatalf("live fork with a deployment err = %v, want invalid", err)
	}
}
