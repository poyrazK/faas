//go:build !no_pg

package migrations_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestDeploymentServingEndedAtMigration — the trigger stamps the moment a
// deployment stops serving (live with traffic → anything else), clears the
// stamp when it serves again, and leaves rows that never served unstamped.
// The Down section removes the column and trigger cleanly.
func TestDeploymentServingEndedAtMigration(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@serving-ended.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: "se-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	stamped := func() bool {
		t.Helper()
		var at *time.Time
		if err := pool.QueryRow(ctx, `SELECT serving_ended_at FROM deployments WHERE id=$1`, dep.ID).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at != nil
	}
	exec := func(sql string) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, dep.ID); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	exec(`UPDATE deployments SET status='live', traffic_percent=100 WHERE id=$1`)
	if stamped() {
		t.Fatal("a serving deployment carries serving_ended_at")
	}
	exec(`UPDATE deployments SET status='superseded', traffic_percent=0 WHERE id=$1`)
	if !stamped() {
		t.Fatal("superseding a serving deployment did not stamp serving_ended_at")
	}
	exec(`UPDATE deployments SET status='live', traffic_percent=100 WHERE id=$1`)
	if stamped() {
		t.Fatal("serving again did not clear serving_ended_at")
	}
	exec(`UPDATE deployments SET traffic_percent=0 WHERE id=$1`)
	if !stamped() {
		t.Fatal("dropping a live deployment to 0% did not stamp serving_ended_at")
	}

	raw, err := migrations.FS.ReadFile("20261004234807528_deployment_serving_ended_at.sql")
	if err != nil {
		t.Fatal(err)
	}
	sections := strings.SplitN(string(raw), "-- +goose Down", 2)
	if len(sections) != 2 {
		t.Fatal("migration has no Down section")
	}
	if _, err := pool.Exec(ctx, sections[1]); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var columns int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name='deployments' AND column_name='serving_ended_at'`).Scan(&columns); err != nil || columns != 0 {
		t.Fatalf("Down left serving_ended_at: columns=%d err=%v", columns, err)
	}
	exec(`UPDATE deployments SET status='superseded', traffic_percent=0 WHERE id=$1`)
}
