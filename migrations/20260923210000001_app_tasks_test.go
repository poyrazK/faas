//go:build !no_pg

package migrations_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrationAppTasksPinsReleaseAndTerminalState(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	accountID := seedAccount(t, ctx, pool)
	appID := uuid.NewString()
	deploymentID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, ram_mb, max_concurrency)
		values ($1, $2, $3, 256, 1)`, appID, accountID, "app-task-"+uuid.NewString()[:8]); err != nil {
		t.Fatalf("create app: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, image_digest, rootfs_key, status, scope)
		values ($1, $2, 'sha256:app-task', 'apps/app-task/rootfs.ext4', 'live', 'default')`,
		deploymentID, appID); err != nil {
		t.Fatalf("create deployment: %v", err)
	}

	insertRelease := func() error {
		_, err := pool.Exec(ctx, `
			insert into app_tasks (
				account_id, app_id, deployment_id, kind, command,
				deployment_scope, artifact_key, image_digest, timeout_seconds
			) values ($1, $2, $3, 'release', array['bin/release'],
			          'default', 'apps/app-task/rootfs.ext4', 'sha256:app-task', 600)`,
			accountID, appID, deploymentID)
		return err
	}
	if err := insertRelease(); err != nil {
		t.Fatalf("create release task: %v", err)
	}
	if err := insertRelease(); err == nil {
		t.Fatal("duplicate release task succeeded")
	} else {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23505" || pgErr.ConstraintName != "app_tasks_one_release_per_deployment_uniq" {
			t.Fatalf("duplicate release error = %v, want release uniqueness violation", err)
		}
	}

	lease := uuid.NewString()
	var taskID string
	if err := pool.QueryRow(ctx, `select id from app_tasks where deployment_id = $1`, deploymentID).Scan(&taskID); err != nil {
		t.Fatalf("read task id: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		update app_tasks
		   set status = 'restoring', lease_token = $2, lease_owner = 'schedd-a',
		       lease_expires_at = now() + interval '1 minute'
		 where id = $1`, taskID, lease); err != nil {
		t.Fatalf("claim task: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		update app_tasks set status = 'running', started_at = now() where id = $1`, taskID); err != nil {
		t.Fatalf("start task: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		update app_tasks
		   set status = 'succeeded', exit_code = 0, finished_at = now(),
		       lease_token = null, lease_owner = null, lease_expires_at = null
		 where id = $1`, taskID); err != nil {
		t.Fatalf("complete task: %v", err)
	}
	_, err := pool.Exec(ctx, `update app_tasks set stdout_tail = 'mutated' where id = $1`, taskID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("terminal task mutation error = %v, want 23514", err)
	}
}
