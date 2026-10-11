package main

// adr: 791
import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestLegacyJobClaimStoreRejectsIncompleteBootstrapDependencies(t *testing.T) {
	for _, name := range []string{"both", "opener", "constructor"} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			deps := runDeps{}
			if name == "opener" {
				deps.openStore = func(*pgxpool.Pool) *state.PgStore { calls++; return nil }
			}
			if name == "constructor" {
				deps.openDB = func(context.Context, string) (*pgxpool.Pool, error) { calls++; return nil, nil }
			}
			pool, store, node, err := loadLegacyJobClaimStore(t.Context(), deps, "postgres://unused.invalid/test")
			if err == nil || pool != nil || store != nil || node != "" || calls != 0 {
				t.Fatalf("incomplete dependencies opened or returned claim authority: pool=%v store=%v node=%q calls=%d err=%v", pool, store, node, calls, err)
			}
		})
	}
}
