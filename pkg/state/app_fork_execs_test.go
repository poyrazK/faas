package state_test

// adr: 732
// Fork exec rules on both stores: only a running fork accepts commands, a
// pending cap applies, only the fork's lease holder claims them one at a
// time, results are bounded, and commands of an ended fork fail.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func (f appForkFixture) runningFork(t *testing.T, owner string) state.AppFork {
	t.Helper()
	ctx := context.Background()
	fork, err := f.store.CreateAppFork(ctx, f.params(forkT0))
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := f.store.ClaimNextAppFork(ctx, owner, forkT0.Add(time.Second), time.Hour)
	if err != nil || claimed.ID != fork.ID {
		t.Fatalf("claim fork = %+v, %v", claimed, err)
	}
	ins := f.runningInstance(t, state.InstanceModeFork)
	running, err := f.store.MarkAppForkRunning(ctx, fork.ID, *claimed.LeaseToken, "snap-1", ins.ID, forkT0.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return running
}

func (f appForkFixture) execParams(forkID string, at time.Time) state.CreateAppForkExecParams {
	return state.CreateAppForkExecParams{
		AccountID: f.accountID, AppID: f.appID, ForkID: forkID, RequestedBy: "user:test",
		Command: []string{"/hello-server", "-print-file", "/app/hello.txt"}, TimeoutSeconds: 60,
		MaxOutputBytes: 4096, MaxPending: 2, CreatedAt: at,
	}
}

func TestAppForkExec_Lifecycle(t *testing.T) {
	for name, f := range appForkStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			queued, err := f.store.CreateAppFork(ctx, f.params(forkT0))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.CreateAppForkExec(ctx, f.execParams(queued.ID, forkT0)); !errors.Is(err, state.ErrAppForkExecRefused) {
				t.Fatalf("exec on a queued fork err = %v, want refused", err)
			}
			if _, err := f.store.RequestAppForkCancellation(ctx, f.accountID, f.appID, queued.ID, forkT0); err != nil {
				t.Fatal(err)
			}

			fork := f.runningFork(t, "sched-a")
			at := forkT0.Add(3 * time.Second)
			first, err := f.store.CreateAppForkExec(ctx, f.execParams(fork.ID, at))
			if err != nil || first.Status != state.AppForkExecQueued || len(first.Command) != 3 {
				t.Fatalf("exec = %+v, %v", first, err)
			}
			second, err := f.store.CreateAppForkExec(ctx, f.execParams(fork.ID, at.Add(time.Millisecond)))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.CreateAppForkExec(ctx, f.execParams(fork.ID, at.Add(2*time.Millisecond))); !errors.Is(err, state.ErrAppForkExecRefused) {
				t.Fatalf("third pending exec err = %v, want refused", err)
			}

			if _, err := f.store.ClaimNextAppForkExec(ctx, "sched-b", at.Add(time.Second)); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("claim by another scheduler err = %v, want ErrNotFound", err)
			}
			claimed, err := f.store.ClaimNextAppForkExec(ctx, "sched-a", at.Add(time.Second))
			if err != nil || claimed.ID != first.ID || claimed.Status != state.AppForkExecRunning || claimed.StartedAt == nil {
				t.Fatalf("claim = %+v, %v", claimed, err)
			}
			if _, err := f.store.ClaimNextAppForkExec(ctx, "sched-a", at.Add(time.Second)); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("second claim while one runs err = %v, want ErrNotFound", err)
			}
			zero := 0
			done, err := f.store.FinishAppForkExec(ctx, state.FinishAppForkExecParams{
				ID: first.ID, Status: state.AppForkExecSucceeded, ExitCode: &zero,
				Stdout: []byte("hello\n"), FinishedAt: at.Add(2 * time.Second),
			})
			if err != nil || done.Status != state.AppForkExecSucceeded || string(done.Stdout) != "hello\n" || *done.ExitCode != 0 {
				t.Fatalf("finish = %+v, %v", done, err)
			}
			got, err := f.store.AppForkExecByID(ctx, f.accountID, f.appID, fork.ID, first.ID)
			if err != nil || string(got.Stdout) != "hello\n" {
				t.Fatalf("get = %+v, %v", got, err)
			}
			if list, err := f.store.ListAppForkExecs(ctx, f.accountID, f.appID, fork.ID, 10); err != nil || len(list) != 2 || list[0].ID != second.ID {
				t.Fatalf("list = %+v, %v", list, err)
			}

			// The fork ends: its queued command fails.
			if _, err := f.store.FinishAppFork(ctx, state.FinishAppForkParams{
				ForkID: fork.ID, LeaseToken: *fork.LeaseToken, Status: state.AppForkCancelled, FinishedAt: at.Add(3 * time.Second),
			}); err != nil {
				t.Fatal(err)
			}
			orphaned, err := f.store.FailOrphanedAppForkExecs(ctx, time.Minute, at.Add(4*time.Second))
			if err != nil || len(orphaned) != 1 || orphaned[0].ID != second.ID || orphaned[0].Status != state.AppForkExecFailed {
				t.Fatalf("orphaned = %+v, %v", orphaned, err)
			}
		})
	}
}
