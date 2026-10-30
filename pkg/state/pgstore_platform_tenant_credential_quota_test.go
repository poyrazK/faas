//go:build !no_pg

package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/state"
)

// pgStoreAdoptingOrgs returns a store, its pool, and a hook that adopts an
// organization's billing, which the provider confirmation path sets and these
// tests do not drive.
func pgStoreAdoptingOrgs(t *testing.T) (*state.PgStore, *pgxpool.Pool, func(orgID string)) {
	t.Helper()
	store, pool, ctx := pgStoreWithPool(t)
	return store, pool, func(orgID string) {
		if _, err := pool.Exec(ctx, `update orgs set resource_ownership_enabled = true where id = $1`, orgID); err != nil {
			t.Fatal(err)
		}
	}
}

// pgRaceGate holds the gated rows on a connection of its own and, on open,
// waits on another until every racer that holds a pool connection is
// queued on a lock. Without it, opening the racers' connections staggers
// them so they seldom overlap.
func pgRaceGate(pool *pgxpool.Pool) raceGate {
	return func(t *testing.T, tenantIDs []string, racers int) func() {
		t.Helper()
		ctx := t.Context()
		connect := func() *pgx.Conn {
			t.Helper()
			conn, err := pgx.ConnectConfig(ctx, pool.Config().ConnConfig.Copy())
			if err != nil {
				t.Fatal(err)
			}
			// t.Context is canceled before cleanups run.
			t.Cleanup(func() { _ = conn.Close(context.WithoutCancel(ctx)) })
			return conn
		}
		holder, watcher := connect(), connect()
		tx, err := holder.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `select 1 from platform_tenants where id = any($1::uuid[]) for update`, tenantIDs); err != nil {
			t.Fatal(err)
		}
		queued := min(racers, int(pool.Config().MaxConns))
		return func() {
			defer func() { _ = tx.Rollback(ctx) }()
			for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(10 * time.Millisecond) {
				var waiting int
				if err := watcher.QueryRow(ctx, `select count(*) from pg_stat_activity
					where datname = current_database() and wait_event_type = 'Lock'`).Scan(&waiting); err != nil {
					t.Errorf("count queued racers: %v", err)
					return
				}
				if waiting >= queued {
					return
				}
				if time.Now().After(deadline) {
					t.Errorf("%d of %d racers queued behind the gate", waiting, queued)
					return
				}
			}
		}
	}
}

// adr: 202610171904
func TestPgPlatformTenantKeysSpendTheBillingSubjectsAllowance(t *testing.T) {
	store, _, adopt := pgStoreAdoptingOrgs(t)
	platformTenantKeySubjectContract(t, store, adopt)
}

func TestPgPlatformTenantKeysAndMemberKeysShareOneAllowance(t *testing.T) {
	store, pool, adopt := pgStoreAdoptingOrgs(t)
	platformTenantKeySubjectRaceContract(t, store, adopt, pgRaceGate(pool))
}
