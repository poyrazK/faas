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
