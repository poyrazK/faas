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

func TestMigrationPreviewProjectWorkloadIdentity(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	accountID := seedAccount(t, ctx, pool)
	projectID := uuid.NewString()
	if _, err := pool.Exec(ctx, `insert into projects (id, account_id, slug) values ($1, $2, $3)`,
		projectID, accountID, "preview-scope-"+uuid.NewString()[:8]); err != nil {
		t.Fatalf("create project: %v", err)
	}

	insertApp := func(slug, previewOf string, prNumber int) error {
		_, err := pool.Exec(ctx, `
			insert into apps
				(id, account_id, slug, ram_mb, max_concurrency, project_id,
				 workload_name, preview_of_slug, preview_pr_number)
			values ($1, $2, $3, 256, 1, $4, 'api', nullif($5, ''), $6)
		`, uuid.NewString(), accountID, slug, projectID, previewOf, prNumber)
		return err
	}

	if err := insertApp("preview-scope-api", "", 0); err != nil {
		t.Fatalf("create production app: %v", err)
	}
	if err := insertApp("pr-41-preview-scope-api", "preview-scope-api", 41); err != nil {
		t.Fatalf("create first PR preview: %v", err)
	}
	if err := insertApp("pr-42-preview-scope-api", "preview-scope-api", 42); err != nil {
		t.Fatalf("create preview in another PR: %v", err)
	}

	err := insertApp("other-pr-42-preview-scope-api", "preview-scope-api", 42)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" || pgErr.ConstraintName != "apps_preview_project_pr_workload_uniq" {
		t.Fatalf("duplicate preview identity error = %v, want apps_preview_project_pr_workload_uniq violation", err)
	}

	err = insertApp("duplicate-preview-scope-api", "", 0)
	pgErr = nil
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" || pgErr.ConstraintName != "apps_project_workload_uniq" {
		t.Fatalf("duplicate production identity error = %v, want apps_project_workload_uniq violation", err)
	}
}
