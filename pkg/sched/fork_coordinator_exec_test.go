package sched

// adr: 732

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeForkExecRuntime struct {
	ran []string
	err error
}

func (f *fakeForkExecRuntime) ExecForkCommand(_ context.Context, exec state.AppForkExec) (apptaskproto.Result, error) {
	f.ran = append(f.ran, exec.ID)
	if f.err != nil {
		return apptaskproto.Result{}, f.err
	}
	zero := 0
	return apptaskproto.Result{Status: apptaskproto.StatusSucceeded, ExitCode: &zero, Stdout: []byte("ok\n")}, nil
}

func TestForkCoordinator_RunsCommandsOfHeldForks(t *testing.T) {
	f := newForkCoordinatorFixture(t)
	execs := &fakeForkExecRuntime{}
	c := f.coordinator("schedd-a", 4).WithForkExecs(f.store, execs)
	fork := f.createFork(t, 600)
	c.Tick(context.Background())
	if got := f.fork(t, fork.ID); got.Status != state.AppForkRunning {
		t.Fatalf("fork = %s, want running", got.Status)
	}

	p := state.CreateAppForkExecParams{
		AccountID: f.acct.ID, AppID: f.app.ID, ForkID: fork.ID, RequestedBy: "user:test",
		Command: []string{"cat", "/app/hello.txt"}, TimeoutSeconds: 30, MaxOutputBytes: 4096,
		MaxPending: 4, CreatedAt: f.clock,
	}
	exec, err := f.store.CreateAppForkExec(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	// Another scheduler does not hold the fork, so it never runs the command.
	other := f.coordinator("schedd-b", 4).WithForkExecs(f.store, execs)
	other.Tick(context.Background())
	other.WaitForkExecs()
	if len(execs.ran) != 0 {
		t.Fatalf("a scheduler without the lease ran %v", execs.ran)
	}

	c.Tick(context.Background())
	c.WaitForkExecs()
	got, err := f.store.AppForkExecByID(context.Background(), f.acct.ID, f.app.ID, fork.ID, exec.ID)
	if err != nil || got.Status != state.AppForkExecSucceeded || string(got.Stdout) != "ok\n" || len(execs.ran) != 1 {
		t.Fatalf("exec = %+v, %v; ran=%v", got, err, execs.ran)
	}

	execs.err = ErrForkExecUnavailable
	p.CreatedAt = f.clock.Add(time.Second)
	failing, err := f.store.CreateAppForkExec(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	c.Tick(context.Background())
	c.WaitForkExecs()
	got, err = f.store.AppForkExecByID(context.Background(), f.acct.ID, f.app.ID, fork.ID, failing.ID)
	if err != nil || got.Status != state.AppForkExecFailed || got.FailureCode == nil || *got.FailureCode != "fork_unavailable" {
		t.Fatalf("failing exec = %+v, %v", got, err)
	}
	if !errors.Is(execs.err, ErrForkExecUnavailable) {
		t.Fatal("unreachable")
	}
}
