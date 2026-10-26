//go:build !no_pg

// adr: 570
package migrations_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/state"
)

// These upgrade fixtures precede service_capacity_policy and traffic epochs.
// Seed their historical parent row with SQL; the current production writer
// (store.CreateApp) requires its complete traffic schema, such as
// resource_namespaces. The app joins its account's personal org: from
// ADR-595's enrollment trigger until the org-ownership migrations, an app
// without an org_id cannot be inserted.
func seedHistoricalInvocationApp(t *testing.T, ctx context.Context, pool *pgxpool.Pool, app state.App) state.App {
	t.Helper()
	appType := app.Type
	if appType == "" {
		appType = state.AppTypeApp
	}
	if err := pool.QueryRow(ctx, `insert into apps (account_id, org_id, slug, type, ram_mb, max_concurrency,
		idle_timeout_s, status, created_at, project_id, preview_of_slug, workload_class)
		values ($1, $2::uuid, $3, $4, 128, 1, 30, 'active', now(), NULLIF($5,'')::uuid, NULLIF($6,''),
		        COALESCE(NULLIF($7,''),'http'))
		returning id::text`,
		app.AccountID, legacyPersonalOrg(t, ctx, pool, app.AccountID), app.Slug, string(appType),
		app.ProjectID, app.PreviewOfSlug, string(app.WorkloadClass)).Scan(&app.ID); err != nil {
		t.Fatalf("seed historical app %s: %v", app.Slug, err)
	}
	return app
}
