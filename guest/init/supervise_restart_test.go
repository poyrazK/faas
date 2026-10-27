package main

import (
	"errors"
	"os/exec"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// A live-patch restart (ADR-740) restarts the child even when the restart
// policy and budget would never allow another start.
func TestSupervisorRequestRestartBypassesPolicyAndBudget(t *testing.T) {
	var starts atomic.Int32
	started := make(chan struct{}, 4)
	s := &Supervisor{Max: 0, Policy: api.RestartPolicyNo}
	s.Start = func() error {
		cmd := exec.Command("sleep", "30")
		if err := cmd.Start(); err != nil {
			return err
		}
		s.TrackCommand(cmd)
		starts.Add(1)
		started <- struct{}{}
		return cmd.Wait()
	}
	done := make(chan error, 1)
	go func() { done <- s.Run() }()
	waitStarted(t, started)

	if err := s.RequestRestart(); err != nil {
		t.Fatal(err)
	}
	waitStarted(t, started)
	if got := starts.Load(); got != 2 {
		t.Fatalf("starts after a requested restart = %d, want 2", got)
	}

	// Stop without Supervisor.Stop's own cmd.Wait, which would race this
	// test's Start with a second Wait on the same command.
	s.RequestStop()
	if err := s.ForwardSignal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run after stop = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor did not stop")
	}
	if got := starts.Load(); got != 2 {
		t.Fatalf("starts after stop = %d, want no further restarts", got)
	}
}

func TestSupervisorRequestRestartWithoutProcess(t *testing.T) {
	s := &Supervisor{Max: 3, Start: func() error { return nil }}
	if err := s.RequestRestart(); !errors.Is(err, errNoWorkloadProcess) {
		t.Fatalf("RequestRestart before the first fork = %v, want errNoWorkloadProcess", err)
	}
}

func waitStarted(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("child did not start")
	}
}

// Production-us rc.294: h14-py crashed once (restart 1/3), was parked into
// a fresh snapshot, and the instance restored from it read "restart 2/3"
// on its first crash: the snapshot carried the used budget, so each
// park/wake cycle shrank it until a single crash killed the instance
// (Hunt 14 H14-19).
func TestSupervisorRestoreRestoresTheRestartBudget(t *testing.T) {
	var starts int
	s := &Supervisor{Max: 1, Policy: api.RestartPolicyOnFailure}
	s.Start = func() error {
		starts++
		if starts == 2 {
			restoreGeneration.bump() // the instance was snapshotted and restored while this run was up
		}
		return errors.New("boom")
	}
	if err := s.Run(); err == nil {
		t.Fatal("Run = nil, want the crash loop error")
	}
	// start, crash (1/1), restore, crash (budget reset: 1/1), crash: exhausted.
	if starts != 3 {
		t.Fatalf("starts = %d, want 3: a restore resets the restart budget", starts)
	}
}
