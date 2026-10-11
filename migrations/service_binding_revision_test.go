//go:build !no_pg

package migrations_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestServiceBindingRevisionMigrationRoundTrip(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	defer pool.Close()
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	acct := seedAccount(t, ctx, pool)
	app := seedApp(t, ctx, pool, acct)
	if _, err := pool.Exec(ctx, `UPDATE apps SET manifest='{"service_bindings":[{"service":"billing","binding":"GREGALE_SERVICE_BILLING_URL"}]}' WHERE id=$1`, app); err != nil {
		t.Fatal(err)
	}
	read := func() string {
		t.Helper()
		var token string
		if err := pool.QueryRow(ctx, `SELECT epoch::text||':'||service_revision::text FROM app_binding_promotion_revisions WHERE app_id=$1`, app).Scan(&token); err != nil {
			t.Fatal(err)
		}
		return token
	}
	before := read()
	rows, err := pool.Query(ctx, `SELECT c.relname,t.tgargs FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema() AND t.tgname='service_binding_revision'`)
	if err != nil {
		t.Fatal(err)
	}
	args := map[string][]string{}
	for rows.Next() {
		var table string
		var raw []byte
		if err := rows.Scan(&table, &raw); err != nil {
			t.Fatal(err)
		}
		args[table] = strings.Split(strings.TrimRight(string(raw), "\x00"), ",")
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(args) != 3 {
		t.Fatalf("service dependency triggers=%v", args)
	}
	for table, columns := range args {
		for _, column := range columns {
			var exists bool
			if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2)`, table, column).Scan(&exists); err != nil || !exists {
				t.Fatalf("absent watched column %s.%s: %v", table, column, err)
			}
		}
	}
	source, err := os.ReadFile("20261004190339147_service_binding_dependency_revision.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(source), "-- +goose Down")
	if !ok {
		t.Fatal("missing migration rollback")
	}
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	var service string
	if err := pool.QueryRow(ctx, `SELECT manifest->'service_bindings'->0->>'service' FROM apps WHERE id=$1`, app).Scan(&service); err != nil || service != "billing" {
		t.Fatalf("rollback changed app: %q %v", service, err)
	}
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	if read() == before {
		t.Fatal("migration reinstallation reused service evidence token")
	}
}
