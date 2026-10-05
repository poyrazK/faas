//go:build !no_pg

package migrations_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMigrationInvocationDeploymentScopeBackfillAndIdentity(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	migrateUpTo(t, ctx, pool, 20261001060000001)
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "invocation-migration@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "migration-project"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, projectID, preview, want string
	}{
		{"standalone", "", "", "default"},
		{"project-member", project.ID, "", "production"},
		{"project-preview", project.ID, "project-member", "default"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: tc.name, ProjectID: tc.projectID, PreviewOfSlug: tc.preview})
			if err != nil {
				t.Fatal(err)
			}
			var invocationID string
			if err := pool.QueryRow(ctx, `INSERT INTO invocations(app_id, account_id, source)
			  VALUES ($1, $2, 'queue') RETURNING id`, app.ID, account.ID).Scan(&invocationID); err != nil {
				t.Fatal(err)
			}
		})
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ slug, want string }{
		{"standalone", "default"}, {"project-member", "production"}, {"project-preview", "default"},
	} {
		var id, scope string
		if err := pool.QueryRow(ctx, `SELECT i.id, i.deployment_scope FROM invocations i
		  JOIN apps a ON a.id = i.app_id WHERE a.slug = $1`, tc.slug).Scan(&id, &scope); err != nil || scope != tc.want {
			t.Fatalf("backfill %s = %q, want %q: %v", tc.slug, scope, tc.want, err)
		}
		if _, err := pool.Exec(ctx, `UPDATE invocations SET deployment_scope = 'other-env' WHERE id = $1`, id); err == nil {
			t.Fatal("scope identity was mutable")
		} else {
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "invocation_deployment_scope_identity" {
				t.Fatalf("wrong identity failure: %v", err)
			}
		}
		var insertedScope string
		if err := pool.QueryRow(ctx, `INSERT INTO invocations(app_id, account_id, source)
		  SELECT app_id, account_id, source FROM invocations WHERE id = $1
		  RETURNING deployment_scope`, id).Scan(&insertedScope); err != nil || insertedScope != tc.want {
			t.Fatalf("legacy producer scope = %q, want %q: %v", insertedScope, tc.want, err)
		}
	}
}
