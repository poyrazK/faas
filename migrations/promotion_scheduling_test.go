//go:build !no_pg

package migrations_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestPromotionSchedulingMigrationRoundTrip(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	account := seedAccount(t, ctx, pool)
	project, id := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, "INSERT INTO projects(id,account_id,slug) VALUES($1,$2,'schedule-roundtrip')", project, account); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO project_environment_promotions(id,account_id,project_id,project_slug,from_environment,to_environment,promotion_hash,idempotency_key) VALUES($1,$2,$3,'schedule-roundtrip','staging','production',$4,'roundtrip')", id, account, project, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO project_environment_promotion_health(promotion_id,generation,phase,payload) VALUES($1,1,'preflight','{"phase":"preflight","generation":1,"policy":{}}')`, id); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob("*_promotion_scheduling.sql")
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("missing down migration")
	}
	if _, err = pool.Exec(ctx, "UPDATE project_environment_promotion_health SET lease_token=$2,lease_until=clock_timestamp()+interval '1 minute' WHERE promotion_id=$1", id, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, down); err == nil {
		t.Fatal("active lease allowed downgrade")
	}
	if _, err = pool.Exec(ctx, "UPDATE project_environment_promotion_health SET lease_token=NULL,lease_until='-infinity' WHERE promotion_id=$1", id); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, down); err != nil {
		t.Fatal("empty-lease downgrade", err)
	}
	if _, err = pool.Exec(ctx, up); err != nil {
		t.Fatal("reinstall", err)
	}
	var generation int64
	if err = pool.QueryRow(ctx, "SELECT generation FROM project_environment_promotion_health WHERE promotion_id=$1", id).Scan(&generation); err != nil || generation != 1 {
		t.Fatal("health checkpoint lost", generation, err)
	}
}
