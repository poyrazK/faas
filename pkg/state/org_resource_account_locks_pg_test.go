//go:build !no_pg

// adr: 202610171904
package state_test

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresOrgResourceAccountLocks(t *testing.T) {
	for _, kind := range []string{"personal", "adopted", "erased"} {
		t.Run(kind, func(t *testing.T) {
			ctx := t.Context()
			pool := pgtest.OpenMigrated(t)
			store := state.NewPgStore(pool)
			f := setupOffboarding(t, store)
			app := f.app
			ownerTable, ownerID, wantPlan := "orgs", f.org.ID, api.PlanPro
			if kind == "personal" {
				var err error
				app, err = store.CreateApp(ctx, state.App{AccountID: f.actor.ID, Slug: "personal-lock"})
				if err != nil {
					t.Fatal(err)
				}
				ownerTable, ownerID, wantPlan = "accounts", f.actor.ID, api.PlanFree
			} else {
				if err := store.UpdateAccountProviderCustomerID(ctx, f.org.ID, "cus_owner_lock"); err != nil {
					t.Fatal(err)
				}
				if err := store.UpdateAccountStripeSubscriptionItem(ctx, f.org.ID, "si_owner_lock"); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.UpdateAccountPlan(ctx, f.actor.ID, api.PlanFree); err != nil {
				t.Fatal(err)
			}
			if kind == "erased" {
				if err := store.RemoveOrgMember(ctx, f.org.ID, f.actor.ID); err != nil {
					t.Fatal(err)
				}
				if err := store.MarkAccountDeletionPending(ctx, f.actor.ID); err != nil {
					t.Fatal(err)
				}
				if err := store.DeleteAccount(ctx, f.actor.ID); err != nil {
					t.Fatal(err)
				}
			}
			for _, exclusive := range []bool{false, true} {
				for _, fast := range []bool{false, true} {
					func() {
						tx, err := pool.Begin(ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer func() { _ = tx.Rollback(ctx) }()
						var plan string
						if err := tx.QueryRow(ctx, `SELECT plan FROM lock_app_resource_account($1,$2,$3)`, app.ID, exclusive, fast).Scan(&plan); err != nil || plan != string(wantPlan) {
							t.Fatalf("billing policy: %s %v", plan, err)
						}
						other, err := pool.Begin(ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer func() { _ = other.Rollback(ctx) }()
						var locked string
						if kind == "adopted" {
							if err := other.QueryRow(ctx, `SELECT id FROM accounts WHERE id=$1 FOR UPDATE NOWAIT`, f.actor.ID).Scan(&locked); err != nil {
								t.Fatalf("company operation locked creator billing: %v", err)
							}
						}
						statement := `SELECT id FROM orgs WHERE id=$1 FOR UPDATE NOWAIT`
						if ownerTable == "accounts" {
							statement = `SELECT id FROM accounts WHERE id=$1 FOR UPDATE NOWAIT`
						}
						err = other.QueryRow(ctx, statement, ownerID).Scan(&locked)
						var pgErr *pgconn.PgError
						if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
							t.Fatalf("billing policy update escaped transaction fence: %v", err)
						}
					}()
				}
			}
		})
	}
}
