package state_test

// adr: 606

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRuntimeUpgradeCancellationFencesClaimAndFreshAcceptance(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		for _, phase := range []string{"claimed_prepared", "live_candidate", "historical_cutover"} {
			t.Run(backend+"/"+phase, func(t *testing.T) {
				var s state.Store = state.NewMemStore()
				if backend == "postgres" {
					s, _ = pgStore(t)
				}
				ops := s.(state.RuntimeUpgradeReservationStore)
				app, serving, candidate, r := runtimeUpgradeOperationFixture(t, s, s.(state.RuntimeReleaseStore))
				if _, err := ops.RegisterRuntimeUpgradeOperation(t.Context(), r); err != nil {
					t.Fatal(err)
				}
				claim := claimRuntimeUpgradeOperation(t, ops)
				var wake string
				if phase != "claimed_prepared" {
					if _, err := ops.AdvanceRuntimeUpgradeOperation(t.Context(), claim); err != nil {
						t.Fatal(err)
					}
					wake = runtimeUpgradeOperationReady(t, s, s.(state.RuntimeReleaseStore), app, candidate, r)
				}
				cutoverRequest := state.RuntimeUpgradeCutoverRequest{AccountID: r.AccountID, AppID: r.AppID, DeploymentID: r.DeploymentID, ExpectedServingID: r.ServingDeploymentID, ExpectedTargetReleaseID: r.TargetReleaseID, ExpectedWakeID: wake, ExpectedQualificationReportSHA256: r.QualificationReportSHA256}
				if phase == "historical_cutover" {
					if _, err := s.(state.RuntimeUpgradeCutoverStore).CutoverDeploymentRuntimeUpgrade(t.Context(), cutoverRequest); err != nil {
						t.Fatal(err)
					}
				}
				result, err := ops.CancelRuntimeUpgradeOperation(t.Context(), r.AccountID, r.ID)
				if err != nil {
					t.Fatal(err)
				}
				if phase == "historical_cutover" {
					if result.Phase != state.RuntimeUpgradeComplete || result.WakeID != wake {
						t.Fatal("cancel erased historical activation", result)
					}
					assertRuntimeUpgradeTraffic(t, s, serving, candidate, true)
					if _, err := ops.CancelRuntimeUpgradeOperation(t.Context(), r.AccountID, r.ID); !errors.Is(err, state.ErrConflict) {
						t.Fatal("completed operation cancellable", err)
					}
				} else {
					if result.Phase != state.RuntimeUpgradeCancelled || result.LeaseToken != "" || !result.LeaseUntil.IsZero() {
						t.Fatal(result)
					}
					stable, err := s.DeploymentByID(t.Context(), serving.ID)
					retained, candidateErr := s.DeploymentByID(t.Context(), candidate.ID)
					if err != nil || candidateErr != nil || stable.Status != state.DeployLive || stable.TrafficPercent != 100 || retained.TrafficPercent != 0 {
						t.Fatal("cancel changed serving traffic", stable, retained, err, candidateErr)
					}
					if phase == "live_candidate" {
						if _, err := s.(state.RuntimeUpgradeCutoverStore).CutoverDeploymentRuntimeUpgrade(t.Context(), cutoverRequest); !errors.Is(err, state.ErrConflict) {
							t.Fatal("cancelled fresh acceptance activated", err)
						}
					} else {
						if _, err := ops.AdvanceRuntimeUpgradeOperation(t.Context(), claim); !errors.Is(err, state.ErrConflict) {
							t.Fatal("cancelled worker lease queued", err)
						}
						if _, err := s.BuildByDeployment(t.Context(), candidate.ID); !errors.Is(err, state.ErrNotFound) {
							t.Fatal("cancel before queue admitted build", err)
						}
					}
				}
				if _, err := ops.CancelRuntimeUpgradeOperation(t.Context(), uuid.NewString(), r.ID); !errors.Is(err, state.ErrNotFound) {
					t.Fatal("cross-account cancellation", err)
				}
			})
		}
	}
}

func TestPgRuntimeUpgradeCancellationCheckpointFailureRollsBackBuildAndCleanup(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	app, _, candidate, r := runtimeUpgradeOperationFixture(t, s, s)
	if _, err := s.RegisterRuntimeUpgradeOperation(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	claim := claimRuntimeUpgradeOperation(t, s)
	if _, err := s.AdvanceRuntimeUpgradeOperation(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimQueuedBuild(t.Context(), r.ID); err != nil {
		t.Fatal(err)
	}
	// Synthetic image metadata admits a queued release task; no VM is run.
	if err := s.SetDeploymentRootfs(t.Context(), candidate.ID, "/synthetic-release", "apps/"+app.Slug+"/release.ext4", 20); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE deployments SET status='imaging',image_digest='sha256:synthetic-release' WHERE id=$1`, candidate.ID); err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateAppTask(t.Context(), state.CreateAppTaskParams{AccountID: r.AccountID, AppID: r.AppID, DeploymentID: r.DeploymentID, Kind: state.AppTaskKindRelease, Command: []string{"synthetic-release-command"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `CREATE FUNCTION reject_cancel_checkpoint() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.phase='cancelled' THEN RAISE EXCEPTION 'synthetic cancellation checkpoint failure'; END IF; RETURN NEW; END $$;
CREATE TRIGGER reject_cancel_checkpoint BEFORE UPDATE ON runtime_upgrade_operations FOR EACH ROW EXECUTE FUNCTION reject_cancel_checkpoint();`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CancelRuntimeUpgradeOperation(t.Context(), r.AccountID, r.ID); err == nil {
		t.Fatal("checkpoint rejection ignored")
	}
	build, err := s.BuildByDeployment(t.Context(), candidate.ID)
	if err != nil || build.Status != state.BuildRunning || build.CancelledAt != nil {
		t.Fatal("build cancellation escaped rollback", build, err)
	}
	cleanup, err := s.ClaimBuildVMCleanup(t.Context(), 1)
	if err != nil || len(cleanup) != 0 {
		t.Fatal("cleanup escaped rollback", cleanup, err)
	}
	retainedTask, err := s.AppTaskByID(t.Context(), r.AccountID, r.AppID, task.ID)
	if err != nil || retainedTask.Status != state.AppTaskQueued {
		t.Fatal("release cancellation escaped rollback", retainedTask, err)
	}
	op, err := s.RuntimeUpgradeOperation(t.Context(), r.AccountID, r.ID)
	if err != nil || op.Phase != state.RuntimeUpgradeWaiting {
		t.Fatal(op, err)
	}
	if _, err := pool.Exec(t.Context(), `DROP TRIGGER reject_cancel_checkpoint ON runtime_upgrade_operations`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CancelRuntimeUpgradeOperation(t.Context(), r.AccountID, r.ID); err != nil {
		t.Fatal(err)
	}
	retainedTask, err = s.AppTaskByID(t.Context(), r.AccountID, r.AppID, task.ID)
	if err != nil || retainedTask.Status != state.AppTaskCancelled || retainedTask.FinishedAt == nil {
		t.Fatal("queued release command survived cancellation", retainedTask, err)
	}

}

func TestPgRuntimeUpgradeReservationDeadlineAndRawFences(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	_, _, _, r := runtimeUpgradeOperationFixture(t, s, s)
	r.DeploymentID = uuid.NewString()
	path := "/tmp/" + r.ID + ".tar.gz"
	if _, err := s.ReserveRuntimeUpgradeOperation(t.Context(), r, path); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE runtime_upgrade_operations SET source_path='/changed' WHERE id=$1`, r.ID); err == nil {
		t.Fatal("reserved source destination replaced")
	}
	if _, err := pool.Exec(t.Context(), `UPDATE runtime_upgrade_operations SET phase='waiting' WHERE id=$1`, r.ID); err == nil {
		t.Fatal("reservation jumped staging checkpoint")
	}
	if _, err := pool.Exec(t.Context(), `ALTER TABLE runtime_upgrade_operations DISABLE TRIGGER runtime_upgrade_operation_guard;
UPDATE runtime_upgrade_operations SET created_at=now()-interval '2 hours',deadline_at=now()-interval '1 hour';
ALTER TABLE runtime_upgrade_operations ENABLE TRIGGER runtime_upgrade_operation_guard;`); err != nil {
		t.Fatal(err)
	}
	claim := claimRuntimeUpgradeOperation(t, s)
	op, err := s.AdvanceRuntimeUpgradeOperation(t.Context(), claim)
	if err != nil || op.Phase != state.RuntimeUpgradeBlocked || op.Blocker != "deadline_exceeded" {
		t.Fatal("abandoned reservation did not expire", op, err)
	}
	if _, err := s.BuildByDeployment(t.Context(), r.DeploymentID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("expired reservation queued", err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE runtime_upgrade_operations SET phase='reserved',blocker='',finished_at=NULL WHERE id=$1`, r.ID); err == nil {
		t.Fatal("terminal reservation reopened")
	}
}
