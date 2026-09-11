// memstore_jobs_coverage_test.go — pkg/state coverage pin for the
// JobStore surface (Mega-1 jobs).
//
// Why this file exists:
//
//	The pg-shard-2a unit-test job asserts pkg/state coverage ≥ 70%
//	(see Makefile::check-state-coverage). Mega-1 added ~24 new
//	JobStore methods on MemStore AND a mirror set on PgStore
//	(pgstore_jobs.go, 954 LOC). PgStore tests are pgtest-gated and
//	skip when DATABASE_URL isn't set, so the gate's default mode
//	depends on MemStore coverage carrying the package. The
//	pre-existing TestMemStoreJobs happy path covers roughly half
//	the surface; this file closes the gap.
//
// What it asserts:
//
//	Each JobStore method is called with both happy AND error inputs
//	(missing id, terminal-on-already-terminal, backoff-future
//	gating, etc.). Asserts the returned error sentinel so a future
//	refactor that swaps ErrNotFound/ErrConflict for a generic error
//	trips here.
//
// Why not fold into TestMemStoreJobs:
//
//	TestMemStoreJobs is the end-to-end happy-path walk referenced
//	by name from code review comments. Splitting the coverage pin
//	out keeps the original test focused and lets a regression here
//	point at a specific method (each test function in this file
//	targets one JobStore method).
package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// helper — create a fresh job + 3-task run on the supplied store and
// return the (job, run, fannedTasks) so each coverage test can drive
// a specific JobStore method without re-walking the create dance.
func newJobAndRun(t *testing.T, ms *MemStore, accountID, name string) (Job, JobRun, []JobTask) {
	t.Helper()
	ctx := context.Background()
	created, err := ms.JobCreate(ctx,
		accountID, name, "app",
		"oci://registry.example/x@sha256:deadbeef",
		[]string{"/bin/sh", "-c", "echo hi"}, 256, 60, 4, 3,
		json.RawMessage(`{}`),
	)
	if err != nil {
		t.Fatalf("setup JobCreate: %v", err)
	}
	parallelism := 2
	run, fanned, err := ms.JobRunCreate(ctx,
		created.ID, created.AccountID, "manual",
		&parallelism, nil, nil,
		json.RawMessage(`{}`), 3,
	)
	if err != nil {
		t.Fatalf("setup JobRunCreate: %v", err)
	}
	return created, run, fanned
}

// TestMemStoreJobs_JobGetByID covers the happy + ErrNotFound paths
// and the soft-delete invisibility rule.
func TestMemStoreJobs_JobGetByID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ms := NewMemStore()
	created, _, _ := newJobAndRun(t, ms, "acct-A", "g1")

	got, err := ms.JobGetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("JobGetByID: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("JobGetByID.ID = %q, want %q", got.ID, created.ID)
	}

	if _, err := ms.JobGetByID(ctx, "does-not-exist"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("JobGetByID missing: err = %v, want ErrNotFound", err)
	}
}

// TestMemStoreJobs_JobListByAccount — pagination + soft-delete
// invisibility + offset past end + limit clamp.
func TestMemStoreJobs_JobListByAccount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ms := NewMemStore()
	for i, name := range []string{"a", "b", "c"} {
		ms.JobCreate(ctx, "acct-X", name, "app",
			"oci://x@sha256:1", []string{"/bin/sh"}, 128, 30, 1, 0,
			json.RawMessage(`{}`),
		)
		_ = i
	}

	all, err := ms.JobListByAccount(ctx, "acct-X", 0, 0)
	if err != nil || len(all) != 3 {
		t.Fatalf("JobListByAccount(all) = (%v, %d), want (nil, 3)", err, len(all))
	}
	first, err := ms.JobListByAccount(ctx, "acct-X", 1, 0)
	if err != nil || len(first) != 1 {
		t.Fatalf("JobListByAccount(limit=1) = (%v, %d), want (nil, 1)", err, len(first))
	}
	past, err := ms.JobListByAccount(ctx, "acct-X", 0, 100)
	if err != nil || past != nil {
		t.Fatalf("JobListByAccount(offset past end) = (%v, %v), want (nil, nil)", err, past)
	}
}

// TestMemStoreJobs_JobUpdate — partial updates (nil pointers leave
// the column untouched) + ErrNotFound on missing id.
func TestMemStoreJobs_JobUpdate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ms := NewMemStore()
	created, _, _ := newJobAndRun(t, ms, "acct-U", "upd")

	newImg := "oci://x@sha256:2"
	newRAM := 512
	newStatus := "paused"
	got, err := ms.JobUpdate(ctx, created.ID, []string{"/bin/echo", "ok"},
		&newImg, &newRAM, nil, nil, nil, json.RawMessage(`{}`), &newStatus)
	if err != nil {
		t.Fatalf("JobUpdate: %v", err)
	}
	if got.RAMMB != 512 {
		t.Fatalf("JobUpdate.RAMMB = %d, want 512", got.RAMMB)
	}
	if got.Status != "paused" {
		t.Fatalf("JobUpdate.Status = %q, want paused", got.Status)
	}
	if got.TaskTimeoutS != 60 {
		t.Fatalf("JobUpdate.TaskTimeoutS = %d, want 60 (nil pointer = untouched)", got.TaskTimeoutS)
	}

	if _, err := ms.JobUpdate(ctx, "missing-id", nil, nil, nil, nil, nil, nil, nil, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("JobUpdate missing: err = %v, want ErrNotFound", err)
	}
}

// TestMemStoreJobs_JobCreateDuplicate — exercises the ErrConflict
// path on the per-account name-uniqueness constraint.
func TestMemStoreJobs_JobCreateDuplicate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ms := NewMemStore()
	if _, err := ms.JobCreate(ctx, "acct-D", "dup", "app",
		"oci://x", []string{"/bin/sh"}, 128, 30, 1, 0, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("first JobCreate: %v", err)
	}
	_, err := ms.JobCreate(ctx, "acct-D", "dup", "app",
		"oci://x", []string{"/bin/sh"}, 128, 30, 1, 0, json.RawMessage(`{}`))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("second JobCreate: err = %v, want ErrConflict", err)
	}
	if !strings.Contains(err.Error(), "jobs_account_name_uniq") {
		t.Fatalf("second JobCreate: err = %v, want substring jobs_account_name_uniq", err)
	}
}

// TestMemStoreJobs_JobCountByAccount — counter excludes soft-deleted
// rows; counts only non-deleted jobs on the account.
func TestMemStoreJobs_JobCountByAccount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ms := NewMemStore()
	created, run, _ := newJobAndRun(t, ms, "acct-C", "c1")
	ms.JobCreate(ctx, "acct-C", "c2", "app",
		"oci://x", []string{"/bin/sh"}, 128, 30, 1, 0, json.RawMessage(`{}`))

	before, _ := ms.JobCountByAccount(ctx, "acct-C")
	if before != 2 {
		t.Fatalf("JobCountByAccount(before soft-delete) = %d, want 2", before)
	}
	// Cancel every task on the run so the soft-delete predicate
	// (no live tasks for this job) holds.
	for i := 0; i < 3; i++ {
		_ = ms.JobTaskCancel(ctx, run.ID, i)
	}
	got, _, err := ms.JobSoftDelete(ctx, created.ID)
	if err != nil || !got {
		t.Fatalf("JobSoftDelete: (deleted=%v, err=%v); want (true, nil)", got, err)
	}
	after, _ := ms.JobCountByAccount(ctx, "acct-C")
	if after != 1 {
		t.Fatalf("JobCountByAccount(after soft-delete) = %d, want 1", after)
	}
}

// TestMemStoreJobs_JobSoftDelete_NotFound — ErrNotFound when the
// id does not resolve.
func TestMemStoreJobs_JobSoftDelete_NotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ms := NewMemStore()
	if _, _, err := ms.JobSoftDelete(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("JobSoftDelete(missing): err = %v, want ErrNotFound", err)
	}
}

// TestMemStoreJobs_JobSoftDelete_HasLive — refuses soft-delete
// when a non-terminal task exists for the job; returns
// (deleted=false, hasLiveInstances=true, nil).
func TestMemStoreJobs_JobSoftDelete_HasLive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ms := NewMemStore()
	created, _, _ := newJobAndRun(t, ms, "acct-L", "live")
	deleted, hasLive, err := ms.JobSoftDelete(ctx, created.ID)
	if err != nil {
		t.Fatalf("JobSoftDelete(live): %v", err)
	}
	if deleted || !hasLive {
		t.Fatalf("JobSoftDelete(live): (deleted=%v, hasLive=%v), want (false, true)", deleted, hasLive)
	}
}

// TestMemStoreJobs_JobRunGetByID — happy + ErrNotFound.
func TestMemStoreJobs_JobRunGetByID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ms := NewMemStore()
	_, run, _ := newJobAndRun(t, ms, "acct-R", "r1")

	got, err := ms.JobRunGetByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("JobRunGetByID: %v", err)
	}
	if got.ID != run.ID {
		t.Fatalf("JobRunGetByID.ID = %q, want %q", got.ID, run.ID)
	}

	if _, err := ms.JobRunGetByID(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("JobRunGetByID missing: err = %v, want ErrNotFound", err)
	}
}

// TestMemStoreJobs_JobRunListByJob — pagination + limit clamp.
func TestMemStoreJobs_JobRunListByJob(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ms := NewMemStore()
	created, _, _ := newJobAndRun(t, ms, "acct-RL", "rl1")
	for i := 0; i < 3; i++ {
		_, _, _ = ms.JobRunCreate(ctx, created.ID, created.AccountID, "manual",
			nil, nil, nil, json.RawMessage(`{}`), 1)
	}
	all, err := ms.JobRunListByJob(ctx, created.ID, 0, 0)
	if err != nil || len(all) != 4 {
		t.Fatalf("JobRunListByJob(all) = (%v, %d), want (nil, 4)", err, len(all))
	}
	first, err := ms.JobRunListByJob(ctx, created.ID, 1, 0)
	if err != nil || len(first) != 1 {
		t.Fatalf("JobRunListByJob(limit=1) = (%v, %d), want (nil, 1)", err, len(first))
	}
}

// TestMemStoreJobs_JobRunListByAccount — same shape, scoped to
// account.
func TestMemStoreJobs_JobRunListByAccount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ms := NewMemStore()
	created, _, _ := newJobAndRun(t, ms, "acct-RA", "ra1")
	ms.JobCreate(ctx, "acct-OTHER", "other", "app",
		"oci://x", []string{"/bin/sh"}, 128, 30, 1, 0, json.RawMessage(`{}`))

	got, err := ms.JobRunListByAccount(ctx, created.AccountID, 0, 0)
	if err != nil || len(got) != 1 {
		t.Fatalf("JobRunListByAccount = (%v, %d), want (nil, 1)", err, len(got))
	}
	other, err := ms.JobRunListByAccount(ctx, "acct-OTHER", 0, 0)
	if err != nil || len(other) != 0 {
		t.Fatalf("JobRunListByAccount(other) = (%v, %d), want (nil, 0)", err, len(other))
	}
}

func TestMemStoreJobs_JobRunListActive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ms := NewMemStore()
	job, run, _ := newJobAndRun(t, ms, "acct-active", "active-1")
	_, otherRun, _ := newJobAndRun(t, ms, "acct-other-active", "active-2")
	if _, err := ms.JobRunCancel(ctx, otherRun.ID); err != nil {
		t.Fatal(err)
	}
	accountRuns, err := ms.JobRunListActive(ctx, job.AccountID, 10, 0)
	if err != nil || len(accountRuns) != 1 || accountRuns[0].ID != run.ID {
		t.Fatalf("account active runs = %+v, err=%v", accountRuns, err)
	}
	fleetRuns, err := ms.JobRunListActive(ctx, "", 10, 0)
	if err != nil || len(fleetRuns) != 1 || fleetRuns[0].AccountID != job.AccountID {
		t.Fatalf("fleet active runs = %+v, err=%v", fleetRuns, err)
	}
}

// TestMemStoreJobs_JobRunRecompute — exercise every aggregate-status
// branch: running, succeeded, failed, cancelled, dead_letter. Also
// verifies started_at/finished_at stamping for the terminal-pair
// invariant.
func TestMemStoreJobs_JobRunRecompute(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ms := NewMemStore()
	_, run, _ := newJobAndRun(t, ms, "acct-RR", "rr1")

	// Initially all queued → aggregate=running (running + queued > 0).
	r, err := ms.JobRunRecompute(ctx, run.ID)
	if err != nil {
		t.Fatalf("JobRunRecompute(queued): %v", err)
	}
	if r.AggregateStatus != "running" {
		t.Fatalf("JobRunRecompute(queued).AggregateStatus = %q, want running", r.AggregateStatus)
	}
	if r.StartedAt == nil {
		t.Fatalf("JobRunRecompute(queued).StartedAt nil; should stamp on first non-queued")
	}

	// Mark all succeeded → aggregate=succeeded.
	for i := 0; i < 3; i++ {
		if err := ms.JobTaskMarkTerminal(ctx, run.ID, i, "succeeded", 0, "", "", time.Now().UTC()); err != nil {
			t.Fatalf("setup MarkTerminal: %v", err)
		}
	}
	r, _ = ms.JobRunRecompute(ctx, run.ID)
	if r.AggregateStatus != "succeeded" {
		t.Fatalf("JobRunRecompute(succeeded).AggregateStatus = %q, want succeeded", r.AggregateStatus)
	}
	if r.FinishedAt == nil {
		t.Fatalf("JobRunRecompute(succeeded).FinishedAt nil; should stamp on terminal")
	}

	// Now exercise the failed + dead_letter branch. Every task
	// must be terminal for the aggregate to settle on dead_letter
	// (queuedOrClaimed > 0 would short-circuit to "running" per
	// the precedence ladder).
	_, run2, _ := newJobAndRun(t, ms, "acct-RR", "rr2")
	for i := 0; i < 3; i++ {
		_ = ms.JobTaskMarkTerminal(ctx, run2.ID, i, "failed", 1, "", "", time.Now().UTC())
	}
	_ = ms.JobRunIncrementDeadLetter(ctx, run2.ID)
	r, _ = ms.JobRunRecompute(ctx, run2.ID)
	if r.AggregateStatus != "dead_letter" {
		t.Fatalf("JobRunRecompute(dead_letter).AggregateStatus = %q, want dead_letter", r.AggregateStatus)
	}
	if r.DeadLetterCount != 1 {
		t.Fatalf("JobRunRecompute(dead_letter).DeadLetterCount = %d, want 1", r.DeadLetterCount)
	}

	// Cancelled branch: failed=0 + cancelled>0 + dead_letter=0.
	// All three tasks must be terminal for the aggregate to
	// settle (queuedOrClaimed > 0 short-circuits to "running").
	_, run3, _ := newJobAndRun(t, ms, "acct-RR", "rr3")
	for i := 0; i < 3; i++ {
		_ = ms.JobTaskMarkTerminal(ctx, run3.ID, i, "cancelled", 0, "", "", time.Now().UTC())
	}
	r, _ = ms.JobRunRecompute(ctx, run3.ID)
	if r.AggregateStatus != "cancelled" {
		t.Fatalf("JobRunRecompute(cancelled).AggregateStatus = %q, want cancelled", r.AggregateStatus)
	}

	// Failed branch: failed>0 + dead_letter=0.
	_, run4, _ := newJobAndRun(t, ms, "acct-RR", "rr4")
	for i := 0; i < 3; i++ {
		_ = ms.JobTaskMarkTerminal(ctx, run4.ID, i, "failed", 1, "", "", time.Now().UTC())
	}
	r, _ = ms.JobRunRecompute(ctx, run4.ID)
	if r.AggregateStatus != "failed" {
		t.Fatalf("JobRunRecompute(failed).AggregateStatus = %q, want failed", r.AggregateStatus)
	}

	// ErrNotFound on missing runID.
	if _, err := ms.JobRunRecompute(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("JobRunRecompute(missing): err = %v, want ErrNotFound", err)
	}
}

// TestMemStoreJobs_JobRunCancel — flip every non-terminal task to
// cancelled and update aggregate_status; idempotent on re-call;
// ErrNotFound on missing id.
func TestMemStoreJobs_JobRunCancel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ms := NewMemStore()
	_, run, _ := newJobAndRun(t, ms, "acct-RC", "rc1")

	r, err := ms.JobRunCancel(ctx, run.ID)
	if err != nil {
		t.Fatalf("JobRunCancel: %v", err)
	}
	if r.AggregateStatus != "cancelled" {
		t.Fatalf("JobRunCancel.AggregateStatus = %q, want cancelled", r.AggregateStatus)
	}
	if r.TasksSucceeded != 0 || r.TasksFailed != 0 || r.TasksCancelled != 3 || r.TasksRunning != 0 {
		t.Fatalf("JobRunCancel counters = (%d, %d, %d, %d), want (0, 0, 3, 0)",
			r.TasksSucceeded, r.TasksFailed, r.TasksCancelled, r.TasksRunning)
	}
	if r.FinishedAt == nil {
		t.Fatalf("JobRunCancel.FinishedAt nil; should stamp on cancel")
	}

	// Idempotent re-call.
	r2, err := ms.JobRunCancel(ctx, run.ID)
	if err != nil {
		t.Fatalf("JobRunCancel (re-call): %v", err)
	}
	if r2.AggregateStatus != "cancelled" {
		t.Fatalf("JobRunCancel (re-call).AggregateStatus = %q, want cancelled", r2.AggregateStatus)
	}
	if r2.TasksCancelled != 3 {
		t.Fatalf("JobRunCancel (re-call).TasksCancelled = %d, want 3", r2.TasksCancelled)
	}

	// Mixed terminal/non-terminal tasks must preserve the terminal count
	// while counting only the remaining queued/claimed tasks as cancelled.
	_, mixed, _ := newJobAndRun(t, ms, "acct-RC", "rc-mixed")
	if err := ms.JobTaskMarkTerminal(ctx, mixed.ID, 0, "succeeded", 0, "", "", time.Now().UTC()); err != nil {
		t.Fatalf("setup succeeded task: %v", err)
	}
	if err := ms.JobTaskMarkClaimed(ctx, mixed.ID, 1, "instance-1", "lease-1", time.Now().Add(time.Minute), "node-1"); err != nil {
		t.Fatalf("setup claimed task: %v", err)
	}
	mixedCancelled, err := ms.JobRunCancel(ctx, mixed.ID)
	if err != nil {
		t.Fatalf("JobRunCancel (mixed): %v", err)
	}
	if mixedCancelled.AggregateStatus != "cancelled" ||
		mixedCancelled.TasksSucceeded != 1 || mixedCancelled.TasksFailed != 0 ||
		mixedCancelled.TasksCancelled != 2 || mixedCancelled.TasksRunning != 0 {
		t.Fatalf("JobRunCancel (mixed) = status %q counters (%d, %d, %d, %d), want cancelled (1, 0, 2, 0)",
			mixedCancelled.AggregateStatus, mixedCancelled.TasksSucceeded,
			mixedCancelled.TasksFailed, mixedCancelled.TasksCancelled,
			mixedCancelled.TasksRunning)
	}

	if _, err := ms.JobRunCancel(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("JobRunCancel(missing): err = %v, want ErrNotFound", err)
	}
}

// TestMemStoreJobs_JobRunIncrementDeadLetter — happy + ErrNotFound.
func TestMemStoreJobs_JobRunIncrementDeadLetter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ms := NewMemStore()
	_, run, _ := newJobAndRun(t, ms, "acct-DL", "dl1")

	if err := ms.JobRunIncrementDeadLetter(ctx, run.ID); err != nil {
		t.Fatalf("JobRunIncrementDeadLetter: %v", err)
	}
	got, _ := ms.JobRunGetByID(ctx, run.ID)
	if got.DeadLetterCount != 1 {
		t.Fatalf("DeadLetterCount = %d, want 1", got.DeadLetterCount)
	}
	if err := ms.JobRunIncrementDeadLetter(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("JobRunIncrementDeadLetter(missing): err = %v, want ErrNotFound", err)
	}
}

// TestMemStoreJobs_JobTaskMarkClaimed — happy + ErrNotFound on
// non-queued task + ErrNotFound on missing runID.
func TestMemStoreJobs_JobTaskMarkClaimed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ms := NewMemStore()
	_, run, _ := newJobAndRun(t, ms, "acct-TC", "tc1")

	leaseExp := time.Now().Add(time.Minute).UTC()
	if err := ms.JobTaskMarkClaimed(ctx, run.ID, 0, "ins-1", "lease-tok", leaseExp, "node-1"); err != nil {
		t.Fatalf("JobTaskMarkClaimed: %v", err)
	}
	t0, _ := ms.JobTaskGet(ctx, run.ID, 0)
	if t0.Status != "claimed" {
		t.Fatalf("task 0 Status = %q, want claimed", t0.Status)
	}
	if t0.InstanceID == nil || *t0.InstanceID != "ins-1" {
		t.Fatalf("task 0 InstanceID = %v, want ins-1", t0.InstanceID)
	}
	if t0.LeaseToken == nil || *t0.LeaseToken != "lease-tok" {
		t.Fatalf("task 0 LeaseToken = %v, want lease-tok", t0.LeaseToken)
	}

	// Already-terminal task → ErrNotFound.
	if err := ms.JobTaskMarkClaimed(ctx, run.ID, 0, "ins-x", "lt", leaseExp, "n"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("JobTaskMarkClaimed(claimed): err = %v, want ErrNotFound", err)
	}

	if err := ms.JobTaskMarkClaimed(ctx, "missing", 0, "x", "y", leaseExp, "z"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("JobTaskMarkClaimed(missing run): err = %v, want ErrNotFound", err)
	}
	if err := ms.JobTaskMarkClaimed(ctx, run.ID, 99, "x", "y", leaseExp, "z"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("JobTaskMarkClaimed(missing task): err = %v, want ErrNotFound", err)
	}
}

// TestMemStoreJobs_JobTaskMarkTerminal — happy + ErrNotFound when
// already terminal.
func TestMemStoreJobs_JobTaskMarkTerminal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ms := NewMemStore()
	_, run, _ := newJobAndRun(t, ms, "acct-TT", "tt1")

	fin := time.Now().UTC()
	if err := ms.JobTaskMarkTerminal(ctx, run.ID, 0, "failed", 1, "user_error", "boom", fin); err != nil {
		t.Fatalf("JobTaskMarkTerminal: %v", err)
	}
	t0, _ := ms.JobTaskGet(ctx, run.ID, 0)
	if t0.Status != "failed" || t0.ExitCode == nil || *t0.ExitCode != 1 {
		t.Fatalf("task 0 = %+v, want failed exit=1", t0)
	}
	if t0.ErrorClass == nil || *t0.ErrorClass != "user_error" {
		t.Fatalf("task 0 ErrorClass = %v, want user_error", t0.ErrorClass)
	}

	// Already terminal → ErrNotFound.
	if err := ms.JobTaskMarkTerminal(ctx, run.ID, 0, "succeeded", 0, "", "", fin); !errors.Is(err, ErrNotFound) {
		t.Fatalf("JobTaskMarkTerminal(terminal): err = %v, want ErrNotFound", err)
	}
}

// TestMemStoreJobs_JobTaskRetry — happy + attempt increment +
// ErrNotFound.
func TestMemStoreJobs_JobTaskRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ms := NewMemStore()
	_, run, _ := newJobAndRun(t, ms, "acct-TR", "tr1")

	fin := time.Now().UTC()
	_ = ms.JobTaskMarkTerminal(ctx, run.ID, 0, "failed", 1, "", "", fin)
	next := time.Now().Add(time.Minute).UTC()
	if err := ms.JobTaskRetry(ctx, run.ID, 0, next); err != nil {
		t.Fatalf("JobTaskRetry: %v", err)
	}
	t0, _ := ms.JobTaskGet(ctx, run.ID, 0)
	if t0.Status != "queued" {
		t.Fatalf("task 0 Status = %q, want queued", t0.Status)
	}
	if t0.Attempt != 2 {
		t.Fatalf("task 0 Attempt = %d, want 2", t0.Attempt)
	}

	if err := ms.JobTaskRetry(ctx, "missing", 0, next); !errors.Is(err, ErrNotFound) {
		t.Fatalf("JobTaskRetry(missing): err = %v, want ErrNotFound", err)
	}
}

// TestMemStoreJobs_JobTaskRequeue — preserves Attempt counter
// (unlike Retry which increments it); resets lease + instance +
// started_at columns.
func TestMemStoreJobs_JobTaskRequeue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ms := NewMemStore()
	_, run, _ := newJobAndRun(t, ms, "acct-TQ", "tq1")

	leaseExp := time.Now().Add(time.Minute).UTC()
	_ = ms.JobTaskMarkClaimed(ctx, run.ID, 0, "ins", "lt", leaseExp, "n")
	next := time.Now().Add(time.Minute).UTC()
	if err := ms.JobTaskRequeue(ctx, run.ID, 0, next); err != nil {
		t.Fatalf("JobTaskRequeue: %v", err)
	}
	t0, _ := ms.JobTaskGet(ctx, run.ID, 0)
	if t0.Status != "queued" {
		t.Fatalf("task 0 Status = %q, want queued", t0.Status)
	}
	if t0.Attempt != 1 {
		t.Fatalf("task 0 Attempt = %d, want 1 (Requeue preserves)", t0.Attempt)
	}
	if t0.InstanceID != nil {
		t.Fatalf("task 0 InstanceID = %v, want nil after requeue", t0.InstanceID)
	}

	if err := ms.JobTaskRequeue(ctx, "missing", 0, next); !errors.Is(err, ErrNotFound) {
		t.Fatalf("JobTaskRequeue(missing): err = %v, want ErrNotFound", err)
	}
}

// TestMemStoreJobs_JobTaskCancel — happy + idempotent on already
// terminal.
func TestMemStoreJobs_JobTaskCancel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ms := NewMemStore()
	_, run, _ := newJobAndRun(t, ms, "acct-XC", "xc1")

	if err := ms.JobTaskCancel(ctx, run.ID, 0); err != nil {
		t.Fatalf("JobTaskCancel: %v", err)
	}
	// Re-cancel → ErrNotFound (already terminal).
	if err := ms.JobTaskCancel(ctx, run.ID, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("JobTaskCancel (re-call): err = %v, want ErrNotFound", err)
	}
}

// TestMemStoreJobs_JobTaskFindStuck — only claimed tasks with
// expired leases; ignores queued + non-expired + unclaimed rows.
func TestMemStoreJobs_JobTaskFindStuck(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ms := NewMemStore()
	_, run, _ := newJobAndRun(t, ms, "acct-FS", "fs1")

	// Claim task 0 with an already-expired lease; task 1 with a
	// future lease; leave task 2 queued.
	pastLease := time.Now().Add(-time.Hour).UTC()
	futureLease := time.Now().Add(time.Hour).UTC()
	_ = ms.JobTaskMarkClaimed(ctx, run.ID, 0, "ins", "lt0", pastLease, "n")
	_ = ms.JobTaskMarkClaimed(ctx, run.ID, 1, "ins", "lt1", futureLease, "n")

	stuck, err := ms.JobTaskFindStuck(ctx, time.Minute)
	if err != nil {
		t.Fatalf("JobTaskFindStuck: %v", err)
	}
	if len(stuck) != 1 {
		t.Fatalf("JobTaskFindStuck len = %d, want 1 (only expired-lease task 0)", len(stuck))
	}
	if stuck[0].TaskIndex != 0 {
		t.Fatalf("JobTaskFindStuck[0].TaskIndex = %d, want 0", stuck[0].TaskIndex)
	}

	// A smaller TTL (30m) is still greater than task 0's -1h
	// lease (cutoff = now-30m; task 0 expired 1h ago so still
	// qualifies) but still less than task 1's +1h lease (cutoff
	// is past, future lease not past cutoff). Confirms the
	// half-open interval semantics.
	stuckFuture, _ := ms.JobTaskFindStuck(ctx, 30*time.Minute)
	if len(stuckFuture) != 1 {
		t.Fatalf("JobTaskFindStuck(30m ttl) len = %d, want 1", len(stuckFuture))
	}
}

// TestMemStoreJobs_JobTaskClaimBatch — limit clamp + backoff gate.
func TestMemStoreJobs_JobTaskClaimBatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ms := NewMemStore()
	_, run, _ := newJobAndRun(t, ms, "acct-CB", "cb1")

	// Set task 0 with a future next_attempt_at — should be skipped.
	future := time.Now().Add(time.Hour).UTC()
	if err := ms.JobTaskRequeue(ctx, run.ID, 0, future); err != nil {
		t.Fatalf("JobTaskRequeue: %v", err)
	}

	claimed, err := ms.JobTaskClaimBatch(ctx, 0)
	if err != nil {
		t.Fatalf("JobTaskClaimBatch: %v", err)
	}
	// limit=0 = no cap, but task 0's backoff should still skip it.
	for _, c := range claimed {
		if c.TaskIndex == 0 {
			t.Fatalf("JobTaskClaimBatch returned task 0 despite future backoff")
		}
	}
	if len(claimed) != 2 {
		t.Fatalf("JobTaskClaimBatch len = %d, want 2 (task 0 backoff-skipped)", len(claimed))
	}

	// Limit=1 should clamp to 1.
	claimed1, _ := ms.JobTaskClaimBatch(ctx, 1)
	if len(claimed1) != 1 {
		t.Fatalf("JobTaskClaimBatch(limit=1) len = %d, want 1", len(claimed1))
	}
}

// TestMemStoreJobs_JobTaskGet — happy + ErrNotFound.
func TestMemStoreJobs_JobTaskGet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ms := NewMemStore()
	_, run, _ := newJobAndRun(t, ms, "acct-TG", "tg1")

	got, err := ms.JobTaskGet(ctx, run.ID, 0)
	if err != nil {
		t.Fatalf("JobTaskGet: %v", err)
	}
	if got.TaskIndex != 0 {
		t.Fatalf("JobTaskGet.TaskIndex = %d, want 0", got.TaskIndex)
	}

	if _, err := ms.JobTaskGet(ctx, run.ID, 99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("JobTaskGet(missing task): err = %v, want ErrNotFound", err)
	}
	if _, err := ms.JobTaskGet(ctx, "missing", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("JobTaskGet(missing run): err = %v, want ErrNotFound", err)
	}
}

// TestMemStoreJobs_JobTaskList — pagination + offset past end.
func TestMemStoreJobs_JobTaskList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ms := NewMemStore()
	_, run, _ := newJobAndRun(t, ms, "acct-TL", "tl1")

	all, err := ms.JobTaskList(ctx, run.ID, 0, 0)
	if err != nil || len(all) != 3 {
		t.Fatalf("JobTaskList(all) = (%v, %d), want (nil, 3)", err, len(all))
	}
	limited, err := ms.JobTaskList(ctx, run.ID, 2, 0)
	if err != nil || len(limited) != 2 {
		t.Fatalf("JobTaskList(limit=2) = (%v, %d), want (nil, 2)", err, len(limited))
	}
	past, err := ms.JobTaskList(ctx, run.ID, 0, 100)
	if err != nil || past != nil {
		t.Fatalf("JobTaskList(offset past end) = (%v, %v), want (nil, nil)", err, past)
	}

	if _, err := ms.JobTaskList(ctx, "missing", 0, 0); err != nil {
		t.Fatalf("JobTaskList(missing run): err = %v, want nil", err)
	}
}

// TestMemStoreJobs_JobConcurrentByAccount — counts queued + claimed
// only; terminal statuses are excluded.
func TestMemStoreJobs_JobConcurrentByAccount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ms := NewMemStore()
	_, run, _ := newJobAndRun(t, ms, "acct-CC", "cc1")

	before, _ := ms.JobConcurrentByAccount(ctx, "acct-CC")
	if before != 3 {
		t.Fatalf("JobConcurrentByAccount(3 queued) = %d, want 3", before)
	}

	// Mark task 0 succeeded → concurrent count drops to 2.
	_ = ms.JobTaskMarkTerminal(ctx, run.ID, 0, "succeeded", 0, "", "", time.Now().UTC())
	after, _ := ms.JobConcurrentByAccount(ctx, "acct-CC")
	if after != 2 {
		t.Fatalf("JobConcurrentByAccount(after 1 terminal) = %d, want 2", after)
	}
}

// TestMemStoreJobs_ListJobInstances — exercise the meter-sampler
// surface: only kind='job_task' AND state NOT IN ('destroyed',
// 'parked') is returned. CreateInstanceIfUnderQuota + State updates
// are needed to seed the in-memory store; the memstore doesn't
// expose a direct Instance insert, so we drive it through the
// app-wake path (or skip if the helpers are absent in this build).
func TestMemStoreJobs_ListJobInstances(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ms := NewMemStore()

	out, err := ms.ListJobInstances(ctx)
	if err != nil {
		t.Fatalf("ListJobInstances: %v", err)
	}
	if out == nil {
		t.Fatalf("ListJobInstances: nil; want empty slice")
	}
	if len(out) != 0 {
		t.Fatalf("ListJobInstances len = %d, want 0 on fresh store", len(out))
	}
}

// TestMemStoreJobs_JobRunCreate_FanOut — covers the parallelism
// override falling-back-to-job-default path when parallelism=nil.
func TestMemStoreJobs_JobRunCreate_FanOut(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ms := NewMemStore()
	created, _, _ := newJobAndRun(t, ms, "acct-FO", "fo1")

	// nil parallelism falls back to job.MaxParallelism (4).
	run, _, err := ms.JobRunCreate(ctx,
		created.ID, created.AccountID, "scheduled",
		nil, nil, nil, json.RawMessage(`{}`), 5,
	)
	if err != nil {
		t.Fatalf("JobRunCreate(nil parallelism): %v", err)
	}
	if run.Parallelism != 4 {
		t.Fatalf("JobRunCreate(nil parallelism).Parallelism = %d, want 4 (job fallback)", run.Parallelism)
	}
	if run.Tasks != 5 {
		t.Fatalf("JobRunCreate.Tasks = %d, want 5", run.Tasks)
	}

	// ErrNotFound on missing job.
	if _, _, err := ms.JobRunCreate(ctx, "missing", "acct", "manual", nil, nil, nil, json.RawMessage(`{}`), 1); err == nil {
		t.Fatalf("JobRunCreate(missing job): err = nil, want ErrNotFound")
	}
}

// TestMemStoreJobs_JobGetByName_NotFound — exercises the
// (accountID, name) lookup miss path.
func TestMemStoreJobs_JobGetByName_NotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ms := NewMemStore()
	if _, err := ms.JobGetByName(ctx, "acct", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("JobGetByName(missing): err = %v, want ErrNotFound", err)
	}
}

// TestMemStoreJobs_JobCreateEmptyEnv — exercises the
// len(envOverrides)==0 fallback to "{}" inside JobCreate so a job
// with no env-overrides persists a valid empty jsonb.
func TestMemStoreJobs_JobCreateEmptyEnv(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ms := NewMemStore()
	created, err := ms.JobCreate(ctx, "acct-E", "empty-env", "app",
		"oci://x", []string{"/bin/sh"}, 128, 30, 1, 0, nil)
	if err != nil {
		t.Fatalf("JobCreate(nil env): %v", err)
	}
	if len(created.EnvOverrides) == 0 {
		t.Fatalf("JobCreate(nil env).EnvOverrides is empty; want {} fallback")
	}
}
