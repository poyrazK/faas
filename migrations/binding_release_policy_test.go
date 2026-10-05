//go:build !no_pg

package migrations_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestBindingReleasePolicyMigrationRoundTrip(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	defer pool.Close()
	ctx := context.Background()
	account := seedAccount(t, ctx, pool)
	app := seedApp(t, ctx, pool, account)
	_, err := pool.Exec(ctx, `INSERT INTO app_binding_release_policies(app_id,scope,mode,revision,max_age_seconds) VALUES($1,'production','enforce',1,600)`, app)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM app_binding_release_policy_history WHERE app_id=$1`, app).Scan(&count); err != nil || count != 1 {
		t.Fatalf("audit missing: %d %v", count, err)
	}
	source, err := os.ReadFile("20261004212716004_binding_release_policy.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(source), "-- +goose Down")
	if !ok {
		t.Fatal("rollback missing")
	}
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM app_binding_release_policies WHERE app_id=$1`, app).Scan(&count); err != nil || count != 0 {
		t.Fatalf("reinstall: %d %v", count, err)
	}
}
