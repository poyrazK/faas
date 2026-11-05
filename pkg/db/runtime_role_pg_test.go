package db_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// runtimeRolePool returns a pool on owner's database whose sessions run as a
// fresh NOLOGIN role holding the faas_runtime grant set (ADR-20261023104734).
// SET ROLE through the startup packet stands in for a peer-mapped login,
// which a shared test cluster cannot provision.
func runtimeRolePool(t *testing.T, owner *pgxpool.Pool) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	name := "faas_rt_" + hex.EncodeToString(suffix)
	role := pgx.Identifier{name}.Sanitize()
	var current string
	if err := owner.QueryRow(ctx, "SELECT current_schema()").Scan(&current); err != nil {
		t.Fatal(err)
	}
	schema := pgx.Identifier{current}.Sanitize()
	t.Cleanup(func() {
		_, _ = owner.Exec(context.Background(), "DROP OWNED BY "+role)
		_, _ = owner.Exec(context.Background(), "DROP ROLE "+role)
	})
	for _, stmt := range []string{
		"CREATE ROLE " + role + " NOLOGIN",
		"GRANT " + role + " TO CURRENT_USER",
		"GRANT USAGE ON SCHEMA " + schema + " TO " + role,
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA " + schema + " TO " + role,
	} {
		if _, err := owner.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	cfg := owner.Config().Copy()
	cfg.ConnConfig.RuntimeParams["role"] = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestMigrateUpOnRuntimeRoleOnlyConfirmsTheSchema(t *testing.T) {
	owner := pgtest.Open(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, owner); err != nil {
		t.Fatal(err)
	}
	runtime := runtimeRolePool(t, owner)

	if err := db.MigrateUp(ctx, runtime); err != nil {
		t.Fatalf("current schema on a runtime role: %v", err)
	}
	if _, err := runtime.Exec(ctx, "CREATE TABLE runtime_role_ddl_probe (id int)"); err == nil {
		t.Fatal("runtime role created a table")
	}

	// Forget the newest migration: the owner would re-apply it; the runtime
	// role must refuse rather than attempt DDL it cannot run.
	if _, err := owner.Exec(ctx, "DELETE FROM goose_db_version WHERE version_id = (SELECT max(version_id) FROM goose_db_version)"); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, runtime); !errors.Is(err, db.ErrMigrationsPendingForRuntimeRole) {
		t.Fatalf("pending migration on a runtime role: err = %v, want ErrMigrationsPendingForRuntimeRole", err)
	}
}
