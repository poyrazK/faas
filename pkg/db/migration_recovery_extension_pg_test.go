package db_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/db/migrationsqlc"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// A trusted extension installs its objects owned by the bootstrap superuser
// even when the migration role runs CREATE EXTENSION, so a schema migrated by
// a non-superuser `faas` holds citext functions and types it does not own.
// Those are the extension's, not another writer's; the ledger-recovery writer
// check must still refuse an ordinary object owned by someone else.
func TestMigrationRecoveryWritersIgnoreExtensionObjects(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	ctx := context.Background()
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	role := pgx.Identifier{"recovery_owner_" + hex.EncodeToString(suffix)}.Sanitize()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP OWNED BY "+role)
		_, _ = pool.Exec(context.Background(), "DROP ROLE "+role)
	})
	exec := func(sql string) {
		t.Helper()
		if _, err := conn.Exec(ctx, sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	exec("CREATE ROLE " + role + " NOLOGIN")
	exec("GRANT " + role + " TO CURRENT_USER")
	exec("CREATE SCHEMA recovery_ext AUTHORIZATION " + role)
	// Installed by the superuser, as a trusted extension's objects always are.
	exec("CREATE EXTENSION IF NOT EXISTS citext SCHEMA recovery_ext")
	exec("SET search_path = recovery_ext")
	exec("SET ROLE " + role)
	exec("CREATE TABLE recovery_ext.owned (id int PRIMARY KEY, name citext)")

	writersValid := func() bool {
		t.Helper()
		valid, err := migrationsqlc.New().CheckMigrationRecoveryWriters(ctx, conn)
		if err != nil {
			t.Fatal(err)
		}
		return valid.Valid && valid.Bool
	}
	if !writersValid() {
		t.Fatal("extension-owned citext objects made the migration role's schema look shared")
	}
	exec("RESET ROLE")
	exec("CREATE FUNCTION recovery_ext.foreign_writer() RETURNS int LANGUAGE sql AS 'select 1'")
	exec("SET ROLE " + role)
	if writersValid() {
		t.Fatal("a function another role owns was accepted")
	}
}
