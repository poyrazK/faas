package sched_test

// adr: 732
// The fork coordinator clears the output of fork commands once their fork
// has been over for api.AppForkExecOutputRetention, at most once a minute,
// and retries a failed pass on the next tick.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// purgeRecordingExecStore records output purges. Only the methods a tick
// with no queued commands calls are implemented; the embedded interface is
// nil and panics if anything else is reached.
type purgeRecordingExecStore struct {
	state.AppForkExecStore
	purges []time.Time
	err    error
}

func (s *purgeRecordingExecStore) FailOrphanedAppForkExecs(context.Context, time.Duration, time.Time) ([]state.AppForkExec, error) {
	return nil, nil
}

func (s *purgeRecordingExecStore) ClaimNextAppForkExec(context.Context, string, time.Time) (state.AppForkExec, error) {
	return state.AppForkExec{}, state.ErrNotFound
}

func (s *purgeRecordingExecStore) ClearExpiredAppForkExecOutput(_ context.Context, retention time.Duration, now time.Time, limit int) (int, error) {
	if retention != api.AppForkExecOutputRetention || limit < 1 {
		return 0, errors.New("unexpected purge arguments")
	}
	s.purges = append(s.purges, now)
	return 0, s.err
}

type unusedForkRuntime struct{}

func (unusedForkRuntime) RestoreFork(context.Context, state.AppFork) (sched.ForkRestore, error) {
	return sched.ForkRestore{}, errors.New("no fork is queued in this test")
}

func (unusedForkRuntime) DestroyForkInstance(context.Context, string, string) error { return nil }

type unusedForkExecRuntime struct{}

func (unusedForkExecRuntime) ExecForkCommand(context.Context, state.AppForkExec) (apptaskproto.Result, error) {
	return apptaskproto.Result{}, errors.New("no command is queued in this test")
}

func TestForkCoordinatorClearsExpiredExecOutputOncePerMinute(t *testing.T) {
	execs := &purgeRecordingExecStore{}
	clock := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	c := sched.NewForkCoordinator(state.NewMemStore(), unusedForkRuntime{}, sched.ForkCoordinatorConfig{Owner: "schedd-a"}, nil).
		WithForkExecs(execs, unusedForkExecRuntime{})
	*c.Now() = func() time.Time { return clock }
	tick := func(at time.Duration) {
		clock = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC).Add(at)
		c.Tick(context.Background())
		c.WaitForkExecs()
	}

	tick(0)
	tick(30 * time.Second)
	if len(execs.purges) != 1 {
		t.Fatalf("purges within a minute = %d, want 1", len(execs.purges))
	}
	tick(61 * time.Second)
	if len(execs.purges) != 2 {
		t.Fatalf("purges after a minute = %d, want 2", len(execs.purges))
	}

	// A failed pass is not counted as done: the next tick tries again.
	execs.err = errors.New("store unavailable")
	tick(2*time.Minute + 2*time.Second)
	tick(2*time.Minute + 4*time.Second)
	if len(execs.purges) != 4 {
		t.Fatalf("purges after a failed pass = %d, want 4 (the failure retried at once)", len(execs.purges))
	}
}
