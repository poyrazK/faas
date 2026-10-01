// adr: 393 — durable command admission shares the app tuple cutover barrier.
package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func createCutoverAppTask(t *testing.T, ps *state.PgStore, account, app string, kind state.AppTaskKind) state.AppTask {
	t.Helper()
	ctx := t.Context()
	d, err := ps.CreateDeployment(ctx, state.Deployment{AppID: app, Kind: state.DeploymentKindImage, ImageDigest: "sha256:cutover-task"})
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []state.DeploymentStatus{state.DeployBuilding, state.DeployImaging} {
		if err := ps.UpdateDeploymentStatus(ctx, d.ID, status, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := ps.SetDeploymentRootfs(ctx, d.ID, "/tmp/cutover.ext4", "apps/cutover/task.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	params := state.CreateAppTaskParams{AccountID: account, AppID: app, DeploymentID: d.ID, Kind: kind,
		Command: []string{"bin/migrate"}, CreatedAt: time.Now().UTC().Add(-time.Second)}
	if kind == state.AppTaskKindCron {
		cron, err := ps.CreateCronWithOptions(ctx, app, "* * * * *", "", true, state.CronOptions{Command: params.Command})
		if err != nil {
			t.Fatal(err)
		}
		params.CronID = cron.ID
	}
	task, err := ps.CreateAppTask(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestPostgresCutoverFenceBlocksCommandDispatch(t *testing.T) {
	for _, kind := range []state.AppTaskKind{state.AppTaskKindManual, state.AppTaskKindRelease, state.AppTaskKindCron} {
		t.Run(string(kind), func(t *testing.T) {
			s, ps, c, _ := admissionCutoverFixture(t)
			task := createCutoverAppTask(t, ps, c.AccountID, c.AppID, kind)
			claimed, err := ps.ClaimNextAppTask(t.Context(), "schedd", time.Now(), time.Minute)
			if err != nil || claimed.ID != task.ID {
				t.Fatalf("claim: %+v, %v", claimed, err)
			}
			if _, err := s.FenceCutoverAdmission(t.Context(), c.AccountID, c.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := ps.MarkAppTaskRunning(t.Context(), task.ID, *claimed.LeaseToken, time.Now()); !errors.Is(err, state.ErrManagedPostgresAdmissionFenced) {
				t.Fatalf("command crossed the fence: %v", err)
			}
			// Heartbeats, cancellation and terminal cleanup must remain writable.
			if err := ps.RenewAppTaskLease(t.Context(), task.ID, *claimed.LeaseToken, time.Now(), time.Minute); err != nil {
				t.Fatal(err)
			}
			if _, err := ps.RequestAppTaskCancellation(t.Context(), c.AccountID, c.AppID, task.ID, time.Now()); err != nil {
				t.Fatal(err)
			}
			if _, err := ps.CompleteAppTask(t.Context(), state.CompleteAppTaskParams{ID: task.ID, LeaseToken: *claimed.LeaseToken,
				Status: state.AppTaskCancelled, FinishedAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPostgresCutoverFenceLeavesQueuedTasksAndDispatchesOtherApps(t *testing.T) {
	s, ps, c, _ := admissionCutoverFixture(t)
	queued := createCutoverAppTask(t, ps, c.AccountID, c.AppID, state.AppTaskKindManual)
	if _, err := s.FenceCutoverAdmission(t.Context(), c.AccountID, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := ps.ClaimNextAppTask(t.Context(), "schedd", time.Now(), time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("fenced task was claimed: %v", err)
	}
	other, err := ps.CreateApp(t.Context(), state.App{AccountID: c.AccountID, Slug: "other-task-app", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	otherTask := createCutoverAppTask(t, ps, c.AccountID, other.ID, state.AppTaskKindManual)
	claimed, err := ps.ClaimNextAppTask(t.Context(), "schedd", time.Now(), time.Minute)
	if err != nil || claimed.ID != otherTask.ID {
		t.Fatalf("fenced app starved another app: %+v, %v", claimed, err)
	}
	if _, err := ps.MarkAppTaskRunning(t.Context(), claimed.ID, *claimed.LeaseToken, time.Now()); err != nil {
		t.Fatal(err)
	}
	saved, err := ps.AppTaskByID(t.Context(), c.AccountID, c.AppID, queued.ID)
	if err != nil || saved.Status != state.AppTaskQueued || saved.LeaseToken != nil {
		t.Fatalf("queued task was lost: %+v, %v", saved, err)
	}
	_, down := cutoverMigrationStatements(t, "20261001184558948_managed_postgres_cutover_task_fence.sql")
	if _, err := s.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback removed a live task admission barrier")
	}
}

func TestPostgresCutoverTaskFenceRejectsOldTransactions(t *testing.T) {
	for _, isolation := range []pgx.TxIsoLevel{pgx.ReadCommitted, pgx.RepeatableRead} {
		t.Run(string(isolation), func(t *testing.T) {
			s, ps, c, _ := admissionCutoverFixture(t)
			task := createCutoverAppTask(t, ps, c.AccountID, c.AppID, state.AppTaskKindManual)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: isolation})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
			// Establish a snapshot before the app tuple is changed.
			if _, err := sqlc.New().ManagedPostgresAdmissionFenced(ctx, tx, c.AppID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.FenceCutoverAdmission(ctx, c.AccountID, c.ID); err != nil {
				t.Fatal(err)
			}
			// Direct writers cannot bypass the filtered claim query.
			_, err = tx.Exec(ctx, `UPDATE app_tasks SET status='restoring',lease_token=gen_random_uuid(),lease_owner='stale',lease_expires_at=clock_timestamp()+interval '1 minute' WHERE id=$1`, task.ID)
			var pgerr *pgconn.PgError
			if !errors.As(err, &pgerr) || (isolation == pgx.ReadCommitted && (pgerr.Code != "23514" || pgerr.ConstraintName != "managed_postgres_cutover_admission_fenced")) ||
				(isolation == pgx.RepeatableRead && pgerr.Code != "40001") {
				t.Fatalf("old transaction admitted command work: %v", err)
			}
		})
	}
}
