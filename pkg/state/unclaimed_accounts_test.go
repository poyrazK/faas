package state_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

type unclaimedBackend interface {
	state.UnclaimedAccountStore
	state.SignupCountStore
	CreateAccountWithPersonalOrg(ctx context.Context, params state.CreateAccountWithPersonalOrgParams) (state.CreateAccountWithPersonalOrgResult, error)
}

func unclaimedBackends() map[string]func(t *testing.T) unclaimedBackend {
	return map[string]func(t *testing.T) unclaimedBackend{
		"memory": func(*testing.T) unclaimedBackend { return state.NewMemStore() },
		"postgres": func(t *testing.T) unclaimedBackend {
			pool := pgtest.OpenMigrated(t)
			if err := db.MigrateUp(context.Background(), pool); err != nil {
				t.Fatalf("db.MigrateUp: %v", err)
			}
			return state.NewPgStore(pool)
		},
	}
}

func claimHash(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

// An anonymous deploy's account and its unclaimed row are created together;
// the row moves to claimed or expired once, and only open rows are listed,
// counted and re-linked (ADR-20261124080000).
func TestUnclaimedAccountLifecycle(t *testing.T) {
	for name, open := range unclaimedBackends() {
		t.Run(name, func(t *testing.T) {
			s := open(t)
			ctx := t.Context()
			now := time.Now().UTC()
			mk := func(email, token string, expires time.Time) string {
				res, err := s.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{
					Email: email, Plan: api.PlanFree,
					Unclaimed: &state.UnclaimedAccountParams{ClaimTokenSHA256: claimHash(token), ExpiresAt: expires, CreatedIP: "203.0.113.9", UserAgent: "agent"},
				})
				if err != nil {
					t.Fatal(err)
				}
				return res.Account.ID
			}
			before, err := s.CountVerifiedAccountsCreatedSince(ctx, now.Add(-time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			due := mk("anon-a@"+api.UnclaimedEmailDomain, "a", now.Add(time.Second))
			live := mk("anon-b@"+api.UnclaimedEmailDomain, "b", now.Add(72*time.Hour))

			// A duplicate claim token never creates a second account.
			if _, err := s.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{
				Email: "anon-c@" + api.UnclaimedEmailDomain, Plan: api.PlanFree,
				Unclaimed: &state.UnclaimedAccountParams{ClaimTokenSHA256: claimHash("a"), ExpiresAt: now.Add(time.Hour)},
			}); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("duplicate token err = %v, want ErrConflict", err)
			}
			if after, _ := s.CountVerifiedAccountsCreatedSince(ctx, now.Add(-time.Hour)); after != before {
				t.Fatalf("unclaimed accounts counted toward sign-ups: %d -> %d", before, after)
			}

			row, err := s.UnclaimedAccountByClaimToken(ctx, claimHash("b"))
			if err != nil || row.AccountID != live || !row.Open() || row.CreatedIP != "203.0.113.9" {
				t.Fatalf("by token = %+v (err %v)", row, err)
			}
			if n, _ := s.CountLiveUnclaimedAccounts(ctx, now.Add(time.Minute)); n != 1 {
				t.Fatalf("live = %d, want 1", n)
			}
			ids, err := s.ListExpiredUnclaimedAccounts(ctx, now.Add(time.Minute), 10)
			if err != nil || len(ids) != 1 || ids[0] != due {
				t.Fatalf("expired = %v (err %v), want [%s]", ids, err, due)
			}

			if err := s.RotateUnclaimedClaimToken(ctx, live, claimHash("b2")); err != nil {
				t.Fatal(err)
			}
			if _, err := s.UnclaimedAccountByClaimToken(ctx, claimHash("b")); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("old token err = %v, want ErrNotFound", err)
			}

			if ok, err := s.MarkUnclaimedAccountClaimed(ctx, live, now); err != nil || !ok {
				t.Fatalf("claim = %v %v", ok, err)
			}
			if ok, _ := s.MarkUnclaimedAccountExpired(ctx, live, now); ok {
				t.Fatal("a claimed account expired")
			}
			if err := s.RotateUnclaimedClaimToken(ctx, live, claimHash("b3")); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("rotate after claim err = %v, want ErrNotFound", err)
			}
			if after, _ := s.CountVerifiedAccountsCreatedSince(ctx, now.Add(-time.Hour)); after != before+1 {
				t.Fatalf("claimed account not counted: %d -> %d", before, after)
			}
			if ok, err := s.MarkUnclaimedAccountExpired(ctx, due, now); err != nil || !ok {
				t.Fatalf("expire = %v %v", ok, err)
			}
			if ids, _ := s.ListExpiredUnclaimedAccounts(ctx, now.Add(time.Hour), 10); len(ids) != 0 {
				t.Fatalf("expired list after sweep = %v", ids)
			}
			if _, err := s.UnclaimedAccountByID(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("missing row err = %v", err)
			}
		})
	}
}
