//go:build !no_pg

// adr: 20261104090000

package meter

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestRollupCountsBuildTime pins ADR-20261104090000: build time recorded in
// builder_usage reaches usage_daily.builder_seconds, beside the app's usage
// rows, and an app-day with only a build gets a row of its own. Before, the
// rollup summed usage_minutes.builder_seconds, which nothing writes, so build
// time always read zero.
func TestRollupCountsBuildTime(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "rollup-builds-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	newApp := func() state.App {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "rollup-builds-" + uuid.NewString()[:8], Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	served, buildOnly := newApp(), newApp()
	day := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	if err := store.AppendUsage(ctx, account.ID, served.ID, uuid.NewString(), day.Add(time.Hour), 600, 1, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	for _, b := range []struct {
		app     string
		at      time.Time
		seconds int64
	}{
		{served.ID, day.Add(2 * time.Hour), 90},
		{served.ID, day.Add(3 * time.Hour), 30},
		{buildOnly.ID, day.Add(4 * time.Hour), 45},
		{buildOnly.ID, day.Add(25 * time.Hour), 999}, // next day: outside the window
	} {
		if err := store.AppendBuilderUsage(ctx, account.ID, b.app, uuid.NewString(), b.at, "railpack", b.seconds); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := RollupOnce(ctx, rollupPGAdapter{pool: pool}, day, day.Add(24*time.Hour)); err != nil {
		t.Fatalf("RollupOnce: %v", err)
	}
	read := func(appID string) (mbSeconds, builderSeconds int64) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT mb_seconds, builder_seconds FROM usage_daily WHERE account_id = $1 AND app_id = $2 AND day = $3`,
			account.ID, appID, day).Scan(&mbSeconds, &builderSeconds); err != nil {
			t.Fatalf("read usage_daily for %s: %v", appID, err)
		}
		return
	}
	if mb, builds := read(served.ID); mb != 600 || builds != 120 {
		t.Fatalf("served app = mb_seconds %d builder_seconds %d, want 600 and 120", mb, builds)
	}
	if mb, builds := read(buildOnly.ID); mb != 0 || builds != 45 {
		t.Fatalf("build-only app = mb_seconds %d builder_seconds %d, want 0 and 45", mb, builds)
	}
}
