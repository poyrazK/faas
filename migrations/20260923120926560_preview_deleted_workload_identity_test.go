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

func TestMigrationPreviewDeletedWorkloadIdentity(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	accountID := seedAccount(t, ctx, pool)
	projectID := uuid.NewString()
	if _, err := pool.Exec(ctx, `insert into projects (id, account_id, slug) values ($1, $2, $3)`,
		projectID, accountID, "deleted-preview-"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	insert := func(slug string) (string, error) {
		id := uuid.NewString()
		_, err := pool.Exec(ctx, `insert into apps
			(id, account_id, slug, ram_mb, max_concurrency, project_id,
			 workload_name, preview_of_slug, preview_pr_number)
			values ($1, $2, $3, 256, 1, $4, 'worker', 'production-worker', 42)`,
			id, accountID, slug, projectID)
		return id, err
	}
	firstID, err := insert("pr-42-deleted-preview-worker")
	if err != nil {
		t.Fatal(err)
	}
	_, err = insert("pr-42-duplicate-live-worker")
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" || pgErr.ConstraintName != "apps_preview_project_pr_workload_uniq" {
		t.Fatalf("duplicate live workload error = %v, want preview workload uniqueness", err)
	}
	if _, err := pool.Exec(ctx, `update apps set status = 'deleted' where id = $1`, firstID); err != nil {
		t.Fatal(err)
	}
	_, err = insert("pr-42-deleted-preview-worker")
	pgErr = nil
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" || pgErr.ConstraintName != "apps_slug_key" {
		t.Fatalf("deleted row reused global slug: %v", err)
	}
	if _, err := insert("pr-42-readded-preview-worker"); err != nil {
		t.Fatalf("deleted row still owns preview workload identity: %v", err)
	}
}
