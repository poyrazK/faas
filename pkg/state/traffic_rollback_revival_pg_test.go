//go:build !no_pg

// adr: 570
package state

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func pgTrafficRollbackIntent(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var operations string
	err := pool.QueryRow(t.Context(), `SELECT md5(coalesce((SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM deployment_rollback_operations t),'null')::text)`).Scan(&operations)
	if err != nil {
		t.Fatal(err)
	}
	return pgTrafficRevisionIntent(t, pool) + operations
}

func TestPgTrafficRollbackRevivalPublication(t *testing.T) {
	for _, mode := range trafficRollbackRevivalModes {
		t.Run(mode, func(t *testing.T) {
			store, pool, account, app := trafficHostPGFixture(t)
			testTrafficRollbackRevival(t, store, account, app, mode, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() string { return pgTrafficRollbackIntent(t, pool) })
		})
	}
}
