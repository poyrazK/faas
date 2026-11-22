package apidserver

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// A copy or binding still being made is not a failed attempt; every other
// error is, and keeps its managed PostgreSQL meaning.
func TestEnvironmentBranchDataInProgress(t *testing.T) {
	copying := fmt.Errorf("isolated PostgreSQL copy is not ready: %w%w", errDatabaseCopyInProgress, managedpostgres.ErrConflict)
	binding := fmt.Errorf("database copy binding for workload %q is not ready: %w%w", "web", errEnvironmentBranchDataInProgress, managedpostgres.ErrUnavailable)
	for _, err := range []error{copying, binding, fmt.Errorf("copy: %w", copying)} {
		if !environmentBranchDataInProgress(err) {
			t.Errorf("%v: not in progress", err)
		}
	}
	if !errors.Is(copying, managedpostgres.ErrConflict) || !errors.Is(binding, managedpostgres.ErrUnavailable) {
		t.Fatal("in-progress errors lost their managed PostgreSQL class")
	}
	for _, err := range []error{managedpostgres.ErrConflict, managedpostgres.ErrUnavailable, managedpostgres.ErrQuotaExceeded, errEnvironmentBranchBucketLimit} {
		if environmentBranchDataInProgress(err) {
			t.Errorf("%v: counted as in progress", err)
		}
	}
}

// Creating a branch with pending data wakes the reconciler without blocking,
// however many branches are created before it runs.
func TestEnvironmentBranchDataWake(t *testing.T) {
	srv := newServer(state.NewMemStore(), slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", &captureNotifier{})
	srv.wakeEnvironmentBranchData()
	srv.wakeEnvironmentBranchData()
	select {
	case <-srv.environmentBranchDataWakeChannel():
	default:
		t.Fatal("no wake-up queued")
	}
	select {
	case <-srv.environmentBranchDataWakeChannel():
		t.Fatal("wake-ups were not coalesced")
	default:
	}
}
