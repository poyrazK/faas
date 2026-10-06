package pgintegration_test

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func migrationRecoveryDestination(t *testing.T, store state.Store) state.ComputeNode {
	t.Helper()
	node, err := store.CreateComputeNode(t.Context(), state.ComputeNode{Name: "commit-recovery-" + uuid.NewString(), Active: true,
		TargetURL: "tcp://127.0.0.1:50052", AdmissionCeilingMB: 4096, MemMB: 8192, VPCPUs: 4, VCPUBudget: 160, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func TestEnvironmentGitOpsRuntimeMigrationCommitRecoveryFencesAbortAndRetainsReceipts(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, _, _, _, _, original := runtimeFixture(t, basic, true)
		recovery := basic.(state.MigrationCommitRecoveryStore)
		receipts := basic.(state.RuntimeConfigReceiptStore)
		inputs, exists, err := receipts.InstanceRuntimeConfigReceipt(t.Context(), original.ID)
		if err != nil || !exists {
			t.Fatalf("source receipt: %v %v", exists, err)
		}
		destination := migrationRecoveryDestination(t, store)
		attempt := state.MigrationCommitAttempt{InstanceID: original.ID, SourceNodeID: original.NodeID, DestinationNodeID: destination.ID,
			LeaseToken: "recovery-lease", SourceWakeID: original.WakeID, DestinationWakeID: uuid.NewString()}
		if err := store.MarkInstanceMigrating(t.Context(), original.ID, original.NodeID, attempt.LeaseToken); err != nil {
			t.Fatal(err)
		}
		wrong := attempt
		wrong.SourceWakeID = uuid.NewString()
		if resolution, err := recovery.ResolveInstanceMigrationCommit(t.Context(), wrong); err != nil || resolution != state.MigrationCommitRetained {
			t.Fatalf("another source wake permitted cleanup: %s %v", resolution, err)
		}
		if err := store.UpdateInstanceState(t.Context(), original.ID, string(state.StateFailed)); err != nil {
			t.Fatal(err)
		}
		if resolution, err := recovery.ResolveInstanceMigrationCommit(t.Context(), wrong); err != nil || resolution != state.MigrationCommitRetained {
			t.Fatalf("another terminal source wake permitted cleanup: %s %v", resolution, err)
		}
		if err := store.UpdateInstanceState(t.Context(), original.ID, string(state.StateMigrating)); err != nil {
			t.Fatal(err)
		}
		wrong = attempt
		wrong.DestinationWakeID = attempt.SourceWakeID
		if resolution, err := recovery.ResolveInstanceMigrationCommit(t.Context(), wrong); !errors.Is(err, state.ErrInvalidArgument) || resolution != state.MigrationCommitRetained {
			t.Fatalf("reused source wake permitted recovery: %s %v", resolution, err)
		}
		wrong = attempt
		wrong.LeaseToken = "another-lease"
		if resolution, err := recovery.ResolveInstanceMigrationCommit(t.Context(), wrong); err != nil || resolution != state.MigrationCommitRetained {
			t.Fatalf("another lease permitted cleanup: %s %v", resolution, err)
		}
		if resolution, err := recovery.ResolveInstanceMigrationCommit(t.Context(), attempt); err != nil || resolution != state.MigrationCommitAborted {
			t.Fatalf("pending commit was not fenced: %s %v", resolution, err)
		}
		publish := state.RuntimeConfigMigration{ExpectedWakeID: original.WakeID, WakeID: attempt.DestinationWakeID, Inputs: &inputs,
			Netns: "recovery-netns", HostIP: "10.100.0.22", GuestUID: 20022}
		if err := basic.(state.RuntimeConfigMigrationStore).MigrateInstanceOwnerWithRuntimeConfig(t.Context(), original.ID, original.NodeID, destination.ID, attempt.LeaseToken, publish); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("late ownership commit escaped the abort fence: %v", err)
		}
		if resolution, err := recovery.ResolveInstanceMigrationCommit(t.Context(), attempt); err != nil || resolution != state.MigrationCommitAborted {
			t.Fatalf("abort retry lost its same-wake result: %s %v", resolution, err)
		}
		if err := store.UpdateInstanceState(t.Context(), original.ID, string(state.StateRunning)); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkInstanceMigrating(t.Context(), original.ID, original.NodeID, attempt.LeaseToken); err != nil {
			t.Fatal(err)
		}
		if err := basic.(state.RuntimeConfigMigrationStore).MigrateInstanceOwnerWithRuntimeConfig(t.Context(), original.ID, original.NodeID, destination.ID, attempt.LeaseToken, publish); err != nil {
			t.Fatal(err)
		}
		if resolution, err := recovery.ResolveInstanceMigrationCommit(t.Context(), attempt); err != nil || resolution != state.MigrationCommitRecovered {
			t.Fatalf("committed handoff was not recovered: %s %v", resolution, err)
		}
		wrong = attempt
		wrong.DestinationWakeID = uuid.NewString()
		if resolution, err := recovery.ResolveInstanceMigrationCommit(t.Context(), wrong); err != nil || resolution != state.MigrationCommitRetained {
			t.Fatalf("another destination wake permitted cleanup: %s %v", resolution, err)
		}
		proof, exists, err := receipts.InstanceRuntimeConfigReceipt(t.Context(), original.ID)
		if err != nil || !exists || !maps.Equal(proof.Variables, inputs.Variables) || !proof.Boundary.Equal(inputs.Boundary) {
			t.Fatalf("commit recovery changed input evidence: %+v %v %v", proof, exists, err)
		}
		if err := store.DeleteInstance(t.Context(), original.ID); err != nil {
			t.Fatal(err)
		}
		if resolution, err := recovery.ResolveInstanceMigrationCommit(t.Context(), attempt); err != nil || resolution != state.MigrationCommitObsolete {
			t.Fatalf("removed row did not permit lease-bound cleanup: %s %v", resolution, err)
		}
	})
}

func TestEnvironmentGitOpsRuntimeMigrationRecoveryWaitsForOriginalTransaction(t *testing.T) {
	for _, commit := range []bool{true, false} {
		t.Run(map[bool]string{true: "original commit lands", false: "original transaction rolls back"}[commit], func(t *testing.T) {
			pool := pgtest.OpenMigrated(t)
			if err := db.MigrateUp(t.Context(), pool); err != nil {
				t.Fatal(err)
			}
			store := state.NewPgStore(pool)
			_, _, _, _, _, original := runtimeFixture(t, store, true)
			destination := migrationRecoveryDestination(t, store)
			attempt := state.MigrationCommitAttempt{InstanceID: original.ID, SourceNodeID: original.NodeID, DestinationNodeID: destination.ID,
				LeaseToken: "transaction-recovery-lease", SourceWakeID: original.WakeID, DestinationWakeID: uuid.NewString()}
			if err := store.MarkInstanceMigrating(t.Context(), original.ID, original.NodeID, attempt.LeaseToken); err != nil {
				t.Fatal(err)
			}
			writer, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = writer.Rollback(t.Context()) }()
			// Hold a complete but uncommitted ownership/receipt publication.
			// The recovery read must wait for this transaction, not observe the
			// older source row through ordinary MVCC and authorize destruction.
			if _, err := writer.Exec(t.Context(), `UPDATE instances SET node_id = $2, wake_id = $3,
                migrated_from_node_id = $4, state = 'running', migration_started_at = NULL
                WHERE id = $1`, original.ID, destination.ID, attempt.DestinationWakeID, original.NodeID); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Exec(t.Context(), `UPDATE instance_runtime_config_receipts SET wake_id = $2 WHERE instance_id = $1`, original.ID, attempt.DestinationWakeID); err != nil {
				t.Fatal(err)
			}
			cfg := pool.Config().Copy()
			name := "gitops-migration-recovery-" + uuid.NewString()
			cfg.ConnConfig.RuntimeParams["application_name"] = name
			reader, err := pgxpool.NewWithConfig(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			type outcome struct {
				resolution state.MigrationCommitResolution
				err        error
			}
			result := make(chan outcome, 1)
			go func() {
				resolution, err := state.NewPgStore(reader).ResolveInstanceMigrationCommit(ctx, attempt)
				result <- outcome{resolution, err}
			}()
			for {
				var waiting bool
				if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name = $1 AND wait_event_type = 'Lock')`, name).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case got := <-result:
					t.Fatalf("recovery bypassed the ownership transaction: %+v", got)
				case <-ctx.Done():
					t.Fatal("recovery never reached the ownership lock", ctx.Err())
				case <-time.After(10 * time.Millisecond):
				}
			}
			// An unavailable outcome grants no cleanup authority, even though
			// ordinary reads can still see the source's older committed row.
			timedCtx, timedCancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			resolution, timeoutErr := state.NewPgStore(reader).ResolveInstanceMigrationCommit(timedCtx, attempt)
			timedCancel()
			if resolution != state.MigrationCommitRetained || !errors.Is(timeoutErr, context.DeadlineExceeded) {
				t.Fatalf("blocked resolution permitted cleanup: %s %v", resolution, timeoutErr)
			}
			if commit {
				err = writer.Commit(ctx)
			} else {
				err = writer.Rollback(ctx)
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-result:
				want := state.MigrationCommitAborted
				if commit {
					want = state.MigrationCommitRecovered
				}
				if got.err != nil || got.resolution != want {
					t.Fatalf("recovery after original transaction: %+v, want %s", got, want)
				}
			case <-ctx.Done():
				t.Fatal("recovery did not finish after ownership lock released", ctx.Err())
			}
			proof, exists, err := store.InstanceRuntimeConfigReceipt(t.Context(), original.ID)
			if err != nil || !exists || proof.Scope != "production" {
				t.Fatalf("ownership resolution lost atomic receipt evidence: %+v %v %v", proof, exists, err)
			}
		})
	}
}
