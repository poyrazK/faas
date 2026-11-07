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

// accountFlowBackend lists only the Store methods these tests call
// (teststorecheck: never embed state.Store in a test interface).
type accountFlowBackend interface {
	state.AccountFlowStore
	CreateAccount(ctx context.Context, email string, plan api.Plan) (state.Account, error)
	AccountByID(ctx context.Context, id string) (state.Account, error)
	CreateSession(ctx context.Context, id, accountID, issuedIP, issuedUA string) (state.Session, error)
	UpsertOAuthLink(ctx context.Context, accountID, provider, providerSubject, email string, emailVerified bool) error
}

func accountFlowBackends(t *testing.T) map[string]func(t *testing.T) accountFlowBackend {
	return map[string]func(t *testing.T) accountFlowBackend{
		"memory": func(*testing.T) accountFlowBackend { return state.NewMemStore() },
		"postgres": func(t *testing.T) accountFlowBackend {
			pool := pgtest.OpenMigrated(t)
			if err := db.MigrateUp(context.Background(), pool); err != nil {
				t.Fatalf("db.MigrateUp: %v", err)
			}
			return state.NewPgStore(pool)
		},
	}
}

func flowToken(s string) []byte { sum := sha256.Sum256([]byte(s)); return sum[:] }

// Email changes apply once, only to the address they were requested from,
// never onto an address another account holds, and the newest request
// replaces an older one (ADR 20261107103000).
func TestAccountFlowEmailChange(t *testing.T) {
	for name, open := range accountFlowBackends(t) {
		t.Run(name, func(t *testing.T) {
			s := open(t)
			ctx := t.Context()
			a, err := s.CreateAccount(ctx, "flow-a@example.com", api.PlanFree)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateAccount(ctx, "flow-taken@example.com", api.PlanFree); err != nil {
				t.Fatal(err)
			}
			exp := time.Now().Add(time.Hour)
			if err := s.IssueEmailChange(ctx, flowToken("old"), a.ID, "flow-old@example.com", exp); err != nil {
				t.Fatal(err)
			}
			if err := s.IssueEmailChange(ctx, flowToken("taken"), a.ID, "flow-taken@example.com", exp); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ConsumeEmailChange(ctx, flowToken("old")); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("replaced request consumed: %v", err)
			}
			if _, err := s.ConsumeEmailChange(ctx, flowToken("taken")); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("change onto a taken address = %v, want ErrConflict", err)
			}
			if err := s.IssueEmailChange(ctx, flowToken("new"), a.ID, "flow-new@example.com", exp); err != nil {
				t.Fatal(err)
			}
			change, err := s.ConsumeEmailChange(ctx, flowToken("new"))
			if err != nil || change.AccountID != a.ID || change.OldEmail != "flow-a@example.com" || change.NewEmail != "flow-new@example.com" {
				t.Fatalf("consume = %+v, %v", change, err)
			}
			moved, err := s.AccountByID(ctx, a.ID)
			if err != nil || moved.Email != "flow-new@example.com" || !moved.EmailVerified() {
				t.Fatalf("account after change = %+v, %v", moved, err)
			}
			if _, err := s.ConsumeEmailChange(ctx, flowToken("new")); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("second consume = %v, want ErrNotFound", err)
			}
			if err := s.IssueEmailChange(ctx, flowToken("expired"), a.ID, "flow-late@example.com", time.Now().Add(time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			time.Sleep(5 * time.Millisecond)
			if _, err := s.ConsumeEmailChange(ctx, flowToken("expired")); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("expired consume = %v, want ErrNotFound", err)
			}
		})
	}
}

// Display names, linked providers and recent sign-ins round-trip.
func TestAccountFlowProfileAndLinks(t *testing.T) {
	for name, open := range accountFlowBackends(t) {
		t.Run(name, func(t *testing.T) {
			s := open(t)
			ctx := t.Context()
			a, err := s.CreateAccount(ctx, "flow-links@example.com", api.PlanFree)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.SetAccountDisplayName(ctx, a.ID, "Grace Hopper"); err != nil {
				t.Fatal(err)
			}
			if got, err := s.AccountDisplayName(ctx, a.ID); err != nil || got != "Grace Hopper" {
				t.Fatalf("display name = %q, %v", got, err)
			}
			for _, p := range []string{"google", "github"} {
				if err := s.UpsertOAuthLink(ctx, a.ID, p, p+"-flow", a.Email, true); err != nil {
					t.Fatal(err)
				}
			}
			if removed, err := s.DeleteOAuthLink(ctx, a.ID, "github"); err != nil || !removed {
				t.Fatalf("delete link = %v, %v", removed, err)
			}
			links, err := s.ListOAuthLinks(ctx, a.ID)
			if err != nil || len(links) != 1 || links[0].Provider != "google" {
				t.Fatalf("links = %+v, %v", links, err)
			}
			if _, err := s.CreateSession(ctx, "22222222-2222-2222-2222-222222222222", a.ID, "198.51.100.4", "flow-ua"); err != nil {
				t.Fatal(err)
			}
			recent, err := s.RecentSignIns(ctx, a.ID, time.Now().Add(-time.Hour))
			if err != nil || len(recent) != 1 || recent[0].IssuedIP != "198.51.100.4" || recent[0].IssuedUA != "flow-ua" {
				t.Fatalf("recent sign-ins = %+v, %v", recent, err)
			}
			if err := s.ReplaceRecoveryCodes(ctx, a.ID, [][]byte{flowToken("code")}); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("replace codes without MFA = %v, want ErrNotFound", err)
			}
		})
	}
}
