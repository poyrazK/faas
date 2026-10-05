package financialtest

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// Keep the financial conformance suite separately runnable; it needs real
// migrated Postgres without compiling unrelated state test fixtures.
func financialPostgres(t *testing.T) (*state.PgStore, *pgxpool.Pool, context.Context) {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return state.NewPgStore(pool), pool, t.Context()
}

func financialLocalNode(t *testing.T, ctx context.Context, store *state.PgStore) string {
	t.Helper()
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	return node.ID
}
