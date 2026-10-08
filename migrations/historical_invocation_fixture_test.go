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
// Seed their historical parent row using the migration helpers; the current
// production writer must continue requiring its complete traffic schema.
func seedHistoricalInvocationApp(t *testing.T, ctx context.Context, pool *pgxpool.Pool, app state.App) state.App {
	t.Helper()
	app.ID = seedApp(t, ctx, pool, app.AccountID)
	appType := app.Type
	if appType == "" {
		appType = state.AppTypeApp
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET slug=$2, type=$3,
		project_id=NULLIF($4,'')::uuid, preview_of_slug=NULLIF($5,''),
		workload_class=COALESCE(NULLIF($6,''),'http') WHERE id=$1`,
		app.ID, app.Slug, string(appType), app.ProjectID, app.PreviewOfSlug, string(app.WorkloadClass)); err != nil {
		t.Fatal(err)
	}
	return app
}
