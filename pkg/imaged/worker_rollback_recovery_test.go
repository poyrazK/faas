package imaged_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func checkedWorkerRollbackFixture(t *testing.T, s state.Store, ready, targetNewer bool) (state.App, state.Deployment, state.Deployment, state.Instance, api.RollbackOperation) {
	t.Helper()
	ctx := t.Context()
	app, target := readyAttemptWorker(t, s)
	if err := s.UpsertAppEnv(ctx, app.AccountID, app.ID, "AUDIT_VERSION", "old"); err != nil {
		t.Fatal(err)
	}
	old, _ := readyAttemptPrime(t, s, app, target)
	if err := readyAttemptActivate(t, s, target, old); err != nil {
		t.Fatal(err)
	}
	current := readyAttemptDeployment(t, s, app, state.DeploySnapshotting)
	currentWorker, _ := readyAttemptPrime(t, s, app, current)
	if err := readyAttemptActivate(t, s, current, currentWorker); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateInstanceState(ctx, old.ID, "stopped"); err != nil {
		t.Fatal(err)
	}
	if targetNewer {
		// The serving predecessor can itself be an earlier rollback. Its
		// creation timestamp must not let reconciliation replace this pair.
		prepared, err := s.PrepareDeploymentRollback(ctx, app.ID, target.ID)
		if err != nil {
			t.Fatal(err)
		}
		restored, _ := readyAttemptPrime(t, s, app, prepared)
		if err := readyAttemptActivate(t, s, prepared, restored); err != nil {
			t.Fatal(err)
		}
		target, current = current, target
		if err := s.UpdateInstanceState(ctx, currentWorker.ID, "stopped"); err != nil {
			t.Fatal(err)
		}
	}
	rstore := s.(state.CheckedRollbackStore)
	op, err := rstore.CreateCheckedRollback(ctx, app.AccountID, app.ID, target.ID, current.ID, "audit11 worker rollback")
	if err != nil {
		t.Fatal(err)
	}
	worker, _ := readyAttemptPrime(t, s, app, target)
	if ready {
		if err := readyAttemptActivate(t, s, target, worker); err != nil {
			t.Fatal(err)
		}
	}
	op, err = rstore.GetCheckedRollback(ctx, app.AccountID, app.ID, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	return app, target, current, worker, op
}

func TestWorkerRecoveryPreservesPendingCheckedRollback(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := readyAttemptStore(t, backend)
			for _, phase := range []string{"snapshotting", "ready", "blocked", "complete", "failed", "newer-ready", "newer-blocked"} {
				t.Run(phase, func(t *testing.T) {
					ctx := t.Context()
					targetNewer := phase == "newer-ready" || phase == "newer-blocked"
					if targetNewer {
						phase = phase[len("newer-"):]
					}
					app, target, current, worker, op := checkedWorkerRollbackFixture(t, s, phase != "snapshotting", targetNewer)
					if phase == "blocked" || phase == "failed" {
						if err := s.(state.CheckedRollbackStore).UpdateCheckedRollback(ctx, op, phase, "regression", nil); err != nil {
							t.Fatal(err)
						}
					}
					if phase == "complete" {
						var err error
						op, err = s.(state.CheckedRollbackStore).CommitCheckedRollback(ctx, op)
						if err != nil {
							t.Fatal(err)
						}
					}
					v := &readyAttemptVM{}
					e := readyAttemptEngine(t, s, v)
					if err := e.SeedLedger(ctx); err != nil {
						t.Fatal(err)
					}
					if err := e.ReconcileWorkerPools(ctx, app.ID, "audit11"); err != nil {
						t.Fatal(err)
					}
					e.WaitWorkerStops()
					got, err := s.InstanceByID(ctx, worker.ID)
					if err != nil {
						t.Fatal(err)
					}
					d, _ := s.DeploymentByID(ctx, target.ID)
					old, _ := s.DeploymentByID(ctx, current.ID)
					latest, err := s.(state.CheckedRollbackStore).GetCheckedRollback(ctx, app.AccountID, app.ID, op.ID)
					if err != nil {
						t.Fatal(err)
					}
					t.Logf("phase=%s operation=%s worker=%s target=%s/%d predecessor=%s/%d", phase, latest.Status, got.State, d.Status, d.TrafficPercent, old.Status, old.TrafficPercent)
					if phase == "failed" {
						if got.State != "stopped" {
							t.Fatalf("failed rollback worker was retained: %s", got.State)
						}
						return
					}
					if got.State != "running" {
						t.Error("worker recovery drained the still-needed rollback readiness worker")
					}
					if phase == "ready" || phase == "blocked" {
						rows, err := s.ListInstancesForApp(ctx, app.ID)
						if err != nil {
							t.Fatal(err)
						}
						running := false
						for _, i := range rows {
							running = running || (i.DeploymentID == current.ID && i.State == "running")
						}
						if !running {
							t.Error("recovery drained the serving predecessor before commit")
						}
					}
				})
			}
		})
	}
}
