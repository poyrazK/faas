package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// unverifiedExpiryBackend lists only the Store methods this test calls.
type unverifiedExpiryBackend interface {
	state.UnverifiedAccountExpiryStore
	state.AccountMailThrottleStore
	state.AccountThrottleClearStore
	CreateAccountWithPersonalOrg(ctx context.Context, params state.CreateAccountWithPersonalOrgParams) (state.CreateAccountWithPersonalOrgResult, error)
	CreateApp(ctx context.Context, app state.App) (state.App, error)
}

func unverifiedExpiryBackends() map[string]func(t *testing.T) unverifiedExpiryBackend {
	return map[string]func(t *testing.T) unverifiedExpiryBackend{
		"memory": func(*testing.T) unverifiedExpiryBackend { return state.NewMemStore() },
		"postgres": func(t *testing.T) unverifiedExpiryBackend {
			pool := pgtest.OpenMigrated(t)
			if err := db.MigrateUp(context.Background(), pool); err != nil {
				t.Fatalf("db.MigrateUp: %v", err)
			}
			return state.NewPgStore(pool)
		},
	}
}

// Only active, unverified, free accounts without apps or billing whose clock
// started before the cutoff are listed (ADR 20261110120000).
func TestListExpiredUnverifiedAccounts(t *testing.T) {
	for name, open := range unverifiedExpiryBackends() {
		t.Run(name, func(t *testing.T) {
			s := open(t)
			ctx := t.Context()
			mk := func(email string, unverified bool) string {
				res, err := s.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{
					Email: email, Plan: api.PlanFree, RequireEmailVerification: unverified,
				})
				if err != nil {
					t.Fatal(err)
				}
				return res.Account.ID
			}
			abandoned := mk("expiry-abandoned@example.com", true)
			withApp := mk("expiry-app@example.com", true)
			mk("expiry-verified@example.com", false)
			if _, err := s.CreateApp(ctx, state.App{AccountID: withApp, Slug: "expiry-kept", Status: state.AppActive, RAMMB: 256}); err != nil {
				t.Fatal(err)
			}
			enforced := time.Now().Add(-time.Hour)
			if ids, err := s.ListExpiredUnverifiedAccounts(ctx, enforced, time.Now().Add(-time.Minute), 10); err != nil || len(ids) != 0 {
				t.Fatalf("before the cutoff = %v %v, want none", ids, err)
			}
			ids, err := s.ListExpiredUnverifiedAccounts(ctx, enforced, time.Now().Add(time.Minute), 10)
			if err != nil || len(ids) != 1 || ids[0] != abandoned {
				t.Fatalf("after the cutoff = %v %v, want only the abandoned account", ids, err)
			}
		})
	}
}

// A cleared counter starts a fresh window.
func TestClearAccountMailSlot(t *testing.T) {
	for name, open := range unverifiedExpiryBackends() {
		t.Run(name, func(t *testing.T) {
			s := open(t)
			ctx := t.Context()
			res, err := s.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "clear-slot@example.com", Plan: api.PlanFree})
			if err != nil {
				t.Fatal(err)
			}
			limit := state.MailSlotLimit{Window: time.Hour, MaxPerWindow: 1}
			if ok, err := s.TakeAccountMailSlot(ctx, res.Account.ID, "totp_attempt", limit); err != nil || !ok {
				t.Fatalf("first attempt = %v %v", ok, err)
			}
			if ok, _ := s.TakeAccountMailSlot(ctx, res.Account.ID, "totp_attempt", limit); ok {
				t.Fatal("second attempt allowed past the limit")
			}
			if err := s.ClearAccountMailSlot(ctx, res.Account.ID, "totp_attempt"); err != nil {
				t.Fatal(err)
			}
			if ok, err := s.TakeAccountMailSlot(ctx, res.Account.ID, "totp_attempt", limit); err != nil || !ok {
				t.Fatalf("attempt after clear = %v %v", ok, err)
			}
		})
	}
}
