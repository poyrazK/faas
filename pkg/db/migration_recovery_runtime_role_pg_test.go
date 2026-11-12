package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/db/migrationsqlc"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// The ledger-recovery writer check admits the faas_runtime group's grants
// (ADR-20261023104734) and nothing wider: a privilege that alters objects or
// can be passed on still makes another role a writer of the schema.
func TestMigrationRecoveryWritersAdmitOnlyTheRuntimeGroup(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DO $$ BEGIN CREATE ROLE faas_runtime NOLOGIN;
EXCEPTION WHEN duplicate_object THEN NULL; END $$`); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42501" {
			t.Skip("test database user cannot create roles")
		}
		t.Fatal(err)
	}
	var schema string
	if err := pool.QueryRow(ctx, "SELECT quote_ident(current_schema())").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	exec := func(sql string) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	writersValid := func() bool {
		t.Helper()
		valid, err := migrationsqlc.New().CheckMigrationRecoveryWriters(ctx, pool)
		if err != nil {
			t.Fatal(err)
		}
		return valid.Valid && valid.Bool
	}
	exec("CREATE TABLE recovery_runtime_probe (id serial PRIMARY KEY, v text)")
	exec("CREATE FUNCTION recovery_private_probe() RETURNS boolean LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog AS 'SELECT true'")
	if !writersValid() {
		t.Fatal("an owner-only schema is not valid")
	}
	// Private trigger functions retain an explicit owner-only ACL after
	// revoking PostgreSQL's default PUBLIC execution permission.
	exec("REVOKE ALL ON FUNCTION recovery_private_probe() FROM PUBLIC")
	if !writersValid() {
		t.Fatal("an explicit owner-only function ACL is rejected")
	}
	exec("GRANT USAGE ON SCHEMA " + schema + " TO faas_runtime")
	exec("GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA " + schema + " TO faas_runtime")
	exec("GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA " + schema + " TO faas_runtime")
	exec("ALTER DEFAULT PRIVILEGES IN SCHEMA " + schema + " GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO faas_runtime")
	exec("ALTER DEFAULT PRIVILEGES IN SCHEMA " + schema + " GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO faas_runtime")
	exec("CREATE TABLE recovery_runtime_probe_later (id bigserial PRIMARY KEY)")
	if !writersValid() {
		t.Fatal("the runtime group's grant set is rejected")
	}
	for _, tc := range []struct{ grant, revoke string }{
		{"GRANT TRUNCATE ON recovery_runtime_probe TO faas_runtime", "REVOKE TRUNCATE ON recovery_runtime_probe FROM faas_runtime"},
		{"GRANT SELECT ON recovery_runtime_probe TO faas_runtime WITH GRANT OPTION", "REVOKE GRANT OPTION FOR SELECT ON recovery_runtime_probe FROM faas_runtime"},
		{"GRANT CREATE ON SCHEMA " + schema + " TO faas_runtime", "REVOKE CREATE ON SCHEMA " + schema + " FROM faas_runtime"},
		{"ALTER DEFAULT PRIVILEGES IN SCHEMA " + schema + " GRANT TRUNCATE ON TABLES TO faas_runtime", "ALTER DEFAULT PRIVILEGES IN SCHEMA " + schema + " REVOKE TRUNCATE ON TABLES FROM faas_runtime"},
		{"GRANT INSERT ON recovery_runtime_probe TO PUBLIC", "REVOKE INSERT ON recovery_runtime_probe FROM PUBLIC"},
		{"GRANT EXECUTE ON FUNCTION recovery_private_probe() TO PUBLIC", "REVOKE EXECUTE ON FUNCTION recovery_private_probe() FROM PUBLIC"},
		{"GRANT EXECUTE ON FUNCTION recovery_private_probe() TO faas_runtime", "REVOKE EXECUTE ON FUNCTION recovery_private_probe() FROM faas_runtime"},
		{"GRANT EXECUTE ON FUNCTION recovery_private_probe() TO faas_runtime WITH GRANT OPTION", "REVOKE EXECUTE ON FUNCTION recovery_private_probe() FROM faas_runtime"},
	} {
		exec(tc.grant)
		if writersValid() {
			t.Errorf("%s: accepted", tc.grant)
		}
		exec(tc.revoke)
		if !writersValid() {
			t.Fatalf("%s: still rejected after revoke", tc.revoke)
		}
	}
}
