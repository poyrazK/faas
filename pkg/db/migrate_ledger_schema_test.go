package db

// The ledger must be pinned to the pool's schema, never resolved through a
// fallback schema. Reproduces the shard-2a poisoning deterministically and in
// isolation: schema A is migrated; schema B is created with search_path=B,A
// (A standing in for a migrated public). Migrating B must populate B — not
// find A's ledger, report "no migrations to run", and leave B empty.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestLedgerTableName(t *testing.T) {
	cases := map[string]struct {
		params map[string]string
		want   string
	}{
		"no search_path (production)": {nil, "goose_db_version"},
		"public first":                {map[string]string{"search_path": "public"}, "goose_db_version"},
		"$user first":                 {map[string]string{"search_path": "$user,public"}, "goose_db_version"},
		"pgtest schema first":         {map[string]string{"search_path": "faas_test_ab12,public"}, `"faas_test_ab12"."goose_db_version"`},
		"quoted schema":               {map[string]string{"search_path": `"odd-name",public`}, `"odd-name"."goose_db_version"`},
	}
	for name, c := range cases {
		if got := ledgerTableName(c.params); got != c.want {
			t.Errorf("%s: ledgerTableName = %q, want %q", name, got, c.want)
		}
	}
}

func TestMigrateUp_LedgerIsPinnedToThePoolSchema(t *testing.T) {
	ctx := context.Background()

	// Schema A: migrated the ordinary way. It plays the role of a migrated
	// public schema.
	poolA := pgtest.Open(t)
	if err := MigrateUp(ctx, poolA); err != nil {
		t.Fatalf("migrate A: %v", err)
	}
	schemaA := firstSchema(t, poolA)

	// Schema B: fresh, with A as its fallback and public last (extensions such
	// as citext live there) — exactly CI's shape when public has been migrated.
	poolB := pgtest.Open(t)
	schemaB := firstSchema(t, poolB)
	cfg := poolB.Config()
	cfg.ConnConfig.RuntimeParams["search_path"] = schemaB + "," + schemaA + ",public"
	poisoned, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer poisoned.Close()

	if err := MigrateUp(ctx, poisoned); err != nil {
		t.Fatalf("migrate B with A on its search_path: %v", err)
	}

	var accountsInB, ledgerInB bool
	if err := poisoned.QueryRow(ctx,
		`SELECT to_regclass($1) IS NOT NULL, to_regclass($2) IS NOT NULL`,
		schemaB+".accounts", schemaB+".goose_db_version").Scan(&accountsInB, &ledgerInB); err != nil {
		t.Fatal(err)
	}
	if !ledgerInB {
		t.Errorf("no ledger in %s: goose resolved %s's ledger through search_path and "+
			"reported \"no migrations to run\"", schemaB, schemaA)
	}
	if !accountsInB {
		t.Errorf("no accounts table in %s: the schema was left empty, which is the "+
			"`relation \"accounts\" does not exist` failure from pg shard 2a", schemaB)
	}
}

func firstSchema(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(), `SELECT current_schema()`).Scan(&s); err != nil {
		t.Fatal(err)
	}
	if s == "" || s == "public" {
		t.Fatalf("pgtest pool is on schema %q; expected an isolated test schema", s)
	}
	return s
}
