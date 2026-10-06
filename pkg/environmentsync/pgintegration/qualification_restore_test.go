package pgintegration_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// These exercise storage authority and accounting, not native restore proof.
func qualificationRestoreFixture(t *testing.T, basic gitOpsTestStore, durations ...time.Duration) (state.EnvironmentWorkloadQualificationRequest, state.EnvironmentQualificationSnapshotReceipt) {
	t.Helper()
	claimed, _, runtime := qualificationRuntimeFixture(t, basic, durations...)
	if _, err := basic.(state.EnvironmentGitOpsQualificationRuntimeStore).PublishEnvironmentWorkloadQualificationRuntime(t.Context(), claimed, runtime); err != nil {
		t.Fatal(err)
	}
	executor := basic.(state.EnvironmentQualificationExecutionStore)
	status, err := executor.EnvironmentQualificationExecution(t.Context(), claimed.ReservedInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	proof := captureProof(status.Execution)
	capture, err := basic.(state.EnvironmentQualificationSnapshotStore).RecordEnvironmentQualificationSnapshot(t.Context(), claimed, status.Execution, proof)
	if err != nil {
		t.Fatal(err)
	}
	return claimed, capture
}

func TestPgEnvironmentQualificationRestoreRawGuardsAndMigrationReplay(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	basic := state.NewPgStore(pool)
	claimed, capture := qualificationRestoreFixture(t, basic)
	retireCapturedQualification(t, basic, capture)
	admission, err := basic.CreateEnvironmentQualificationRestore(t.Context(), claimed, qualificationPlacement(t, basic, 4096))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`update environment_qualification_restore_reservations set capture_instance_id=gen_random_uuid() where instance_id=$1`,
		`delete from environment_qualification_restore_reservations where instance_id=$1`,
		`update environment_qualification_executions set capture_instance_id=null where instance_id=$1`,
		`update environment_qualification_executions set dispatch_started=true where instance_id=$1`,
		`update instances set node_id=gen_random_uuid() where id=$1`,
		`update instances set state='stopped',terminal_at=clock_timestamp() where id=$1`,
		`delete from instances where id=$1`,
	} {
		if _, err := pool.Exec(t.Context(), statement, admission.Instance.ID); err == nil {
			t.Fatal("raw write bypassed restore fence", statement)
		}
	}
	before, err := basic.EnvironmentQualificationExecution(t.Context(), admission.Instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `delete from goose_db_version where version_id=$1`, int64(20261006142500001)); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal("restore migration replay", err)
	}
	after, err := basic.EnvironmentQualificationExecution(t.Context(), admission.Instance.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("migration changed original restore authority", err)
	}
	if err := basic.MarkEnvironmentQualificationRestoreDispatched(t.Context(), claimed, admission.Execution); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `select set_config('gregale.gitops_qualification_cleanup',$1,true)`, admission.Execution.CleanupToken); err != nil {
		_ = tx.Rollback(t.Context())
		t.Fatal(err)
	}
	proof := qualificationNativeProof()
	proof.NativeGeneration = capture.Snapshot.NativeGeneration
	data, _ := json.Marshal(proof)
	_, err = tx.Exec(t.Context(), `update environment_qualification_executions set retirement=$2,retired_at=clock_timestamp() where instance_id=$1`, admission.Instance.ID, data)
	_ = tx.Rollback(t.Context())
	if err == nil {
		t.Fatal("raw target cleanup borrowed source physical generation")
	}
}

func retireCapturedQualification(t *testing.T, basic gitOpsTestStore, capture state.EnvironmentQualificationSnapshotReceipt) {
	t.Helper()
	proof := qualificationNativeProof()
	proof.NativeGeneration, proof.KernelBootID = capture.Snapshot.NativeGeneration, capture.Snapshot.KernelBootID
	if err := basic.(state.EnvironmentQualificationExecutionStore).RetireEnvironmentQualificationExecution(t.Context(), capture.Execution, proof); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentQualificationRestoreReservationAndDispatch(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		claimed, capture := qualificationRestoreFixture(t, basic)
		restorer, executor := basic.(state.EnvironmentQualificationRestoreStore), basic.(state.EnvironmentQualificationExecutionStore)
		placement := qualificationPlacement(t, basic, 512+api.PerVMOverheadMB)
		if _, err := restorer.CreateEnvironmentQualificationRestore(t.Context(), claimed, placement); !errors.Is(err, state.ErrConflict) {
			t.Fatal("live capture producer admitted a restore", err)
		}
		retireCapturedQualification(t, basic, capture)
		tooSmall := qualificationPlacement(t, basic, 256+api.PerVMOverheadMB)
		if _, err := restorer.CreateEnvironmentQualificationRestore(t.Context(), claimed, tooSmall); !errors.Is(err, state.ErrNodeCapacity) {
			t.Fatal("restore bypassed node admission", err)
		}
		aliased := placement
		aliased.WakeID = capture.Execution.WakeID
		if _, err := restorer.CreateEnvironmentQualificationRestore(t.Context(), claimed, aliased); !errors.Is(err, state.ErrConflict) {
			t.Fatal("restore reused producer wake", err)
		}
		var group sync.WaitGroup
		results := make([]state.EnvironmentQualificationRestoreAdmission, 2)
		errs := make([]error, 2)
		for i := range results {
			group.Go(func() {
				results[i], errs[i] = restorer.CreateEnvironmentQualificationRestore(t.Context(), claimed, placement)
			})
		}
		group.Wait()
		first := results[0]
		if errs[0] != nil || errs[1] != nil || results[0].Execution != results[1].Execution || results[0].Created == results[1].Created {
			t.Fatalf("lost response/concurrent admission duplicated target: %v %v", errs, results)
		}
		if first.Execution.CaptureInstanceID != capture.Execution.InstanceID || first.Instance.ID == capture.Execution.InstanceID || first.Execution.CleanupToken == capture.Execution.CleanupToken || first.Instance.WakeID == capture.Execution.WakeID ||
			first.Instance.State != string(state.StateColdBooting) || first.Instance.FrameworkReadyAt != nil || !reflect.DeepEqual(first.Capture, capture) {
			t.Fatal("restore borrowed capture identity or readiness", first)
		}
		status, err := executor.EnvironmentQualificationExecution(t.Context(), first.Instance.ID)
		if err != nil || status.CaptureInstanceID != capture.Execution.InstanceID || status.DispatchStarted || status.RetiredAt != nil {
			t.Fatal("restore lost original cohort", status, err)
		}
		for _, field := range []string{"token", "attempt", "capture", "wake", "node", "ram"} {
			forged, moved := claimed, placement
			switch field {
			case "token":
				forged.LeaseToken = uuid.NewString()
			case "attempt":
				forged.Attempt++
			case "capture":
				forged.ReservedInstanceID = uuid.NewString()
			case "wake":
				moved.WakeID = uuid.NewString()
			case "node":
				moved.NodeID = uuid.NewString()
			case "ram":
				moved.RAMMB++
			}
			if _, err := restorer.CreateEnvironmentQualificationRestore(t.Context(), forged, moved); !errors.Is(err, state.ErrConflict) {
				t.Fatal("restore substituted", field, err)
			}
		}
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := restorer.CreateEnvironmentQualificationRestore(cancelled, claimed, placement); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if err := executor.MarkEnvironmentQualificationDispatched(t.Context(), claimed, first.Execution); !errors.Is(err, state.ErrConflict) {
			t.Fatal("cold producer dispatch accepted a restore", err)
		}
		if _, err := basic.(state.EnvironmentQualificationRecoveryStore).EnvironmentQualificationExecutionForRecovery(t.Context(), placement.NodeID, first.Instance.ID); !errors.Is(err, state.ErrConflict) {
			t.Fatal("recovery stole restore lease", err)
		}
		if rows, err := basic.(state.EnvironmentQualificationRecoveryStore).ListEnvironmentQualificationExecutionsForRecovery(t.Context(), placement.NodeID, "", 10); err != nil || len(rows) != 0 {
			t.Fatal("active restore listed for cleanup", len(rows), err)
		}
		for _, mutate := range []func() error{
			func() error {
				return basic.UpdateInstanceState(t.Context(), first.Instance.ID, string(state.StateStopped))
			},
			func() error {
				return basic.UpdateInstanceState(t.Context(), first.Instance.ID, string(state.StateRunning))
			},
			func() error { return basic.SetInstanceFrameworkReadyAt(t.Context(), first.Instance.ID, time.Now()) },
			func() error { return basic.DeleteInstance(t.Context(), first.Instance.ID) },
			func() error { return basic.MarkDeploymentLive(t.Context(), claimed.DeploymentID) },
		} {
			if err := mutate(); err == nil {
				t.Fatal("generic writer borrowed restore authority")
			}
		}
		if _, err := basic.(state.EnvironmentQualificationSnapshotStore).RecordEnvironmentQualificationSnapshot(t.Context(), claimed, first.Execution, capture.Snapshot); !errors.Is(err, state.ErrConflict) {
			t.Fatal("restore replaced original capture", err)
		}
		if err := restorer.MarkEnvironmentQualificationRestoreDispatched(cancelled, claimed, first.Execution); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if err := restorer.MarkEnvironmentQualificationRestoreDispatched(t.Context(), claimed, first.Execution); err != nil {
			t.Fatal(err)
		}
		if err := restorer.MarkEnvironmentQualificationRestoreDispatched(t.Context(), claimed, first.Execution); !errors.Is(err, state.ErrConflict) {
			t.Fatal("lost dispatch response authorized replay", err)
		}
		if err := executor.RetireEnvironmentQualificationExecution(t.Context(), first.Execution, state.EnvironmentQualificationRetirement{Kind: state.QualificationNeverDispatched}); !errors.Is(err, state.ErrConflict) {
			t.Fatal("dispatched restore released without physical proof", err)
		}
		borrowed := qualificationNativeProof()
		borrowed.NativeGeneration = capture.Snapshot.NativeGeneration
		if err := executor.RetireEnvironmentQualificationExecution(t.Context(), first.Execution, borrowed); !errors.Is(err, state.ErrConflict) {
			t.Fatal("restore borrowed producer retirement", err)
		}
		proof := qualificationNativeProof()
		if err := executor.RetireEnvironmentQualificationExecution(t.Context(), first.Execution, proof); err != nil {
			t.Fatal(err)
		}
		if err := executor.RetireEnvironmentQualificationExecution(t.Context(), first.Execution, proof); err != nil {
			t.Fatal("cleanup response not idempotent", err)
		}
		if err := basic.DeleteInstance(t.Context(), first.Instance.ID); err == nil {
			t.Fatal("current restore row deleted before attempt release")
		}
		if _, err := restorer.CreateEnvironmentQualificationRestore(t.Context(), claimed, placement); !errors.Is(err, state.ErrConflict) {
			t.Fatal("retired restore recreated within attempt", err)
		}
		retained, err := basic.(state.EnvironmentQualificationSnapshotStore).EnvironmentQualificationSnapshotReceipt(t.Context(), capture.Execution.InstanceID)
		if err != nil || !reflect.DeepEqual(retained, capture) {
			t.Fatal("target changed original evidence", err)
		}
	})
}

func TestEnvironmentQualificationRestoreRejectsStaleCapturedInputs(t *testing.T) {
	for _, admitted := range []bool{false, true} {
		t.Run(map[bool]string{false: "admission", true: "dispatch"}[admitted], func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				claimed, capture := qualificationRestoreFixture(t, basic)
				retireCapturedQualification(t, basic, capture)
				placement := qualificationPlacement(t, basic, 4096)
				restorer := basic.(state.EnvironmentQualificationRestoreStore)
				var target state.EnvironmentQualificationRestoreAdmission
				if admitted {
					var err error
					target, err = restorer.CreateEnvironmentQualificationRestore(t.Context(), claimed, placement)
					if err != nil {
						t.Fatal(err)
					}
				}
				app, err := basic.AppByID(t.Context(), claimed.AppID)
				if err != nil {
					t.Fatal(err)
				}
				if err := basic.UpsertAppEnvInScope(t.Context(), app.AccountID, app.ID, capture.Inputs.Scope, "RESTORE_CHANGED", "new"); err != nil {
					t.Fatal(err)
				}
				if _, err := restorer.CreateEnvironmentQualificationRestore(t.Context(), claimed, placement); !errors.Is(err, state.ErrConflict) {
					t.Fatal("stale captured inputs admitted", err)
				}
				if admitted {
					if err := restorer.MarkEnvironmentQualificationRestoreDispatched(t.Context(), claimed, target.Execution); !errors.Is(err, state.ErrConflict) {
						t.Fatal("stale captured inputs dispatched", err)
					}
					retireQualificationWithoutDispatch(t, basic, target.Instance.ID)
				}
			})
		})
	}
}

func TestEnvironmentQualificationRestoreHeldAcrossExpiryAndPurge(t *testing.T) {
	for _, purge := range []bool{false, true} {
		t.Run(map[bool]string{false: "expiry", true: "purge"}[purge], func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				claimed, capture := qualificationRestoreFixture(t, basic, 2*time.Second)
				retireCapturedQualification(t, basic, capture)
				placement := qualificationPlacement(t, basic, 4096)
				restorer := basic.(state.EnvironmentQualificationRestoreStore)
				admission, err := restorer.CreateEnvironmentQualificationRestore(t.Context(), claimed, placement)
				if err != nil {
					t.Fatal(err)
				}
				if purge {
					app, err := basic.AppByID(t.Context(), claimed.AppID)
					if err != nil {
						t.Fatal(err)
					}
					if err := basic.DeleteProject(t.Context(), app.ProjectID); err != nil {
						t.Fatal(err)
					}
				} else {
					time.Sleep(max(0, time.Until(*claimed.LeaseUntil)+20*time.Millisecond))
					if _, err := basic.(state.EnvironmentGitOpsQualificationStore).ClaimEnvironmentWorkloadQualification(t.Context(), claimed.ID, "replacement", time.Minute); !errors.Is(err, state.ErrConflict) {
						t.Fatal("retired producer hid charged restore", err)
					}
				}
				if err := restorer.MarkEnvironmentQualificationRestoreDispatched(t.Context(), claimed, admission.Execution); err == nil {
					t.Fatal("obsolete attempt dispatched restore")
				}
				discovery := basic.(state.EnvironmentQualificationRecoveryStore)
				rows, err := discovery.ListEnvironmentQualificationExecutionsForRecovery(t.Context(), placement.NodeID, "", 1)
				if err != nil || len(rows) != 1 || rows[0].Execution != admission.Execution || rows[0].CaptureInstanceID != capture.Execution.InstanceID {
					t.Fatal("restore disappeared from recovery", len(rows), err)
				}
				if _, err := discovery.EnvironmentQualificationExecutionForRecovery(t.Context(), placement.NodeID, admission.Instance.ID); err != nil {
					t.Fatal(err)
				}
				retireQualificationWithoutDispatch(t, basic, admission.Instance.ID)
				if !purge {
					if err := basic.DeleteInstance(t.Context(), admission.Instance.ID); err != nil {
						t.Fatal(err)
					}
					next, err := basic.(state.EnvironmentGitOpsQualificationStore).ClaimEnvironmentWorkloadQualification(t.Context(), claimed.ID, "replacement", time.Minute)
					if err != nil || next.Attempt != claimed.Attempt+1 {
						t.Fatal("retirement failed to release attempt", err)
					}
					if _, err := restorer.CreateEnvironmentQualificationRestore(t.Context(), next, placement); !errors.Is(err, state.ErrConflict) {
						t.Fatal("new attempt adopted prior capture", err)
					}
				}
				retained, err := basic.(state.EnvironmentQualificationSnapshotStore).EnvironmentQualificationSnapshotReceipt(t.Context(), capture.Execution.InstanceID)
				if err != nil || !reflect.DeepEqual(retained, capture) {
					t.Fatal("purge or cleanup erased cohort", err)
				}
			})
		})
	}
}
