// adr: 051

package main

import (
	"errors"
	"testing"
)

func TestSupervisorExitStatusDistinguishesRunningFromExitZero(t *testing.T) {
	sup := &Supervisor{}
	if code := sup.LastExitCode(); code != -1 {
		t.Fatalf("initial LastExitCode = %d, want -1", code)
	}
	if code, exited := sup.LastExitStatus(); exited || code != 0 {
		t.Fatalf("initial LastExitStatus = (%d, %v), want (0, false)", code, exited)
	}

	sup.trackExit(0)
	if code, exited := sup.LastExitStatus(); !exited || code != 0 {
		t.Fatalf("clean LastExitStatus = (%d, %v), want (0, true)", code, exited)
	}
}

func TestSupervisorExitCodeReservesMinusOneForRunning(t *testing.T) {
	if code := supervisorExitCode(errors.New("fork failed")); code != 255 {
		t.Fatalf("non-exit failure code = %d, want 255", code)
	}
}
