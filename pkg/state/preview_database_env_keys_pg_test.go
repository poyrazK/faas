//go:build !no_pg

// adr: 20261102123523 — the keys a PR preview gets from its own databases.
package state_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPgAppPreviewDatabaseEnvKeys(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	acct, app := seedPgAccountAndApp(t, st, ctx)
	database := func(name, previews string) string {
		t.Helper()
		id := uuid.NewString()
		if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_databases(id,account_id,name,region,postgres_major,service_class,availability,backend_id,backend_fingerprint) VALUES($1,$2,$3,'eu',17,'development','single_zone','test',$4)`,
			id, acct.ID, name, strings.Repeat("a", 64)); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE managed_postgres_databases SET previews=$2 WHERE id=$1`, id, previews); err != nil {
			t.Fatal(err)
		}
		return id
	}
	bind := func(databaseID, scope, key string) string {
		t.Helper()
		id := uuid.NewString()
		if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_bindings(id,account_id,database_id,app_id,scope,environment_key,access) VALUES($1,$2,$3,$4,$5,$6,'read_write')`,
			id, acct.ID, databaseID, app.ID, scope, key); err != nil {
			t.Fatal(err)
		}
		return id
	}
	copied := database("copied", "copy")
	empty := database("empty", "empty")
	off := database("off", "off")
	bind(copied, "default", "DATABASE_URL")
	bind(empty, "production", "EVENTS_URL")
	bind(copied, "staging", "STAGING_URL") // not a scope production serves
	bind(off, "default", "LEGACY_URL")     // previews off
	removed := bind(empty, "default", "REMOVED_URL")
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_bindings SET state='deleted', deleted_at=now() WHERE id=$1`, removed); err != nil {
		t.Fatal(err)
	}
	// A failed binding never delivered a credential; previews do not copy it.
	failed := bind(empty, "default", "FAILED_URL")
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_bindings SET state='failed' WHERE id=$1`, failed); err != nil {
		t.Fatal(err)
	}
	got, err := st.AppPreviewDatabaseEnvKeys(ctx, acct.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"DATABASE_URL", "EVENTS_URL"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("preview database keys = %v, want %v", got, want)
	}
	if got, err := st.AppPreviewDatabaseEnvKeys(ctx, acct.ID, uuid.NewString()); err != nil || len(got) != 0 {
		t.Fatalf("another app's keys = %v, %v", got, err)
	}
}
