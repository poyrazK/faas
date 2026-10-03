package pgintegration_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func preparedQualificationFixture(t *testing.T, basic gitOpsTestStore) (state.EnvironmentGitOpsLease, environmentsync.Plan, []state.EnvironmentWorkloadQualificationRequest) {
	t.Helper()
	lease, plan, _ := workloadGraphFixture(t, basic)
	candidates, err := basic.(state.EnvironmentGitOpsPreparationStore).PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range candidates {
		if err := basic.SetDeploymentRootfs(t.Context(), candidate.DeploymentID, "/reviewed.ext4", "reviewed-"+candidate.Resource, 4096); err != nil {
			t.Fatal(err)
		}
		if err := basic.UpdateDeploymentStatus(t.Context(), candidate.DeploymentID, state.DeploySnapshotting, ""); err != nil {
			t.Fatal(err)
		}
	}
	if graph, err := basic.(state.EnvironmentGitOpsGraphPreparationStore).ReconcileEnvironmentGitOpsPreparation(t.Context(), lease, plan); err != nil || graph.Phase != "prepared" {
		t.Fatalf("prepared graph: %s %v", graph.Phase, err)
	}
	requests, err := basic.(state.EnvironmentGitOpsQualificationStore).QueueEnvironmentGitOpsQualification(t.Context(), lease, plan)
	if err != nil || len(requests) != 2 {
		t.Fatalf("qualification cohort: %d %v", len(requests), err)
	}
	return lease, plan, requests
}

func TestEnvironmentGitOpsQualificationRequiresPreparedCohort(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		lease, plan, _ := workloadGraphFixture(t, basic)
		if _, err := basic.(state.EnvironmentGitOpsPreparationStore).PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan); err != nil {
			t.Fatal(err)
		}
		if _, err := basic.(state.EnvironmentGitOpsQualificationStore).QueueEnvironmentGitOpsQualification(t.Context(), lease, plan); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("unprepared graph published qualification authority: %v", err)
		}
	})
}

func TestEnvironmentGitOpsQualificationLeasesBindExactGraphAndArtifact(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		lease, plan, requests := preparedQualificationFixture(t, basic)
		qualifier, store := basic.(state.EnvironmentGitOpsQualificationStore), basic.(state.Store)
		if requests[0].Resource != "workload/api" || requests[0].ExecutionMode != api.ExecutionModeRequest || requests[1].Resource != "workload/worker" || requests[1].ExecutionMode != api.ExecutionModeWorker {
			t.Fatal("qualification changed the reviewed workload modes")
		}
		again, err := qualifier.QueueEnvironmentGitOpsQualification(t.Context(), lease, plan)
		if err != nil || !reflect.DeepEqual(again, requests) {
			t.Fatalf("lost publication response changed request identities: %v", err)
		}
		requests[0].FrozenInputs.Runtime["port"] = json.RawMessage(`1`)
		again, err = qualifier.QueueEnvironmentGitOpsQualification(t.Context(), lease, plan)
		if err != nil || string(again[0].FrozenInputs.Runtime["port"]) != "8080" {
			t.Fatalf("request aliased frozen inputs: %v", err)
		}
		claimed, err := qualifier.ClaimEnvironmentWorkloadQualification(t.Context(), again[0].ID, "owning-scheduler", time.Minute)
		if err != nil || claimed.Phase != "claimed" || claimed.Attempt != 1 || claimed.LeaseToken == "" || claimed.ReservedInstanceID == "" {
			t.Fatalf("claim reviewed qualification: %v", err)
		}
		if _, err := qualifier.ClaimEnvironmentWorkloadQualification(t.Context(), claimed.ID, "peer-scheduler", time.Minute); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("parallel claimant stole execution lease: %v", err)
		}
		if err := qualifier.ValidateEnvironmentWorkloadQualification(t.Context(), claimed); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"artifact", "scope", "resource", "phase", "token", "attempt", "instance"} {
			forged := claimed
			switch field {
			case "artifact":
				forged.Artifact.RootfsKey = "unreviewed.ext4"
			case "scope":
				forged.FrozenInputs.Scope = "staging"
			case "resource":
				forged.Resource = "workload/worker"
			case "phase":
				forged.Phase = "qualified"
			case "token":
				forged.LeaseToken = "not-issued"
			case "attempt":
				forged.Attempt++
			case "instance":
				forged.ReservedInstanceID = claimed.DeploymentID
			}
			if err := qualifier.ValidateEnvironmentWorkloadQualification(t.Context(), forged); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("substituted %s retained qualification authority: %v", field, err)
			}
		}
		renewed, err := qualifier.RenewEnvironmentWorkloadQualification(t.Context(), claimed, 2*time.Minute)
		if err != nil || renewed.Attempt != claimed.Attempt || renewed.LeaseToken != claimed.LeaseToken || renewed.ReservedInstanceID != claimed.ReservedInstanceID || !renewed.LeaseUntil.After(*claimed.LeaseUntil) {
			t.Fatalf("renewal changed execution identity: %v", err)
		}
		wire, _ := json.Marshal(renewed)
		if strings.Contains(string(wire), renewed.LeaseToken) || strings.Contains(string(wire), "frozen_inputs") {
			t.Fatal("execution token or frozen inputs entered public JSON")
		}
		if _, err := store.CreateInstance(t.Context(), renewed.AppID, renewed.DeploymentID, string(state.StateColdBooting), 512, "", ""); err == nil {
			t.Fatal("ordinary instance creation borrowed a qualification lease")
		}
		if err := store.MarkDeploymentLive(t.Context(), renewed.DeploymentID); err == nil {
			t.Fatal("a claimed request became serving proof")
		}
		planJSON, _ := json.Marshal(plan)
		if err := basic.FinishEnvironmentGitOps(t.Context(), lease, "converged", planJSON, json.RawMessage(`[]`), "", time.Now(), time.Now()); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("claimed qualification advanced applied revision: %v", err)
		}
		if err := basic.FinishEnvironmentGitOps(t.Context(), lease, "partial", planJSON, json.RawMessage(`[]`), "", time.Now(), time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := qualifier.ValidateEnvironmentWorkloadQualification(t.Context(), renewed); err != nil {
			t.Fatalf("controller lease release discarded approved qualification work: %v", err)
		}
		if _, err := qualifier.ClaimEnvironmentWorkloadQualification(t.Context(), again[1].ID, "worker-scheduler", time.Minute); err != nil {
			t.Fatalf("durable qualification depended on the controller's active lease: %v", err)
		}
		source, err := basic.EnvironmentGitSource(t.Context(), lease.Source.AccountID, lease.Source.ProjectID, "production")
		if err != nil || source.AppliedRevisionID != "" {
			t.Fatalf("execution admission claimed serving convergence: %v", err)
		}
		var definition api.EnvironmentDefinition
		_ = json.Unmarshal(lease.Revision.Definition, &definition)
		desired, err := environmentsync.Compile(definition)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := basic.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("f", 40))); err != nil {
			t.Fatal(err)
		}
		if err := qualifier.ValidateEnvironmentWorkloadQualification(t.Context(), renewed); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("superseded qualification retained authority: %v", err)
		}
	})
}

func TestEnvironmentGitOpsQualificationExpiredAttemptCannotResume(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		_, _, requests := preparedQualificationFixture(t, basic)
		qualifier := basic.(state.EnvironmentGitOpsQualificationStore)
		old, err := qualifier.ClaimEnvironmentWorkloadQualification(t.Context(), requests[0].ID, "lost-scheduler", 100*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Until(*old.LeaseUntil) + 20*time.Millisecond)
		if err := qualifier.ValidateEnvironmentWorkloadQualification(t.Context(), old); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("expired qualification remained usable: %v", err)
		}
		current, err := qualifier.ClaimEnvironmentWorkloadQualification(t.Context(), old.ID, "recovery-scheduler", time.Minute)
		if err != nil || current.ID != old.ID || current.Attempt != old.Attempt+1 || current.LeaseToken == old.LeaseToken || current.Artifact != old.Artifact || current.ReservedInstanceID == "" || current.ReservedInstanceID == old.ReservedInstanceID {
			t.Fatalf("qualification recovery changed work or reused authority: %v", err)
		}
		if _, err := qualifier.RenewEnvironmentWorkloadQualification(t.Context(), old, time.Minute); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("old attempt renewed a replacement's lease: %v", err)
		}
	})
}

func TestEnvironmentGitOpsQualificationCohortChangeRevokesAllMembers(t *testing.T) {
	for _, change := range []string{"artifact", "failure", "inherited_manifest"} {
		t.Run(change, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				lease, plan, requests := preparedQualificationFixture(t, basic)
				qualifier := basic.(state.EnvironmentGitOpsQualificationStore)
				worker, err := qualifier.ClaimEnvironmentWorkloadQualification(t.Context(), requests[1].ID, "worker-scheduler", time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				switch change {
				case "artifact":
					if err := basic.SetDeploymentRootfs(t.Context(), requests[0].DeploymentID, "/substituted.ext4", "unreviewed", 4096); err != nil {
						t.Fatal(err)
					}
				case "failure":
					if err := basic.UpdateDeploymentStatus(t.Context(), requests[0].DeploymentID, state.DeployFailed, "failed artifact"); err != nil {
						t.Fatal(err)
					}
				case "inherited_manifest":
					command := "./changed-after-qualification"
					if _, err := basic.(state.Store).UpdateApp(t.Context(), requests[0].AppID, state.UpdateAppParams{StartCommand: &command}); err != nil {
						t.Fatal(err)
					}
				}
				if err := qualifier.ValidateEnvironmentWorkloadQualification(t.Context(), worker); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("%s change in API retained worker authority: %v", change, err)
				}
				if _, err := qualifier.QueueEnvironmentGitOpsQualification(t.Context(), lease, plan); err == nil {
					t.Fatalf("%s change in API allowed stale cohort republication: %v", change, err)
				}
			})
		})
	}
}

func TestPgEnvironmentGitOpsQualificationHandoffRollbackAndJournalFences(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	lease, plan, requests := preparedQualificationFixture(t, store)
	// Reuse the current issued lease but force a complete cohort publication
	// rollback in a second fresh isolated database.
	pool2 := pgtest.OpenMigrated(t)
	other := state.NewPgStore(pool2)
	otherLease, otherPlan, _ := workloadGraphFixture(t, other)
	candidates, err := other.PrepareEnvironmentGitOpsImageCandidates(t.Context(), otherLease, otherPlan)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range candidates {
		if err := other.SetDeploymentRootfs(t.Context(), candidate.DeploymentID, "/reviewed.ext4", "reviewed", 4096); err != nil {
			t.Fatal(err)
		}
		if err := other.UpdateDeploymentStatus(t.Context(), candidate.DeploymentID, state.DeploySnapshotting, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := other.ReconcileEnvironmentGitOpsPreparation(t.Context(), otherLease, otherPlan); err != nil {
		t.Fatal(err)
	}
	if _, err := pool2.Exec(t.Context(), `create function reject_qualification_handoff() returns trigger language plpgsql as $$ begin
 if NEW.channel='environment_workload_qualify' then raise exception 'injected qualification handoff failure'; end if; return NEW; end $$;
 create trigger reject_qualification_handoff before insert on notification_outbox for each row execute function reject_qualification_handoff()`); err != nil {
		t.Fatal(err)
	}
	if _, err := other.QueueEnvironmentGitOpsQualification(t.Context(), otherLease, otherPlan); err == nil || !strings.Contains(err.Error(), "injected qualification") {
		t.Fatalf("qualification handoff failure was hidden: %v", err)
	}
	var count int
	if err := pool2.QueryRow(t.Context(), `select count(*) from environment_workload_qualification_requests`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed handoff published qualification authority: %d %v", count, err)
	}
	if _, err := pool2.Exec(t.Context(), `drop trigger reject_qualification_handoff on notification_outbox`); err != nil {
		t.Fatal(err)
	}
	if retry, err := other.QueueEnvironmentGitOpsQualification(t.Context(), otherLease, otherPlan); err != nil || len(retry) != 2 {
		t.Fatalf("publication recovery: %d %v", len(retry), err)
	}
	if _, err := store.QueueEnvironmentGitOpsQualification(t.Context(), lease, plan); err != nil {
		t.Fatal(err)
	}
	for _, request := range requests {
		if err := pool.QueryRow(t.Context(), `select count(*) from notification_outbox where channel='environment_workload_qualify' and payload::jsonb->>'qualification_id'=$1`, request.ID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("qualification handoff lost or duplicated: %d %v", count, err)
		}
	}
	for _, query := range []string{
		`update environment_workload_qualification_requests set artifact='{}'::jsonb where id=$1`,
		`update environment_workload_qualification_requests set frozen_inputs='{}'::jsonb where id=$1`,
		`update environment_workload_qualification_requests set phase='qualified' where id=$1`,
		`update environment_workload_qualification_requests set lease_token='forged',worker_id='forged',lease_until=now()+interval '1 minute',phase='claimed',attempt=1 where id=$1`,
		`delete from environment_workload_qualification_requests where id=$1`,
	} {
		if _, err := pool.Exec(t.Context(), query, requests[0].ID); err == nil {
			t.Fatalf("SQL escaped qualification contract: %s", query)
		}
	}
	claimed, err := store.ClaimEnvironmentWorkloadQualification(t.Context(), requests[0].ID, "scheduler", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `delete from goose_db_version where version_id=20261002095747000`); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	if err := store.ValidateEnvironmentWorkloadQualification(t.Context(), claimed); err != nil {
		t.Fatalf("migration replay discarded active execution authority: %v", err)
	}
}

func TestPgEnvironmentGitOpsQualificationOriginalParentPurges(t *testing.T) {
	for _, parent := range []string{"project", "account"} {
		t.Run(parent, func(t *testing.T) {
			pool := pgtest.OpenMigrated(t)
			store := state.NewPgStore(pool)
			lease, _, requests := preparedQualificationFixture(t, store)
			claimed, err := store.ClaimEnvironmentWorkloadQualification(t.Context(), requests[0].ID, "scheduler", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.CreateEnvironmentWorkloadQualificationInstance(t.Context(), claimed, qualificationPlacement(t, store, 4096)); err != nil {
				t.Fatal(err)
			}
			if parent == "project" {
				if err := store.DeleteProject(t.Context(), lease.Source.ProjectID); err != nil {
					t.Fatal(err)
				}
				// Project deletion detaches apps. Their lifecycle owner can
				// retire the original frame after execution authority is purged.
				if err := store.DeleteInstance(t.Context(), claimed.ReservedInstanceID); err == nil {
					t.Fatal("parent purge released unretired reservation")
				}
				retireQualificationWithoutDispatch(t, store, claimed.ReservedInstanceID)
				if err := store.DeleteInstance(t.Context(), claimed.ReservedInstanceID); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := store.MarkAccountDeletionPending(t.Context(), lease.Source.AccountID); err != nil {
					t.Fatal(err)
				}
				if err := store.DeleteAccount(t.Context(), lease.Source.AccountID); err == nil {
					t.Fatal("account purge released unretired reservation")
				}
				retireQualificationWithoutDispatch(t, store, claimed.ReservedInstanceID)
				if err := store.DeleteAccount(t.Context(), lease.Source.AccountID); err != nil {
					t.Fatal(err)
				}
			}
			var count int
			if err := pool.QueryRow(t.Context(), `select count(*) from environment_workload_qualification_requests where graph_id=$1`, requests[0].GraphID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("original parent purge retained qualification work: %d %v", count, err)
			}
			if _, err := store.ClaimEnvironmentWorkloadQualification(t.Context(), requests[0].ID, "scheduler", time.Minute); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("purged work still granted authority: %v", err)
			}
			if _, err := store.InstanceByID(t.Context(), claimed.ReservedInstanceID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("original parent purge retained its reservation: %v", err)
			}
		})
	}
}

func TestPgEnvironmentGitOpsQualificationSQLLeaseAndCohortFences(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	_, _, requests := preparedQualificationFixture(t, store)
	claimed, err := store.ClaimEnvironmentWorkloadQualification(t.Context(), requests[0].ID, "scheduler", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, assignment := range []string{
		`reserved_instance_id=gen_random_uuid()`,
		`worker_id='different-scheduler'`,
		`attempt=attempt+1`,
		`lease_until=clock_timestamp()+interval '16 minutes'`,
		`lease_until=clock_timestamp()+interval '1 second'`,
	} {
		tx, err := pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(t.Context(), `select set_config('gregale.gitops_qualification',$1,true)`, claimed.LeaseToken); err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(t.Context(), `update environment_workload_qualification_requests set `+assignment+` where id=$1`, claimed.ID)
		_ = tx.Rollback(t.Context())
		if err == nil || !strings.Contains(err.Error(), "execution lease is stale") {
			t.Fatalf("issued token allowed replacement of %s: %v", assignment, err)
		}
	}
	command := "./changed-after-review"
	if _, err := store.UpdateApp(t.Context(), requests[0].AppID, state.UpdateAppParams{StartCommand: &command}); err != nil {
		t.Fatal(err)
	}
	// The worker's own artifact and baseline remain unchanged. Raw SQL must
	// still reject its claim after the API member's inherited inputs change.
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), `select set_config('gregale.gitops_qualification','fresh-worker-token',true)`); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(t.Context(), `update environment_workload_qualification_requests set phase='claimed',worker_id='worker-scheduler',
 lease_token='fresh-worker-token',lease_until=clock_timestamp()+interval '1 minute',attempt=attempt+1,reserved_instance_id=gen_random_uuid() where id=$1`, requests[1].ID)
	if err == nil || !strings.Contains(err.Error(), "complete unchanged artifact cohort") {
		t.Fatalf("sibling baseline substitution escaped the SQL cohort fence: %v", err)
	}
}

func TestPgEnvironmentGitOpsQualificationRecoveryWaitsForPriorInstanceRetirement(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	_, _, requests := preparedQualificationFixture(t, store)
	placement := qualificationPlacement(t, store, 4096)
	var prior state.EnvironmentWorkloadQualificationRequest
	for _, phase := range []state.State{state.State("pending"), state.StateWaking, state.StateColdBooting, state.StateRunning, state.StateDraining, state.StateWarm, state.StateSnapshotting, state.StateMigrating, state.StateEvictingAccountDeleting} {
		// Leave enough time for the real fenced admission/phase transactions
		// under load before deliberately waiting for the short lease to expire.
		current, err := store.ClaimEnvironmentWorkloadQualification(t.Context(), requests[0].ID, "scheduler", time.Second)
		if err != nil || (prior.ID != "" && (current.Attempt != prior.Attempt+1 || current.ReservedInstanceID == prior.ReservedInstanceID)) {
			t.Fatalf("fresh attempt after retirement: %+v %v", current, err)
		}
		admitted, err := store.CreateEnvironmentWorkloadQualificationInstance(t.Context(), current, placement)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkEnvironmentQualificationDispatched(t.Context(), current, admitted.Execution); err != nil {
			t.Fatal(err)
		}
		// Model the dedicated executor's lifecycle under its still-current
		// capability, then lose the scheduler while that lifecycle is active.
		tx, err := pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(t.Context(), `select set_config('gregale.gitops_qualification',$1,true)`, current.LeaseToken); err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(t.Context(), `update instances set state=$2 where id=$1`, current.ReservedInstanceID, string(phase))
		charged := phase == state.StateWaking || phase == state.StateColdBooting || phase == state.StateRunning || phase == state.StateDraining || phase == state.StateWarm
		if charged {
			if err != nil {
				_ = tx.Rollback(t.Context())
				t.Fatal(err)
			}
			if err := tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
		} else {
			_ = tx.Rollback(t.Context())
			if err == nil || !strings.Contains(err.Error(), "physical retirement") {
				t.Fatalf("%s removed an unretired capacity holding: %v", phase, err)
			}
		}
		time.Sleep(max(0, time.Until(*current.LeaseUntil)+20*time.Millisecond))
		if _, err := store.ClaimEnvironmentWorkloadQualification(t.Context(), current.ID, "recovery-scheduler", time.Minute); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("expired %s instance allowed a replacement attempt: %v", phase, err)
		}
		if err := store.UpdateInstanceState(t.Context(), current.ReservedInstanceID, string(state.StateStopped)); err == nil {
			t.Fatal("generic terminal update acknowledged native retirement")
		}
		if err := store.RetireEnvironmentQualificationExecution(t.Context(), admitted.Execution, qualificationNativeProof()); err != nil {
			t.Fatal(err)
		}
		prior = current
	}
	recovered, err := store.ClaimEnvironmentWorkloadQualification(t.Context(), prior.ID, "recovery-scheduler", time.Minute)
	if err != nil || recovered.Attempt != prior.Attempt+1 || recovered.ReservedInstanceID == prior.ReservedInstanceID {
		t.Fatalf("retired instance did not release fresh recovery authority: %v", err)
	}
}
