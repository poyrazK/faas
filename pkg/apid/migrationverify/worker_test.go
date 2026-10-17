// adr:1573 — server-driven migration checks and durable restoration.
package migrationverify

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func workerFor(b Backend, m *state.MemStore) Worker {
	return Worker{Store: m, Backend: func(context.Context, state.MigrationVerificationRecord) (Backend, error) { return b, nil }}
}

func reschedule(t *testing.T, b Backend, edit func(*state.MigrationVerificationRecord)) state.MigrationVerificationRecord {
	t.Helper()
	r, err := b.Store.GetMigrationVerification(t.Context(), b.AccountID, b.AppID, mustRunID(t, b))
	if err != nil {
		t.Fatal(err)
	}
	edit(&r)
	r, err = b.Store.SaveMigrationVerification(t.Context(), r, r.Version, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func mustRunID(t *testing.T, b Backend) string {
	t.Helper()
	rows, err := b.Store.ListMigrationVerifications(t.Context(), b.AccountID, b.AppID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("runs=%d err=%v", len(rows), err)
	}
	return rows[0].Report.ID
}

func due(t *testing.T, b Backend) {
	t.Helper()
	reschedule(t, b, func(r *state.MigrationVerificationRecord) { r.NextAttemptAt = time.Now().Add(-time.Second) })
}

func TestWorkerCompletesWithoutClientAndResumesSavedTaskAfterRestart(t *testing.T) {
	b, m, _, in := fixture(t)
	r, err := b.Create(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if err := workerFor(b, m).Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	saved, err := b.Get(t.Context(), r.ID)
	if err != nil || saved.DependencyTaskID == "" {
		t.Fatalf("worker did not admit task: %+v %v", saved, err)
	}
	firstTask := saved.DependencyTaskID
	// Simulate a daemon disappearing with a claimed step. Another worker must
	// wait for that lease, then continue with the existing task identity.
	reschedule(t, b, func(r *state.MigrationVerificationRecord) {
		r.LeaseUntil = time.Now().Add(time.Minute)
		r.NextAttemptAt = time.Now()
	})
	completeDependency(t, m, "postgres", state.AppTaskSucceeded, "")
	restarted := workerFor(b, m)
	if err := restarted.Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	loaded, _ := b.Get(t.Context(), r.ID)
	if loaded.Phase != "dependencies" || loaded.DependencyTaskID != firstTask {
		t.Fatal("worker bypassed lease or replaced task")
	}
	reschedule(t, b, func(r *state.MigrationVerificationRecord) {
		r.LeaseUntil = time.Now().Add(-time.Second)
		r.NextAttemptAt = time.Now().Add(-time.Second)
	})
	for range 12 {
		if err := restarted.Drain(t.Context()); err != nil {
			t.Fatal(err)
		}
		loaded, err = b.Get(t.Context(), r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.ColdDependencyTaskID != "" {
			break
		}
		due(t, b)
	}
	if loaded.ColdDependencyTaskID == "" || loaded.DependencyTaskID != firstTask {
		t.Fatalf("lost resume identity: %+v", loaded)
	}
	completeDependency(t, m, "postgres", state.AppTaskSucceeded, "")
	due(t, b)
	if err := restarted.Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	loaded, err = b.Get(t.Context(), r.ID)
	if err != nil || loaded.Status != "passed" {
		t.Fatalf("background result: %+v %v", loaded, err)
	}
	rows, err := m.ListAppTasks(t.Context(), b.AccountID, b.AppID, 20, 0)
	if err != nil || len(rows) != 2 {
		t.Fatalf("duplicated tasks=%d %v", len(rows), err)
	}
	ready, err := m.ListDueMigrationVerifications(t.Context(), time.Now().Add(time.Hour), 32)
	if err != nil || len(ready) != 0 {
		t.Fatalf("terminal run remained queued: %d %v", len(ready), err)
	}
}

func TestCancellationFencesInFlightParkAndSurvivesRestart(t *testing.T) {
	b, m, _, in := fixture(t)
	in.Checks = api.MigrationDependencyChecks{}
	r, err := b.Create(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	r = advance(t, b, r.ID)
	r = advance(t, b, r.ID)
	if r.Phase != "park" {
		t.Fatal(r.Phase)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var parks, restores atomic.Int32
	b.Park = func(context.Context, api.MigrationVerification) error {
		parks.Add(1)
		close(started)
		<-release
		return nil
	}
	b.Restore = func(context.Context, api.MigrationVerification) (string, error) {
		if restores.Add(1) == 1 {
			return "pending", nil
		}
		return "restored", nil
	}
	done := make(chan error, 1)
	go func() { _, err := b.Advance(t.Context(), r.ID); done <- err }()
	<-started
	saved, err := m.GetMigrationVerification(t.Context(), b.AccountID, b.AppID, r.ID)
	if err != nil || !saved.RestoreRequired {
		t.Fatal("park obligation not saved before side effect")
	}
	cancelled, err := b.Cancel(t.Context(), r.ID)
	if err != nil || cancelled.CancelRequestedAt == nil {
		t.Fatalf("cancel: %+v %v", cancelled, err)
	}
	if _, err := b.Cancel(t.Context(), r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Advance(t.Context(), r.ID); err != nil {
		t.Fatal(err)
	}
	if restores.Load() != 0 {
		t.Fatal("cleanup overtook an in-flight park")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	saved, _ = m.GetMigrationVerification(t.Context(), b.AccountID, b.AppID, r.ID)
	if saved.Report.Phase != "park" || saved.Report.CancelRequestedAt == nil {
		t.Fatal("stale step overwrote cancellation")
	}
	if saved.LeaseUntil.After(time.Now()) {
		t.Fatal("finished step did not release its cancelled lease")
	}
	reschedule(t, b, func(r *state.MigrationVerificationRecord) {
		r.LeaseUntil = time.Now().Add(-time.Second)
		r.NextAttemptAt = time.Now().Add(-time.Second)
	})
	w := workerFor(b, m)
	if err := w.Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, err := b.Get(t.Context(), r.ID)
	if err != nil || current.Status != "pending" || current.Phase != "restoring" || current.RecoveryStatus != "pending" {
		t.Fatalf("cleanup falsely complete: %+v %v", current, err)
	}
	other := in
	other.ID = uuid.NewString()
	if _, err := b.Create(t.Context(), other); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("overlapping park accepted: %v", err)
	}
	due(t, b)
	if err := workerFor(b, m).Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, err = b.Get(t.Context(), r.ID)
	if err != nil || current.Status != "cancelled" || current.RecoveryStatus != "restored" || current.CompletedAt == nil {
		t.Fatalf("cancel outcome: %+v %v", current, err)
	}
	if parks.Load() != 1 {
		t.Fatal("replayed park after cancellation")
	}
	before := current.CompletedAt
	current, err = b.Cancel(t.Context(), r.ID)
	if err != nil || !current.CompletedAt.Equal(*before) {
		t.Fatal("terminal cancel changed result")
	}
}

func TestDeadlineCancelsQueuedDependencyAndRetainsCleanupBound(t *testing.T) {
	b, m, _, in := fixture(t)
	r, err := b.Create(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	r = advance(t, b, r.ID)
	reschedule(t, b, func(r *state.MigrationVerificationRecord) { r.Report.ExpiresAt = time.Now().Add(-time.Second) })
	other := in
	other.ID = uuid.NewString()
	if _, err := b.Create(t.Context(), other); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("expired cleanup bypassed active bound: %v", err)
	}
	r = advance(t, b, r.ID)
	if r.Status != "expired" || r.CompletedAt == nil {
		t.Fatalf("deadline: %+v", r)
	}
	task, err := m.AppTaskByID(t.Context(), b.AccountID, b.AppID, r.DependencyTaskID)
	if err != nil || task.Status != state.AppTaskCancelled {
		t.Fatalf("task not cancelled: %+v %v", task, err)
	}
}

func TestDeadlineRecoveryRetriesOriginalWakeUntilRunning(t *testing.T) {
	b, _, _, in := fixture(t)
	in.Checks = api.MigrationDependencyChecks{}
	r, err := b.Create(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		r = advance(t, b, r.ID)
	}
	if r.Phase != "drain" {
		t.Fatal(r.Phase)
	}
	wakeID := r.WakeID
	reschedule(t, b, func(r *state.MigrationVerificationRecord) { r.Report.ExpiresAt = time.Now().Add(-time.Second) })
	calls := 0
	b.Restore = func(ctx context.Context, run api.MigrationVerification) (string, error) {
		if ctx.Err() != nil {
			t.Error("cleanup inherited expired verification deadline")
		}
		if run.WakeID != wakeID {
			t.Error("recovery replaced wake identity")
		}
		calls++
		if calls == 1 {
			return "", errors.New("publication lost")
		}
		if calls == 2 {
			return "pending", nil
		}
		return "restored", nil
	}
	if _, err := b.Advance(t.Context(), r.ID); err == nil {
		t.Fatal("lost infrastructure failure")
	}
	r = advance(t, b, r.ID)
	if r.Status != "pending" || r.Phase != "restoring" {
		t.Fatalf("notification treated as restored: %+v", r)
	}
	r = advance(t, b, r.ID)
	if r.Status != "expired" || r.RecoveryStatus != "restored" {
		t.Fatalf("restoration result: %+v", r)
	}
}

func TestCancellationWaitsForRunningTaskShutdown(t *testing.T) {
	b, m, _, in := fixture(t)
	r, err := b.Create(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	r = advance(t, b, r.ID)
	task, err := m.ClaimNextAppTask(t.Context(), "fixture", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	task, err = m.MarkAppTaskRunning(t.Context(), task.ID, *task.LeaseToken, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Cancel(t.Context(), r.ID); err != nil {
		t.Fatal(err)
	}
	r = advance(t, b, r.ID)
	if r.Status != "pending" || r.Phase != "restoring" {
		t.Fatalf("running task cleanup lost: %+v", r)
	}
	current, _ := m.AppTaskByID(t.Context(), b.AccountID, b.AppID, task.ID)
	if current.CancelRequested == nil {
		t.Fatal("task owner did not receive cancellation")
	}
	_, err = m.CompleteAppTask(t.Context(), state.CompleteAppTaskParams{ID: task.ID, LeaseToken: *task.LeaseToken, Status: state.AppTaskCancelled, FinishedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	r = advance(t, b, r.ID)
	if r.Status != "cancelled" {
		t.Fatalf("never completed cancellation: %+v", r)
	}
}

func TestWorkerShutdownPreservesRetryableStep(t *testing.T) {
	b, m, _, in := fixture(t)
	in.Checks = api.MigrationDependencyChecks{}
	if _, err := b.Create(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	advance(t, b, in.ID)
	due(t, b)
	started := make(chan struct{})
	b.Smoke = func(ctx context.Context, _ api.MigrationVerification) (api.MigrationVerificationCheck, error) {
		close(started)
		<-ctx.Done()
		return api.MigrationVerificationCheck{}, ctx.Err()
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { workerFor(b, m).Run(ctx); close(done) }()
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
	r, err := m.GetMigrationVerification(t.Context(), b.AccountID, b.AppID, in.ID)
	if err != nil || r.Report.Phase != "startup" || r.Report.Status != "pending" || r.LeaseUntil.After(time.Now()) {
		t.Fatalf("shutdown lost progress: %+v %v", r, err)
	}
}
