//go:build !no_pg

package migrations_test

import (
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestAlertHistoricalRollbackMigrationRoundTrip(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	defer pool.Close()
	ctx := t.Context()
	account := seedAccount(t, ctx, pool)
	app := seedApp(t, ctx, pool, account)
	prior, candidate := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO deployments(id,app_id,image_digest,status,traffic_percent,rollout_state) VALUES($1,$2,'sha256:fixture','live',100,'complete')`, prior, app); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO deployments(id,app_id,image_digest,status,traffic_percent,rollout_state) VALUES($1,$2,'sha256:fixture','pending',0,'pending')`, candidate, app); err != nil {
		t.Fatal(err)
	}
	var predecessor string
	if err := pool.QueryRow(ctx, `SELECT predecessor_deployment_id FROM deployment_recovery_lineage WHERE deployment_id=$1`, candidate).Scan(&predecessor); err != nil || predecessor != prior {
		t.Fatalf("lineage %s %v", predecessor, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO alert_historical_rollback_claims(deployment_id,fire_id) VALUES($1,$2)`, candidate, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile("20261005195805324_alert_historical_rollback.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(source), "-- +goose Down")
	if !ok {
		t.Fatal("missing down migration")
	}
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM deployment_recovery_lineage`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unexpected lineage backfill %d %v", count, err)
	}
}
