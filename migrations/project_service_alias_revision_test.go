//go:build !no_pg

package migrations_test

import (
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestProjectServiceAliasRevisionMigrationRoundTrip(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	defer pool.Close()
	ctx := t.Context()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	account := seedAccount(t, ctx, pool)
	app := seedApp(t, ctx, pool, account)
	if _, err := pool.Exec(ctx, `UPDATE apps SET manifest='{"service_bindings":[{"service":"api","binding":"GREGALE_SERVICE_API_URL"}]}' WHERE id=$1`, app); err != nil {
		t.Fatal(err)
	}
	read := func() string {
		t.Helper()
		var revision string
		if err := pool.QueryRow(ctx, `SELECT epoch::text||':'||service_revision::text FROM app_binding_promotion_revisions WHERE app_id=$1`, app).Scan(&revision); err != nil {
			t.Fatal(err)
		}
		return revision
	}
	before := read()
	if _, err := pool.Exec(ctx, `UPDATE apps SET workload_name='repository-api' WHERE id=$1`, app); err != nil {
		t.Fatal(err)
	}
	if read() == before {
		t.Fatal("repository name change retained routing evidence")
	}
	source, err := os.ReadFile("20261017190925575_project_service_alias_revision.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(source), "-- +goose Down")
	if !ok {
		t.Fatal("missing rollback")
	}
	for _, sql := range []string{down, up} {
		before = read()
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
		if read() == before {
			t.Fatal("migration direction retained old service evidence")
		}
	}
	var name string
	if err := pool.QueryRow(ctx, `SELECT workload_name FROM apps WHERE id=$1`, app).Scan(&name); err != nil || name != "repository-api" {
		t.Fatalf("migration changed repository identity: %q %v", name, err)
	}
}
