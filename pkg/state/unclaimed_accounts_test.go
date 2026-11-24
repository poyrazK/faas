package state_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
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
	state.AccountFlowStore
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
					Unclaimed: &state.UnclaimedAccountParams{ClaimTokenSHA256: claimHash(token), ExpiresAt: expires, CreatedIP: "203.0.113.9", UserAgent: "agent", LiveMax: 100, PerAddress: 100, PerNetwork: 100},
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
				Unclaimed: &state.UnclaimedAccountParams{ClaimTokenSHA256: claimHash("a"), ExpiresAt: now.Add(time.Hour), LiveMax: 100, PerAddress: 100, PerNetwork: 100},
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
			ids, err := s.ListExpiredUnclaimedAccounts(ctx, now.Add(time.Minute), state.UnclaimedCursor{}, 10)
			if err != nil || len(ids) != 1 || ids[0].AccountID != due {
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
			if ids, _ := s.ListExpiredUnclaimedAccounts(ctx, now.Add(time.Hour), state.UnclaimedCursor{}, 10); len(ids) != 0 {
				t.Fatalf("expired list after sweep = %v", ids)
			}
			if _, err := s.UnclaimedAccountByID(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("missing row err = %v", err)
			}
		})
	}
}

// The caps are enforced in the creating transaction: per address, per /24,
// and across the platform; an over-cap request creates no account.
func TestUnclaimedAccountCaps(t *testing.T) {
	for name, open := range unclaimedBackends() {
		t.Run(name, func(t *testing.T) {
			s := open(t)
			ctx := t.Context()
			n := 0
			create := func(ip string, p state.UnclaimedAccountParams) error {
				n++
				p.ClaimTokenSHA256 = claimHash(name + ip + string(rune('a'+n)))
				p.ExpiresAt = time.Now().Add(time.Hour)
				p.CreatedIP = ip
				_, err := s.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{
					Email: fmt.Sprintf("anon-%s-%d@%s", name, n, api.UnclaimedEmailDomain), Plan: api.PlanFree, Unclaimed: &p,
				})
				return err
			}
			reason := func(err error) string {
				var limit *state.UnclaimedLimitError
				if errors.As(err, &limit) {
					return limit.Reason
				}
				return fmt.Sprint(err)
			}
			caps := state.UnclaimedAccountParams{LiveMax: 4, PerAddress: 2, PerNetwork: 3}
			for _, ip := range []string{"198.51.100.1", "198.51.100.1"} {
				if err := create(ip, caps); err != nil {
					t.Fatalf("within caps: %v", err)
				}
			}
			if got := reason(create("198.51.100.1", caps)); got != state.UnclaimedLimitAddress {
				t.Fatalf("third from one address = %s, want %s", got, state.UnclaimedLimitAddress)
			}
			if err := create("198.51.100.2", caps); err != nil {
				t.Fatalf("other address in the network: %v", err)
			}
			if got := reason(create("198.51.100.3", caps)); got != state.UnclaimedLimitNetwork {
				t.Fatalf("fourth from one /24 = %s, want %s", got, state.UnclaimedLimitNetwork)
			}
			if err := create("203.0.113.1", caps); err != nil {
				t.Fatalf("other network: %v", err)
			}
			if got := reason(create("192.0.2.1", caps)); got != state.UnclaimedLimitLive {
				t.Fatalf("fifth live account = %s, want %s", got, state.UnclaimedLimitLive)
			}
			if live, _ := s.CountLiveUnclaimedAccounts(ctx, time.Now()); live != 4 {
				t.Fatalf("live = %d, want 4: a refused request must not create an account", live)
			}
			if got := reason(create("192.0.2.2", state.UnclaimedAccountParams{LiveMax: 100})); got != state.UnclaimedLimitAddress {
				t.Fatalf("zero per-address cap = %s, want it to refuse", got)
			}
		})
	}
}

// The sweep pages past rows it cannot finish: every due row is reached in
// (expiry, account) order, page by page.
func TestUnclaimedSweepPages(t *testing.T) {
	for name, open := range unclaimedBackends() {
		t.Run(name, func(t *testing.T) {
			s := open(t)
			ctx := t.Context()
			now := time.Now().UTC()
			for i := range 5 {
				if _, err := s.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{
					Email: fmt.Sprintf("anon-page-%d@%s", i, api.UnclaimedEmailDomain), Plan: api.PlanFree,
					Unclaimed: &state.UnclaimedAccountParams{
						ClaimTokenSHA256: claimHash(fmt.Sprintf("page-%d", i)), ExpiresAt: now.Add(time.Duration(i%2+1) * time.Second),
						LiveMax: 100, PerAddress: 100, PerNetwork: 100,
					},
				}); err != nil {
					t.Fatal(err)
				}
			}
			seen := map[string]bool{}
			var cursor state.UnclaimedCursor
			for page := 0; ; page++ {
				rows, err := s.ListExpiredUnclaimedAccounts(ctx, now.Add(time.Minute), cursor, 2)
				if err != nil {
					t.Fatal(err)
				}
				if len(rows) == 0 {
					break
				}
				if page > 5 {
					t.Fatal("paging does not end")
				}
				for _, r := range rows {
					if seen[r.AccountID] {
						t.Fatalf("row %s listed twice", r.AccountID)
					}
					seen[r.AccountID] = true
				}
				cursor = rows[len(rows)-1]
			}
			if len(seen) != 5 {
				t.Fatalf("paged through %d rows, want 5", len(seen))
			}
		})
	}
}

// A claim confirmation is issued only while the claim link names a live
// account; a rotated link or an expired account issues nothing.
func TestIssueClaimEmailChange(t *testing.T) {
	for name, open := range unclaimedBackends() {
		t.Run(name, func(t *testing.T) {
			s := open(t)
			ctx := t.Context()
			now := time.Now().UTC()
			res, err := s.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{
				Email: "anon-issue@" + api.UnclaimedEmailDomain, Plan: api.PlanFree,
				Unclaimed: &state.UnclaimedAccountParams{ClaimTokenSHA256: claimHash("issue"), ExpiresAt: now.Add(time.Hour), LiveMax: 100, PerAddress: 100, PerNetwork: 100},
			})
			if err != nil {
				t.Fatal(err)
			}
			id := res.Account.ID
			got, err := s.IssueClaimEmailChange(ctx, claimHash("issue"), claimHash("change-1"), "first@example.com", now.Add(time.Hour), now)
			if err != nil || got != id {
				t.Fatalf("issue = %q %v, want %s", got, err, id)
			}
			if err := s.RotateUnclaimedClaimToken(ctx, id, claimHash("issue-2")); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ConsumeEmailChange(ctx, claimHash("change-1")); err == nil {
				t.Fatal("rotation left the mailed confirmation usable")
			}
			if _, err := s.IssueClaimEmailChange(ctx, claimHash("issue"), claimHash("change-2"), "second@example.com", now.Add(time.Hour), now); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("issue through a replaced link err = %v, want ErrNotFound", err)
			}
			if _, err := s.IssueClaimEmailChange(ctx, claimHash("issue-2"), claimHash("change-3"), "third@example.com", now.Add(time.Hour), now.Add(2*time.Hour)); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("issue past the window err = %v, want ErrNotFound", err)
			}
			if _, err := s.MarkUnclaimedAccountExpired(ctx, id, now); err != nil {
				t.Fatal(err)
			}
			if _, err := s.IssueClaimEmailChange(ctx, claimHash("issue-2"), claimHash("change-4"), "fourth@example.com", now.Add(time.Hour), now); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("issue on an expired account err = %v, want ErrNotFound", err)
			}
		})
	}
}
