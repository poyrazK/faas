package pgintegration_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func qualificationPlacement(t *testing.T, store state.Store, ceiling int) state.EnvironmentWorkloadQualificationPlacement {
	t.Helper()
	node, err := store.CreateComputeNode(t.Context(), state.ComputeNode{Name: "qualification-" + uuid.NewString(), Active: true,
		TargetURL: "tcp://127.0.0.1:50051", AdmissionCeilingMB: ceiling, MemMB: 8192, VPCPUs: 4, VCPUBudget: 160, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	return state.EnvironmentWorkloadQualificationPlacement{NodeID: node.ID, WakeID: uuid.NewString(), RAMMB: 512}
}

func TestEnvironmentGitOpsQualificationInstanceAdmissionAndOrdinaryWriterFences(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		lease, plan, requests := preparedQualificationFixture(t, basic)
		qualifier, admission := basic.(state.EnvironmentGitOpsQualificationStore), basic.(state.EnvironmentGitOpsQualificationInstanceStore)
		placement := qualificationPlacement(t, basic, 4096)
		for _, request := range requests {
			claimed, err := qualifier.ClaimEnvironmentWorkloadQualification(t.Context(), request.ID, "scheduler", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			placement.WakeID = uuid.NewString()
			cancelled, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := admission.CreateEnvironmentWorkloadQualificationInstance(cancelled, claimed, placement); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled admission published a reservation: %v", err)
			}
			first, err := admission.CreateEnvironmentWorkloadQualificationInstance(t.Context(), claimed, placement)
			mode := string(state.InstanceModeNormal)
			if request.ExecutionMode == api.ExecutionModeWorker {
				mode = string(state.InstanceModeWorker)
			}
			if err != nil || !first.Created || first.Instance.ID != claimed.ReservedInstanceID || first.Instance.DeploymentID != claimed.DeploymentID ||
				first.Instance.State != string(state.StateColdBooting) || first.Instance.Mode != mode || first.Instance.Kind != "wake" || first.Instance.FrameworkReadyAt != nil {
				t.Fatalf("dedicated admission: %+v %v", first, err)
			}
			retry, err := admission.CreateEnvironmentWorkloadQualificationInstance(t.Context(), claimed, placement)
			if err != nil || retry.Created || retry.Instance.ID != first.Instance.ID || !retry.Instance.StartedAt.Equal(first.Instance.StartedAt) {
				t.Fatalf("lost response duplicated admission: %+v %v", retry, err)
			}
			for _, field := range []string{"token", "attempt", "instance", "node", "wake", "ram"} {
				forged, moved := claimed, placement
				switch field {
				case "token":
					forged.LeaseToken = "not-issued"
				case "attempt":
					forged.Attempt++
				case "instance":
					forged.ReservedInstanceID = uuid.NewString()
				case "node":
					moved.NodeID = uuid.NewString()
				case "wake":
					moved.WakeID = uuid.NewString()
				case "ram":
					moved.RAMMB++
				}
				if _, err := admission.CreateEnvironmentWorkloadQualificationInstance(t.Context(), forged, moved); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("substituted %s reused reserved instance: %v", field, err)
				}
			}
			for _, mutate := range []func() error{
				func() error { return basic.DeleteInstance(t.Context(), first.Instance.ID) },
				func() error {
					return basic.UpdateInstanceState(t.Context(), first.Instance.ID, string(state.StateRunning))
				},
				func() error {
					return basic.SetInstanceRuntime(t.Context(), first.Instance.ID, "unqualified", "10.0.0.1", 20001)
				},
				func() error { return basic.SetInstanceFrameworkReadyAt(t.Context(), first.Instance.ID, time.Now()) },
			} {
				if err := mutate(); err == nil {
					t.Fatal("ordinary writer borrowed qualification authority")
				}
			}
			inputs := state.RuntimeConfigInputs{Scope: "production", Boundary: time.Unix(0, 0), Variables: map[string]string{}, SecretVersions: map[string]int64{}}
			if _, err := basic.(state.RuntimeConfigReceiptPublisher).PublishInstanceRuntimeWithConfig(t.Context(), first.Instance.ID,
				string(state.StateColdBooting), "unqualified", "10.0.0.1", 20001, placement.WakeID, inputs); err == nil {
				t.Fatal("ordinary runtime publisher turned admission into readiness")
			}
			if _, exists, err := basic.(state.RuntimeConfigReceiptStore).InstanceRuntimeConfigReceipt(t.Context(), first.Instance.ID); err != nil || exists {
				t.Fatalf("rejected publication left runtime evidence: %v %v", exists, err)
			}
			current, err := basic.InstanceByID(t.Context(), first.Instance.ID)
			if err != nil || current.State != string(state.StateColdBooting) || current.Netns != "" || current.FrameworkReadyAt != nil {
				t.Fatalf("rejected mutation changed the reservation: %+v %v", current, err)
			}
			if err := basic.MarkDeploymentLive(t.Context(), claimed.DeploymentID); err == nil {
				t.Fatal("admitted instance became serving proof")
			}
			if err := basic.UpdateInstanceState(t.Context(), first.Instance.ID, string(state.StateStopped)); err == nil {
				t.Fatal("generic terminal state released qualification capacity")
			}
			retireQualificationWithoutDispatch(t, basic, first.Instance.ID)
			if err := basic.DeleteInstance(t.Context(), first.Instance.ID); err == nil {
				t.Fatal("deletion released a reserved identity before attempt expiry")
			}
			if _, err := admission.CreateEnvironmentWorkloadQualificationInstance(t.Context(), claimed, placement); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("retired reservation became a new boot: %v", err)
			}
		}
		raw, _ := json.Marshal(plan)
		if err := basic.FinishEnvironmentGitOps(t.Context(), lease, "converged", raw, json.RawMessage(`[]`), "", time.Now(), time.Now()); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("reservation advanced applied revision: %v", err)
		}
	})
}

func TestEnvironmentGitOpsQualificationInstancePreservesCapacity(t *testing.T) {
	for _, capacity := range []string{"node", "account_worker"} {
		t.Run(capacity, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				_, _, requests := preparedQualificationFixture(t, basic)
				qualifier, admission := basic.(state.EnvironmentGitOpsQualificationStore), basic.(state.EnvironmentGitOpsQualificationInstanceStore)
				ceiling := 4096
				if capacity == "node" {
					ceiling = 512 + api.PerVMOverheadMB
				}
				placement := qualificationPlacement(t, basic, ceiling)
				if capacity == "node" {
					apiAttempt, err := qualifier.ClaimEnvironmentWorkloadQualification(t.Context(), requests[0].ID, "api-scheduler", time.Minute)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := admission.CreateEnvironmentWorkloadQualificationInstance(t.Context(), apiAttempt, placement); err != nil {
						t.Fatal(err)
					}
				} else {
					// Fill the account quota on its retained serving deployment;
					// no extra app or intent change may invalidate the reviewed plan.
					serving, err := basic.LiveDeploymentForScope(t.Context(), requests[1].AppID, "production")
					if err != nil {
						t.Fatal(err)
					}
					limits, _ := api.LimitsFor(api.PlanPro)
					for i := 0; i < limits.WorkerReplicasMax; i++ {
						if _, err := basic.CreateInstanceWithMode(t.Context(), requests[1].AppID, serving.ID, string(state.StateColdBooting), 512, placement.NodeID, uuid.NewString(), string(state.InstanceModeWorker)); err != nil {
							t.Fatal(err)
						}
					}
				}
				worker, err := qualifier.ClaimEnvironmentWorkloadQualification(t.Context(), requests[1].ID, "worker-scheduler", time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				_, err = admission.CreateEnvironmentWorkloadQualificationInstance(t.Context(), worker, placement)
				want := state.ErrNodeCapacity
				if capacity == "account_worker" {
					want = state.ErrAccountWorkerCapacity
				}
				if !errors.Is(err, want) {
					t.Fatalf("qualification bypassed %s capacity: %v", capacity, err)
				}
				if _, err := basic.InstanceByID(t.Context(), worker.ReservedInstanceID); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("refusal published a held instance: %v", err)
				}
			})
		})
	}
}

func TestEnvironmentGitOpsQualificationInstanceSupersessionAndExpiry(t *testing.T) {
	for _, change := range []string{"expired", "sibling_artifact", "report", "account_deleting"} {
		t.Run(change, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				lease, _, requests := preparedQualificationFixture(t, basic)
				qualifier, admission := basic.(state.EnvironmentGitOpsQualificationStore), basic.(state.EnvironmentGitOpsQualificationInstanceStore)
				placement := qualificationPlacement(t, basic, 4096)
				duration := time.Minute
				if change == "expired" {
					duration = 250 * time.Millisecond
				}
				claimed, err := qualifier.ClaimEnvironmentWorkloadQualification(t.Context(), requests[1].ID, "scheduler", duration)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := admission.CreateEnvironmentWorkloadQualificationInstance(t.Context(), claimed, placement); err != nil {
					t.Fatal(err)
				}
				switch change {
				case "expired":
					time.Sleep(max(0, time.Until(*claimed.LeaseUntil)+20*time.Millisecond))
					if _, err := qualifier.ClaimEnvironmentWorkloadQualification(t.Context(), claimed.ID, "recovery", time.Minute); !errors.Is(err, state.ErrConflict) {
						t.Fatalf("parallel attempt replaced an active VM reservation: %v", err)
					}
				case "sibling_artifact":
					if err := basic.SetDeploymentRootfs(t.Context(), requests[0].DeploymentID, "/changed.ext4", "changed", 4096); err != nil {
						t.Fatal(err)
					}
				case "report":
					if _, err := basic.(state.EnvironmentGitOpsControlStore).UpdateEnvironmentGitSource(t.Context(), lease.Source.AccountID, lease.Source.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: lease.Source.Generation, Mode: "report"}); err != nil {
						t.Fatal(err)
					}
				case "account_deleting":
					if err := basic.MarkAccountDeletionPending(t.Context(), lease.Source.AccountID); err != nil {
						t.Fatal(err)
					}
				}
				if err := qualifier.ValidateEnvironmentWorkloadQualification(t.Context(), claimed); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("%s retained its execution lease: %v", change, err)
				}
				if _, err := admission.CreateEnvironmentWorkloadQualificationInstance(t.Context(), claimed, placement); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("%s reservation retry retained execution authority: %v", change, err)
				}
				retireQualificationWithoutDispatch(t, basic, claimed.ReservedInstanceID)
				if change == "expired" {
					recovered, err := qualifier.ClaimEnvironmentWorkloadQualification(t.Context(), claimed.ID, "recovery", time.Minute)
					if err != nil || recovered.ReservedInstanceID == claimed.ReservedInstanceID || recovered.Attempt != claimed.Attempt+1 {
						t.Fatalf("retirement did not permit fresh authority: %+v %v", recovered, err)
					}
					if _, err := admission.CreateEnvironmentWorkloadQualificationInstance(t.Context(), recovered, placement); err != nil {
						t.Fatal(err)
					}
					if err := basic.DeleteInstance(t.Context(), claimed.ReservedInstanceID); err != nil {
						t.Fatalf("retired prior attempt could not be collected: %v", err)
					}
				}
			})
		})
	}
}

func TestPgEnvironmentGitOpsQualificationInstanceSQLFencesAndReplay(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	_, _, requests := preparedQualificationFixture(t, store)
	placement := qualificationPlacement(t, store, 4096)
	claimed, err := store.ClaimEnvironmentWorkloadQualification(t.Context(), requests[0].ID, "scheduler", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := duplicate.Exec(t.Context(), `select set_config('gregale.gitops_qualification','duplicate-reservation',true)`); err != nil {
		t.Fatal(err)
	}
	_, err = duplicate.Exec(t.Context(), `update environment_workload_qualification_requests set phase='claimed',worker_id='worker-scheduler',
 lease_token='duplicate-reservation',lease_until=clock_timestamp()+interval '1 minute',attempt=attempt+1,reserved_instance_id=$2 where id=$1`, requests[1].ID, claimed.ReservedInstanceID)
	_ = duplicate.Rollback(t.Context())
	if err == nil || !strings.Contains(err.Error(), "environment_qualification_reserved_instance_unique_idx") {
		t.Fatalf("two requests reserved the same instance identity: %v", err)
	}
	serving, err := store.LiveDeploymentForScope(t.Context(), claimed.AppID, "production")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `insert into instances(id,app_id,deployment_id,state,ram_mb,node_id,wake_id)
 values($1,$2,$3,'cold_booting',512,$4,$5)`, claimed.ReservedInstanceID, claimed.AppID, serving.ID, placement.NodeID, placement.WakeID); err == nil || !strings.Contains(err.Error(), "cannot be reassigned") {
		t.Fatalf("ordinary deployment borrowed reserved identity: %v", err)
	}
	// Even the issued capability must use the exact initial admission shape.
	for _, substitution := range []string{"mode", "ram", "running", "ready", "node"} {
		tx, err := pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(t.Context(), `select set_config('gregale.gitops_qualification',$1,true)`, claimed.LeaseToken); err != nil {
			t.Fatal(err)
		}
		mode, phase, ram, node := "normal", "cold_booting", 512, placement.NodeID
		var ready *time.Time
		switch substitution {
		case "mode":
			mode = "mirror"
		case "ram":
			ram++
		case "running":
			phase = "running"
		case "ready":
			now := time.Now()
			ready = &now
		case "node":
			node = uuid.NewString()
		}
		_, err = tx.Exec(t.Context(), `insert into instances(id,app_id,deployment_id,state,ram_mb,node_id,wake_id,mode,kind,framework_ready_at)
 values($1,$2,$3,$4,$5,$6,$7,$8,'wake',$9)`, claimed.ReservedInstanceID, claimed.AppID, claimed.DeploymentID, phase, ram, node, placement.WakeID, mode, ready)
		_ = tx.Rollback(t.Context())
		if err == nil {
			t.Fatalf("issued capability accepted substituted %s", substitution)
		}
	}
	admitted, err := store.CreateEnvironmentWorkloadQualificationInstance(t.Context(), claimed, placement)
	if err != nil || !admitted.Created {
		t.Fatalf("admit: %+v %v", admitted, err)
	}
	for _, assignment := range []string{`id=gen_random_uuid()`, `deployment_id='` + serving.ID + `'`, `node_id=gen_random_uuid()`, `wake_id=gen_random_uuid()`, `ram_mb=ram_mb+1`, `mode='mirror'`} {
		_, err := pool.Exec(t.Context(), `update instances set `+assignment+` where id=$1`, admitted.Instance.ID)
		var guard *pgconn.PgError
		standardsFence := errors.As(err, &guard) && guard.Code == "23514" && guard.ConstraintName == "application_standard_runtime_stale"
		if err == nil || (!standardsFence && !strings.Contains(err.Error(), "identity is immutable") && !strings.Contains(err.Error(), "identity are immutable") && !strings.Contains(err.Error(), "placement survives parent removal")) {
			t.Fatalf("SQL reassigned active reservation: %s %v", assignment, err)
		}
	}
	if _, err := pool.Exec(t.Context(), `delete from goose_db_version where version_id=20261003214107892`); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	if retry, err := store.CreateEnvironmentWorkloadQualificationInstance(t.Context(), claimed, placement); err != nil || retry.Created || retry.Instance.ID != admitted.Instance.ID {
		t.Fatalf("populated replay changed reservation: %+v %v", retry, err)
	}
	// A sibling change also fences raw lifecycle publication with the exact
	// token; it must not rely only on the Go admission method's observation.
	if err := store.MarkEnvironmentQualificationDispatched(t.Context(), claimed, admitted.Execution); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(t.Context(), requests[1].DeploymentID, "/changed.ext4", "changed", 4096); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `select set_config('gregale.gitops_qualification',$1,true)`, claimed.LeaseToken); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(t.Context(), `update instances set state='running' where id=$1`, admitted.Instance.ID)
	_ = tx.Rollback(t.Context())
	if err == nil || !strings.Contains(err.Error(), "current qualification attempt") {
		t.Fatalf("changed sibling allowed raw readiness publication: %v", err)
	}
	if err := store.RetireEnvironmentQualificationExecution(t.Context(), admitted.Execution, qualificationNativeProof()); err != nil {
		t.Fatal(err)
	}
}
