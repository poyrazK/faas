package pgtest

// The guard must detect a ledger in a named schema, and only there. Tested
// against a throwaway schema so the test itself never touches public — which
// would poison every other package on the cluster, the very thing the guard
// exists to catch.

import (
	"context"
	"fmt"
	"testing"
)

func TestSchemaHasLedger(t *testing.T) {
	pool := Open(t)
	ctx := context.Background()
	var schema string
	if err := pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}

	if found, err := schemaHasLedger(ctx, pool, schema); err != nil || found {
		t.Fatalf("fresh schema %s: found=%v err=%v, want no ledger", schema, found, err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`CREATE TABLE %s.goose_db_version (id serial)`, schema)); err != nil {
		t.Fatal(err)
	}
	if found, err := schemaHasLedger(ctx, pool, schema); err != nil || !found {
		t.Fatalf("after creating the ledger in %s: found=%v err=%v, want found", schema, found, err)
	}
	// Still nothing in public — this test must not be the thing that poisons it.
	if found, err := schemaHasLedger(ctx, pool, "public"); err != nil || found {
		t.Fatalf("public: found=%v err=%v; a ledger in public means some test migrated the shared schema", found, err)
	}
}
