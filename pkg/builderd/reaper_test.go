package builderd

// Reaper tests (issue #195 B1.4). All tests run against an in-memory
// state.Store + MemStore hooks (no DB, no KVM). The PG-backed tests
// live under pkg/state/pgstore_test.go if/when a similar hook is
// introduced.

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// newReaperFixture builds a MemStore with one seeded running build
// row. Returns the store (as state.Store interface for the public
// path + the concrete *state.MemStore for test-only hooks like
// SetBuildStartedAtForTest) + the build ID.
func newReaperFixture(t *testing.T) (state.Store, *state.MemStore, string) {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), "u@x.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "reaper-app", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:r",
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.CreateBuild(context.Background(), dep.ID, state.DeploymentKindImage, 1024, "/tmp/log")
	if err != nil {
		t.Fatal(err)
	}
	// Move the row to 'running' so the sweep can match it.
	if err := store.UpdateBuildStatus(context.Background(), b.ID, state.BuildRunning, "", true, false); err != nil {
		t.Fatal(err)
	}
	return store, store, b.ID
}

// TestReaperLoop_SweepsStuckRow drives one tick of the reaper
// against a backdated build row. The threshold is 5 minutes; the row
// is backdated 10 minutes. The sweep must flip the row to
// status='failed' + failure_class='timeout' + finished_at stamped.
func TestReaperLoop_SweepsStuckRow(t *testing.T) {
	store, ms, buildID := newReaperFixture(t)

	ms.SetBuildStartedAtForTest(buildID, time.Now().Add(-10*time.Minute))

	runReaperOnce(t, store, 5*time.Minute)

	got, err := store.BuildByID(context.Background(), buildID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.BuildFailed {
		t.Errorf("stuck-running sweep: status = %q, want %q", got.Status, state.BuildFailed)
	}
	if got.FailureClass != state.FailureTimeout {
		t.Errorf("stuck-running sweep: failure_class = %q, want %q",
			got.FailureClass, state.FailureTimeout)
	}
	if got.FinishedAt.IsZero() {
		t.Error("stuck-running sweep: finished_at was not stamped")
	}
	dep, err := store.DeploymentByID(context.Background(), got.DeploymentID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	if dep.Status != state.DeployFailed {
		t.Errorf("stuck-running sweep: deployment status = %q, want %q", dep.Status, state.DeployFailed)
	}
	if dep.ErrorCode != api.CodeBuildTimeout {
		t.Errorf("stuck-running sweep: deployment error_code = %q, want %q", dep.ErrorCode, api.CodeBuildTimeout)
	}
}

// TestReaperLoop_LeavesFreshRowsAlone asserts a 'running' row whose
// started_at is within the threshold is NOT swept. The build is
// still in flight — the reaper must wait for it to either finish or
// exceed the threshold.
func TestReaperLoop_LeavesFreshRowsAlone(t *testing.T) {
	store, ms, buildID := newReaperFixture(t)

	// Backdate only 1 minute; threshold is 5 minutes. Row is still
	// "in flight" by the threshold's definition.
	ms.SetBuildStartedAtForTest(buildID, time.Now().Add(-1*time.Minute))

	runReaperOnce(t, store, 5*time.Minute)

	got, err := store.BuildByID(context.Background(), buildID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.BuildRunning {
		t.Errorf("fresh row swept: status = %q, want %q", got.Status, state.BuildRunning)
	}
}

// TestReaperLoop_ThresholdParam is the table-driven threshold check.
// Two cases: short threshold (1 min) sweeps a 2-min-old row; long
// threshold (1 hour) leaves the same row alone.
func TestReaperLoop_ThresholdParam(t *testing.T) {
	cases := []struct {
		name      string
		threshold time.Duration
		backdate  time.Duration
		wantSweep bool
	}{
		{"below_threshold", 5 * time.Minute, 10 * time.Minute, true},
		{"above_threshold", 30 * time.Minute, 10 * time.Minute, false},
		{"fresh_row", 1 * time.Minute, 10 * time.Second, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, ms, buildID := newReaperFixture(t)

			ms.SetBuildStartedAtForTest(buildID, time.Now().Add(-tc.backdate))

			runReaperOnce(t, store, tc.threshold)

			got, _ := store.BuildByID(context.Background(), buildID)
			swept := got.Status == state.BuildFailed
			if swept != tc.wantSweep {
				t.Errorf("threshold=%v backdate=%v: swept=%v, want %v (status=%q)",
					tc.threshold, tc.backdate, swept, tc.wantSweep, got.Status)
			}
		})
	}
}

// TestReaperLoop_IdempotentSecondTick asserts the sweep is a no-op on
// the second tick. After the first tick flips the row to 'failed',
// the second tick matches 0 rows.
func TestReaperLoop_IdempotentSecondTick(t *testing.T) {
	store, ms, buildID := newReaperFixture(t)

	ms.SetBuildStartedAtForTest(buildID, time.Now().Add(-10*time.Minute))

	runReaperOnce(t, store, 5*time.Minute)
	first, _ := store.BuildByID(context.Background(), buildID)
	if first.Status != state.BuildFailed {
		t.Fatalf("first tick didn't sweep; status = %q", first.Status)
	}
	firstFinishedAt := first.FinishedAt

	runReaperOnce(t, store, 5*time.Minute)
	second, _ := store.BuildByID(context.Background(), buildID)
	if second.Status != state.BuildFailed {
		t.Errorf("second tick un-swept: status = %q", second.Status)
	}
	// finished_at must NOT have been re-stamped (the second sweep
	// matches 0 rows because the row is already 'failed').
	if !second.FinishedAt.Equal(firstFinishedAt) {
		t.Errorf("finished_at changed on idempotent second tick: was %v, now %v",
			firstFinishedAt, second.FinishedAt)
	}
}

// TestReaperLoop_LateMarkSucceededCannotResurrect is the B1.4 race
// regression. The CAS guard on UpdateBuildStatus prevents a late
// markSucceeded from a builderd process that finishes AFTER the
// reaper sweep from resurrecting a 'failed(timeout)' row.
func TestReaperLoop_LateMarkSucceededCannotResurrect(t *testing.T) {
	store, ms, buildID := newReaperFixture(t)

	ms.SetBuildStartedAtForTest(buildID, time.Now().Add(-10*time.Minute))

	// Step 1: reaper sweeps.
	runReaperOnce(t, store, 5*time.Minute)
	first, _ := store.BuildByID(context.Background(), buildID)
	if first.Status != state.BuildFailed {
		t.Fatalf("setup: reaper didn't sweep; status = %q", first.Status)
	}

	// Step 2: a late builderd process finishes the build and calls
	// UpdateBuildStatus(BuildSucceeded). The CAS guard must refuse —
	// the row is no longer 'running', so the update returns
	// ErrNotFound (which the real builderd logs at WARN).
	err := store.UpdateBuildStatus(context.Background(), buildID,
		state.BuildSucceeded, "", false, true)
	if err == nil {
		t.Error("B1.4 race regression: late markSucceeded silently resurrected a swept row")
	}

	got, _ := store.BuildByID(context.Background(), buildID)
	if got.Status != state.BuildFailed {
		t.Errorf("B1.4 race regression: late markSucceeded changed status to %q", got.Status)
	}
}

// TestReaperLoop_DoesNotTouchNonRunningRows asserts the sweep
// ignores rows in {queued, succeeded, failed} — only 'running' is in
// scope.
func TestReaperLoop_DoesNotTouchNonRunningRows(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "u@x.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "non-running", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:n",
	})

	// One 'queued' build that we backdate — the sweep must skip it.
	queued, err := store.CreateBuild(context.Background(), dep.ID, state.DeploymentKindImage, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	store.SetBuildStartedAtForTest(queued.ID, time.Now().Add(-1*time.Hour))

	// One 'succeeded' build (terminal). Sweep must skip.
	terminal, _ := store.CreateBuild(context.Background(), dep.ID, state.DeploymentKindImage, 1, "")
	if err := store.UpdateBuildStatus(context.Background(), terminal.ID, state.BuildRunning, "", true, false); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateBuildStatus(context.Background(), terminal.ID, state.BuildSucceeded, "", false, true); err != nil {
		t.Fatal(err)
	}
	store.SetBuildStartedAtForTest(terminal.ID, time.Now().Add(-1*time.Hour))

	runReaperOnce(t, store, 5*time.Minute)

	got, _ := store.BuildByID(context.Background(), queued.ID)
	if got.Status != state.BuildQueued {
		t.Errorf("queued row swept: status = %q, want %q", got.Status, state.BuildQueued)
	}
	got, _ = store.BuildByID(context.Background(), terminal.ID)
	if got.Status != state.BuildSucceeded {
		t.Errorf("succeeded row swept: status = %q, want %q", got.Status, state.BuildSucceeded)
	}
}

// runReaperOnce calls SweepStuckRunningBuilds directly with a cutoff
// of `time.Now() - threshold`. This is the inner work the reaper
// goroutine does per tick; calling it directly is faster and more
// deterministic than driving the goroutine.
func runReaperOnce(t *testing.T, store state.Store, threshold time.Duration) {
	t.Helper()
	cutoff := time.Now().Add(-threshold)
	if _, err := store.SweepStuckRunningBuilds(context.Background(), cutoff); err != nil {
		t.Fatalf("SweepStuckRunningBuilds: %v", err)
	}
}

// TestReaperLoop_GoroutineSmoke spins the actual ReaperLoop
// goroutine for two ticks on a backdated row, then cancels the
// context. Asserts the goroutine returns cleanly and the row was
// swept. Catches panics in the loop body that the unit-level
// SweepStuckRunningBuilds call wouldn't surface.
func TestReaperLoop_GoroutineSmoke(t *testing.T) {
	store, ms, buildID := newReaperFixture(t)

	ms.SetBuildStartedAtForTest(buildID, time.Now().Add(-10*time.Minute))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ReaperLoop(ctx, store, 5*time.Millisecond, 5*time.Minute,
			slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()

	// Let the goroutine tick at least twice (5 ms cadence).
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
		// expected — goroutine returned cleanly on ctx.Done
	case <-time.After(2 * time.Second):
		t.Fatal("ReaperLoop did not return after ctx cancel")
	}

	got, _ := store.BuildByID(context.Background(), buildID)
	if got.Status != state.BuildFailed {
		t.Errorf("goroutine smoke: status = %q, want %q", got.Status, state.BuildFailed)
	}
}
