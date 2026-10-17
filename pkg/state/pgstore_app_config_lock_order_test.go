//go:build !no_pg

// adr: 570
package state_test

import (
	"context"
	"encoding/json"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// App UPDATE triggers lock the account, while usage event foreign keys lock
// account then app. A config writer waiting for that parent must leave the app
// available to the usage writer, including when it records timeline activity.
func TestPgAppConfigLocksAccountBeforeApp(t *testing.T) {
	for _, activity := range []bool{false, true} {
		name := "ordinary"
		if activity {
			name = "activity"
		}
		t.Run(name, func(t *testing.T) {
			pool := pgtest.OpenMigrated(t)
			store := state.NewPgStore(pool)
			account, err := store.CreateAccount(t.Context(), "config-lock-order@example.test", api.PlanScale)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "config-lock-order", Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			streaming := true
			if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{StreamingEnabled: &streaming, SetStreamingEnabled: true}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			blocker, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
			if _, err := sqlc.New().LockTrafficPolicyAccount(ctx, blocker, pgtype.UUID{Bytes: uuid.MustParse(account.ID), Valid: true}); err != nil {
				t.Fatal(err)
			}
			var blockerPID int
			if err := blocker.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID); err != nil {
				t.Fatal(err)
			}
			prefixes := []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}
			params := state.UpdateAppParams{EgressAllowlist: &prefixes, SetEgressAllowlist: true}
			result := make(chan error, 1)
			go func() {
				if activity {
					_, _, err := store.UpdateAppWithActivity(ctx, app.ID, params, state.OrgActivity{},
						func(state.App, state.App) (json.RawMessage, bool, error) { return nil, false, nil })
					result <- err
				} else {
					_, err := store.UpdateApp(ctx, app.ID, params)
					result <- err
				}
			}()
			defer func() {
				cancel()
				_ = blocker.Rollback(context.WithoutCancel(ctx))
			}()
			// Observe the database wait instead of assuming the writer started
			// after a sleep. The fixture has its own cloned database and account.
			for {
				var waiting bool
				if err := pool.QueryRow(ctx, `SELECT EXISTS (
					SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, blockerPID).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case err := <-result:
					t.Fatalf("config did not wait for its account: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(5 * time.Millisecond):
				}
			}
			if _, err := blocker.Exec(ctx, `SELECT id FROM apps WHERE id=$1 FOR UPDATE NOWAIT`, app.ID); err != nil {
				t.Fatalf("account waiter already holds the app; usage can deadlock: %v", err)
			}
			if err := blocker.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				if err != nil {
					t.Fatalf("config after account release: %v", err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			updated, err := store.AppByID(ctx, app.ID)
			if err != nil || len(updated.EgressAllowlist) != 1 || updated.EgressAllowlist[0] != prefixes[0] {
				t.Fatalf("accepted config did not persist its egress grant: %+v %v", updated.EgressAllowlist, err)
			}
		})
	}
}
