//go:build !no_pg

package state_test

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectWorkerReleasePinsPG(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	testProjectWorkerReleasePinsOnPool(t, pool)
}

// Four concurrent worker deliveries on a two-connection pool: a delivery that
// held its version snapshot while waiting for a second connection to capture
// the lineage pin deadlocked the pool (nightly state shard 1 hung for 21
// minutes on a four-CPU runner, where the default pool has four).
func TestProjectWorkerReleasePinsPGSmallPool(t *testing.T) {
	cfg := pgtest.OpenMigrated(t).Config()
	cfg.MaxConns, cfg.MinConns = 2, 0
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	testProjectWorkerReleasePinsOnPool(t, pool)
}

func testProjectWorkerReleasePinsOnPool(t *testing.T, pool *pgxpool.Pool) {
	testProjectWorkerReleasePins(t, state.NewPgStore(pool), func() state.Store { return state.NewPgStore(pool) }, func(id string) {
		if _, err := pool.Exec(t.Context(), "UPDATE project_release_sets SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1 AND NOT active", id); err != nil {
			t.Fatal(err)
		}
	})
}
