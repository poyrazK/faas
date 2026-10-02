package pgintegration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func qualificationRuntimeFixture(t *testing.T, store gitOpsTestStore, durations ...time.Duration) (state.EnvironmentWorkloadQualificationRequest, state.EnvironmentWorkloadQualificationRequest, state.EnvironmentWorkloadQualificationRuntime) {
	t.Helper()
	_, _, requests := preparedQualificationFixture(t, store)
	placement := qualificationPlacement(t, store, 4096)
	duration := time.Minute
	if len(durations) > 0 {
		duration = durations[0]
	}
	claimed, err := store.(state.EnvironmentGitOpsQualificationStore).ClaimEnvironmentWorkloadQualification(t.Context(), requests[0].ID, "scheduler", duration)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.(state.EnvironmentGitOpsQualificationInstanceStore).CreateEnvironmentWorkloadQualificationInstance(t.Context(), claimed, placement); err != nil {
		t.Fatal(err)
	}
	return claimed, requests[1], state.EnvironmentWorkloadQualificationRuntime{NodeID: placement.NodeID, WakeID: placement.WakeID,
		Netns: "qualification-netns", HostIP: "10.0.0.1", GuestUID: 20001, Inputs: state.RuntimeConfigInputs{
			Scope: "production", Boundary: time.Unix(0, 0), Variables: map[string]string{}, SecretVersions: map[string]int64{}, SecretRefs: map[string]string{}, AllSecrets: true}}
}

func TestEnvironmentGitOpsQualificationRuntimeRechecksAdmissionAndCancelledAttempts(t *testing.T) {
	for _, change := range []string{"ram", "node", "account", "cancelled", "expired"} {
		t.Run(change, func(t *testing.T) {
			stores(t, func(t *testing.T, store gitOpsTestStore) {
				duration := time.Minute
				if change == "expired" {
					duration = time.Second
				}
				claimed, _, runtime := qualificationRuntimeFixture(t, store, duration)
				publisher := store.(state.EnvironmentGitOpsQualificationRuntimeStore)
				boostUntil := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
				if err := store.SetInstanceStartupCPUBoostUntil(t.Context(), claimed.ReservedInstanceID, &boostUntil); err != nil {
					t.Fatal(err)
				}
				published, err := publisher.PublishEnvironmentWorkloadQualificationRuntime(t.Context(), claimed, runtime)
				if err != nil || published.StartupCPUBoostUntil == nil || !published.StartupCPUBoostUntil.Equal(boostUntil) {
					t.Fatalf("publication lost recovery reservation: %+v %v", published, err)
				}
				ctx := t.Context()
				switch change {
				case "ram":
					ram := 256
					if _, err := store.UpdateApp(ctx, claimed.AppID, state.UpdateAppParams{RAMMB: &ram}); err != nil {
						t.Fatal(err)
					}
				case "node":
					if err := store.NodeSetLifecycle(ctx, runtime.NodeID, state.NodeLifecycleActive, state.NodeLifecycleDraining); err != nil {
						t.Fatal(err)
					}
				case "account":
					app, err := store.AppByID(ctx, claimed.AppID)
					if err != nil {
						t.Fatal(err)
					}
					if err := store.MarkAccountDeletionPending(ctx, app.AccountID); err != nil {
						t.Fatal(err)
					}
				case "cancelled":
					cancelled, cancel := context.WithCancel(ctx)
					cancel()
					ctx = cancelled
				case "expired":
					time.Sleep(max(0, time.Until(*claimed.LeaseUntil)+20*time.Millisecond))
				}
				if _, err := publisher.PublishEnvironmentWorkloadQualificationRuntime(ctx, claimed, runtime); err == nil {
					t.Fatalf("%s acknowledged stale runtime", change)
				}
				current, err := store.InstanceByID(t.Context(), published.ID)
				if err != nil || !current.StartedAt.Equal(published.StartedAt) || current.Netns != published.Netns {
					t.Fatalf("rejection changed runtime incarnation: %+v %v", current, err)
				}
			})
		})
	}
}

func TestEnvironmentGitOpsQualificationRuntimePublishesBoundInputsAndRejectsOrdinaryAcknowledgements(t *testing.T) {
	stores(t, func(t *testing.T, store gitOpsTestStore) {
		claimed, sibling, runtime := qualificationRuntimeFixture(t, store)
		publisher, receipts := store.(state.EnvironmentGitOpsQualificationRuntimeStore), store.(state.RuntimeConfigReceiptStore)
		for _, field := range []string{"token", "attempt", "instance", "node", "wake", "scope", "inputs"} {
			forged, changed := claimed, runtime
			switch field {
			case "token":
				forged.LeaseToken = "not-issued"
			case "attempt":
				forged.Attempt++
			case "instance":
				forged.ReservedInstanceID = uuid.NewString()
			case "node":
				changed.NodeID = uuid.NewString()
			case "wake":
				changed.WakeID = uuid.NewString()
			case "scope":
				changed.Inputs.Scope = "staging"
			case "inputs":
				changed.Inputs.Variables = map[string]string{"UNDELIVERED": "different"}
			}
			if _, err := publisher.PublishEnvironmentWorkloadQualificationRuntime(t.Context(), forged, changed); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("substituted %s published runtime: %v", field, err)
			}
			ins, err := store.InstanceByID(t.Context(), claimed.ReservedInstanceID)
			_, exists, proofErr := receipts.InstanceRuntimeConfigReceipt(t.Context(), claimed.ReservedInstanceID)
			if err != nil || ins.State != string(state.StateColdBooting) || ins.Netns != "" || proofErr != nil || exists {
				t.Fatalf("rejected %s left runtime or evidence: %+v %v %v", field, ins, err, proofErr)
			}
		}
		published, err := publisher.PublishEnvironmentWorkloadQualificationRuntime(t.Context(), claimed, runtime)
		if err != nil || published.ID != claimed.ReservedInstanceID || published.State != string(state.StateRunning) || published.Netns != runtime.Netns || published.HostIP != runtime.HostIP || published.GuestUID != runtime.GuestUID {
			t.Fatalf("runtime acknowledgement: %+v %v", published, err)
		}
		retry, err := publisher.PublishEnvironmentWorkloadQualificationRuntime(t.Context(), claimed, runtime)
		if err != nil || !retry.StartedAt.Equal(published.StartedAt) {
			t.Fatalf("lost response changed runtime incarnation: %+v %v", retry, err)
		}
		changed := runtime
		changed.Netns = "replacement-netns"
		if _, err := publisher.PublishEnvironmentWorkloadQualificationRuntime(t.Context(), claimed, changed); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("second runtime replaced the acknowledged VM: %v", err)
		}
		if err := receipts.RecordInstanceRuntimeConfigReceipt(t.Context(), published.ID, published.WakeID, runtime.Inputs); err == nil {
			t.Fatal("ordinary acknowledgement borrowed the qualification runtime")
		}
		runtime.Inputs.Variables["MUTATED_CALLER"] = "not sent"
		proof, exists, err := receipts.InstanceRuntimeConfigReceipt(t.Context(), published.ID)
		if err != nil || !exists || len(proof.Variables) != 0 || proof.Scope != "production" || !proof.AllSecrets {
			t.Fatalf("runtime receipt aliased caller inputs: %+v %v", proof, err)
		}
		if err := store.MarkDeploymentLive(t.Context(), claimed.DeploymentID); err == nil {
			t.Fatal("runtime acknowledgement became activation authority")
		}
		if err := store.SetDeploymentRootfs(t.Context(), sibling.DeploymentID, "/changed.ext4", "changed", 4096); err != nil {
			t.Fatal(err)
		}
		runtime.Inputs = proof
		if _, err := publisher.PublishEnvironmentWorkloadQualificationRuntime(t.Context(), claimed, runtime); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("superseded cohort acknowledged its old runtime: %v", err)
		}
	})
}

func TestPgEnvironmentGitOpsQualificationRuntimeAtomicRollbackSQLAuthorityAndReplay(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	claimed, _, runtime := qualificationRuntimeFixture(t, store)
	if _, err := pool.Exec(t.Context(), `create function reject_qualification_runtime_receipt() returns trigger language plpgsql as $$ begin raise exception 'injected runtime evidence failure'; end $$;
 create trigger reject_qualification_runtime_receipt before insert on instance_runtime_config_receipts for each row execute function reject_qualification_runtime_receipt()`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishEnvironmentWorkloadQualificationRuntime(t.Context(), claimed, runtime); err == nil {
		t.Fatal("receipt failure was hidden")
	}
	ins, err := store.InstanceByID(t.Context(), claimed.ReservedInstanceID)
	_, exists, proofErr := store.InstanceRuntimeConfigReceipt(t.Context(), ins.ID)
	if err != nil || ins.State != string(state.StateColdBooting) || ins.Netns != "" || proofErr != nil || exists {
		t.Fatalf("failed receipt left readiness visible: %+v %v %v", ins, err, proofErr)
	}
	if _, err := pool.Exec(t.Context(), `drop trigger reject_qualification_runtime_receipt on instance_runtime_config_receipts`); err != nil {
		t.Fatal(err)
	}
	published, err := store.PublishEnvironmentWorkloadQualificationRuntime(t.Context(), claimed, runtime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `update instance_runtime_config_receipts set acknowledged_at=clock_timestamp() where instance_id=$1`, ins.ID); err == nil {
		t.Fatal("raw acknowledgement bypassed attempt authority")
	}
	for _, assignment := range []string{`wake_id=gen_random_uuid()`, `scope='staging'`, `variables='{"NOT_DELIVERED":"x"}'::jsonb`} {
		tx, err := pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(t.Context(), `select set_config('gregale.gitops_qualification',$1,true)`, claimed.LeaseToken); err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(t.Context(), `update instance_runtime_config_receipts set `+assignment+` where instance_id=$1`, ins.ID)
		_ = tx.Rollback(t.Context())
		if err == nil {
			t.Fatalf("issued token accepted substituted receipt: %s", assignment)
		}
	}
	if _, err := pool.Exec(t.Context(), `delete from goose_db_version where version_id=20261002174258000`); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	if retry, err := store.PublishEnvironmentWorkloadQualificationRuntime(t.Context(), claimed, runtime); err != nil || !retry.StartedAt.Equal(published.StartedAt) {
		t.Fatalf("populated replay changed runtime proof: %+v %v", retry, err)
	}
}

func TestPgEnvironmentGitOpsQualificationRuntimeReceiptWaitsForConcurrentRetirement(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	claimed, _, runtime := qualificationRuntimeFixture(t, store)
	if _, err := store.PublishEnvironmentWorkloadQualificationRuntime(t.Context(), claimed, runtime); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	retiring, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = retiring.Rollback(context.Background()) }()
	if _, err := retiring.Exec(ctx, `update instances set state='stopped',terminal_at=clock_timestamp() where id=$1`, claimed.ReservedInstanceID); err != nil {
		t.Fatal(err)
	}
	writer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback(context.Background()) }()
	if _, err := writer.Exec(ctx, `select set_config('gregale.gitops_qualification',$1,true)`, claimed.LeaseToken); err != nil {
		t.Fatal(err)
	}
	var pid int
	if err := writer.QueryRow(ctx, `select pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := writer.Exec(ctx, `update instance_runtime_config_receipts set acknowledged_at=clock_timestamp() where instance_id=$1`, claimed.ReservedInstanceID)
		result <- err
	}()
	for {
		select {
		case err := <-result:
			t.Fatalf("receipt writer did not serialize behind retirement: %v", err)
		default:
		}
		var blocked bool
		if err := pool.QueryRow(ctx, `select coalesce(wait_event_type='Lock',false) from pg_stat_activity where pid=$1`, pid).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("receipt never reached its incarnation lock")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := retiring.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("retired instance published a stale runtime acknowledgement")
		}
	case <-ctx.Done():
		t.Fatal("receipt writer did not finish after retirement")
	}
}
