package sched

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/state"
)

func taskObservationStore(t *testing.T, backend string) state.Store {
	t.Helper()
	if backend == "postgres" {
		return state.NewPgStore(pgtest.OpenMigrated(t))
	}
	return state.NewMemStore()
}

func observationTask(t *testing.T, s state.Store, slug string, kind state.AppTaskKind, at time.Time, deadline *time.Time) state.AppTask {
	t.Helper()
	account, err := s.CreateAccount(t.Context(), slug+"@example.com", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	a, d := primeObservationCandidate(t, s, account.ID, slug, nil)
	params := state.CreateAppTaskParams{AccountID: a.AccountID, AppID: a.ID, DeploymentID: d.ID, Kind: kind, Command: []string{"/app/private-command", "secret-argument"}, CreatedAt: at, StartDeadlineAt: deadline}
	task, err := s.CreateAppTask(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func observeTasks(t *testing.T, s state.Store, at time.Time) state.AppTaskObservationSnapshot {
	t.Helper()
	got, err := s.(state.AppTaskObservationStore).AppTaskObservationSnapshot(t.Context(), at)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateAppTaskObservation(got, at); err != nil {
		t.Fatal(err, got)
	}
	return got
}

func expectTaskCohort(t *testing.T, s state.Store, at time.Time, kind int, class state.AppTaskObservationClass, known time.Time) {
	t.Helper()
	snap := observeTasks(t, s, at)
	var want state.AppTaskObservationSnapshot
	p := state.AppTaskObservationCohort{Pending: 1, OldestAt: known}
	if known.IsZero() {
		p.UnknownAge = 1
	}
	want[kind][class] = p
	for k, classes := range snap {
		for c, p := range classes {
			w := want[k][c]
			if p.Pending != w.Pending || p.UnknownAge != w.UnknownAge || !p.OldestAt.Equal(w.OldestAt) {
				t.Fatalf("%s/%s=%+v want=%+v", state.AppTaskObservationKind(k), state.AppTaskObservationClass(c), p, w)
			}
		}
	}
}

// adr: 20261017141024523 — Bounded task evidence, freshness and coordinator progress.
func TestAppTaskObservationLifecycleContract(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		for k, kind := range []state.AppTaskKind{state.AppTaskKindRelease, state.AppTaskKindManual} {
			t.Run(backend+"/"+string(kind), func(t *testing.T) {
				s := taskObservationStore(t, backend)
				base := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
				if got := observeTasks(t, s, base); got != (state.AppTaskObservationSnapshot{}) {
					t.Fatal(got)
				}
				task := observationTask(t, s, "task-life", kind, base, nil)
				expectTaskCohort(t, s, base, k, state.AppTaskObservationQueued, base)
				claimed, err := s.ClaimNextAppTask(t.Context(), "runner", base.Add(time.Minute), 30*time.Second)
				if err != nil || claimed.ID != task.ID {
					t.Fatal(err)
				}
				expectTaskCohort(t, s, base.Add(time.Minute), k, state.AppTaskObservationRestoring, time.Time{})
				if err := s.RenewAppTaskLease(t.Context(), task.ID, *claimed.LeaseToken, base.Add(65*time.Second), 30*time.Second); err != nil {
					t.Fatal(err)
				}
				expectTaskCohort(t, s, base.Add(65*time.Second), k, state.AppTaskObservationRestoring, time.Time{})
				started := base.Add(70 * time.Second)
				if _, err := s.MarkAppTaskRunning(t.Context(), task.ID, *claimed.LeaseToken, started); err != nil {
					t.Fatal(err)
				}
				if err := s.RenewAppTaskLease(t.Context(), task.ID, *claimed.LeaseToken, base.Add(75*time.Second), 30*time.Second); err != nil {
					t.Fatal(err)
				}
				expectTaskCohort(t, s, base.Add(75*time.Second), k, state.AppTaskObservationRunning, started)
				cancelAt := base.Add(80 * time.Second)
				if _, err := s.RequestAppTaskCancellation(t.Context(), task.AccountID, task.AppID, task.ID, cancelAt); err != nil {
					t.Fatal(err)
				}
				expectTaskCohort(t, s, cancelAt, k, state.AppTaskObservationCancellationPending, cancelAt)
				expiry := base.Add(105 * time.Second)
				expectTaskCohort(t, s, expiry, k, state.AppTaskObservationLeaseExpired, expiry)
				if r, err := s.SweepExpiredAppTasks(t.Context(), expiry); err != nil || r.Cancelled != 1 {
					t.Fatal(r, err)
				}
				if got := observeTasks(t, s, expiry); got != (state.AppTaskObservationSnapshot{}) {
					t.Fatal("terminal retained", got)
				}
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if _, err := s.(state.AppTaskObservationStore).AppTaskObservationSnapshot(ctx, expiry); err == nil {
					t.Fatal("canceled read succeeded")
				}
			})
		}
	}
}

func TestAppTaskObservationRecoveryAndDeadlines(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := taskObservationStore(t, backend)
			base := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
			task := observationTask(t, s, "task-recovery", state.AppTaskKindRelease, base, nil)
			claimed, err := s.ClaimNextAppTask(t.Context(), "runner", base.Add(time.Minute), 30*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			expiry := base.Add(90 * time.Second)
			expectTaskCohort(t, s, expiry, 0, state.AppTaskObservationLeaseExpired, expiry)
			if r, err := s.SweepExpiredAppTasks(t.Context(), expiry); err != nil || r.RequeuedRestores != 1 {
				t.Fatal(r, err)
			}
			expectTaskCohort(t, s, expiry, 0, state.AppTaskObservationQueued, expiry)
			next, err := s.ClaimNextAppTask(t.Context(), "runner-2", expiry, 30*time.Second)
			if err != nil || next.LeaseToken == nil || *next.LeaseToken == *claimed.LeaseToken {
				t.Fatal(next, err)
			}
			if _, err := s.MarkAppTaskRunning(t.Context(), task.ID, *next.LeaseToken, expiry); err != nil {
				t.Fatal(err)
			}
			expiry = expiry.Add(30 * time.Second)
			if r, err := s.SweepExpiredAppTasks(t.Context(), expiry); err != nil || r.FailedRuns != 1 {
				t.Fatal(r, err)
			}
			if got := observeTasks(t, s, expiry); got != (state.AppTaskObservationSnapshot{}) {
				t.Fatal(got)
			}
			deadline := base.Add(10 * time.Minute)
			observationTask(t, s, "task-deadline", state.AppTaskKindManual, base, &deadline)
			expectTaskCohort(t, s, deadline, 1, state.AppTaskObservationQueued, base)
			expectTaskCohort(t, s, deadline.Add(time.Microsecond), 1, state.AppTaskObservationDeadlineOverdue, deadline)
		})
	}
}

func TestAppTaskObservationFullBacklogAndFutureEvidence(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := taskObservationStore(t, backend)
			base := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
			task := observationTask(t, s, "task-backlog", state.AppTaskKindManual, base, nil)
			for i := range 300 {
				_, err := s.CreateAppTask(t.Context(), state.CreateAppTaskParams{AccountID: task.AccountID, AppID: task.AppID, DeploymentID: task.DeploymentID, Kind: state.AppTaskKindManual, Command: []string{fmt.Sprintf("/app/task-%d", i)}, CreatedAt: base.Add(time.Duration(i+1) * time.Microsecond)})
				if err != nil {
					t.Fatal(err)
				}
			}
			got := observeTasks(t, s, base.Add(time.Minute))
			if got[1][state.AppTaskObservationQueued].Pending != 301 || !got[1][state.AppTaskObservationQueued].OldestAt.Equal(base) {
				t.Fatal(got)
			}
			got = observeTasks(t, s, base)
			if got[1][state.AppTaskObservationQueued].Pending != 1 || got[1][state.AppTaskObservationUnknown].Pending != 300 {
				t.Fatal("future creation fabricated eligible work", got)
			}
		})
	}
}

func TestAppTaskObservationExclusiveHoldContract(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := taskObservationStore(t, backend)
			ctx := t.Context()
			old := observationTask(t, s, "task-exclusive", state.AppTaskKindManual, time.Now().UTC().Add(-time.Minute), nil)
			if _, err := s.RequestAppTaskCancellation(ctx, old.AccountID, old.AppID, old.ID, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			owners := s.(state.ExclusiveWorkStore)
			if _, err := owners.UpsertExclusiveWorkPolicy(ctx, old.AccountID, exclusivework.Policy{Name: "task-observation", Scope: "account", MemberAppIDs: []string{old.AppID}, Contention: "queue", LeaseSeconds: 5, MaxAttemptSeconds: 60}); err != nil {
				t.Fatal(err)
			}
			op, _, err := owners.AdmitExclusiveOperation(ctx, state.ExclusiveAdmission{AccountID: old.AccountID, AppID: old.AppID, PolicyName: "task-observation", Key: json.RawMessage(`"observation"`), Request: json.RawMessage(`{"kind":"app_task"}`)})
			if err != nil {
				t.Fatal(err)
			}
			owner, err := owners.ClaimExclusiveOperation(ctx, old.AccountID, op.ID, "task-observation")
			if err != nil {
				t.Fatal(err)
			}
			at := time.Now().UTC().Truncate(time.Microsecond)
			if _, err := s.CreateAppTask(ctx, state.CreateAppTaskParams{AccountID: old.AccountID, AppID: old.AppID, DeploymentID: old.DeploymentID, Kind: state.AppTaskKindManual, Command: []string{"/app/task"}, ExclusiveOperationID: op.ID, ExclusiveGeneration: owner.Generation, CreatedAt: at}); err != nil {
				t.Fatal(err)
			}
			expectTaskCohort(t, s, at, 1, state.AppTaskObservationQueued, at)
			expectTaskCohort(t, s, at.Add(6*time.Second), 1, state.AppTaskObservationHeld, time.Time{})
		})
	}
}
