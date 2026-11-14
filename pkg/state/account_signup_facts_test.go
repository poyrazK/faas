package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261113160000 — the first sign-up record wins, free text is trimmed to
// the column bounds, and reduction clears only the personal fields.
func exerciseAccountSignupFacts(t *testing.T, store contractTestStore) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "signup-facts-"+time.Now().Format("150405.000000")+"@example.com", api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if _, err := store.AccountSignupFacts(ctx, acct.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("AccountSignupFacts before record = %v, want ErrNotFound", err)
	}
	created := time.Now().UTC().Truncate(time.Microsecond).Add(-100 * 24 * time.Hour)
	facts := state.AccountSignupFacts{
		AccountID: acct.ID, Method: "password", SignupIP: "::ffff:203.0.113.9",
		UserAgent: strings.Repeat("u", 600), Country: "DE", ASN: 64500, ASNOrg: "Example Net",
		EmailDomain: "example.com", CreatedAt: created,
	}
	if err := store.RecordAccountSignupFacts(ctx, facts); err != nil {
		t.Fatalf("RecordAccountSignupFacts: %v", err)
	}
	second := facts
	second.Method, second.SignupIP = "github", "198.51.100.1"
	if err := store.RecordAccountSignupFacts(ctx, second); err != nil {
		t.Fatalf("second RecordAccountSignupFacts: %v", err)
	}
	got, err := store.AccountSignupFacts(ctx, acct.ID)
	if err != nil {
		t.Fatalf("AccountSignupFacts: %v", err)
	}
	if got.Method != "password" || got.SignupIP != "203.0.113.9" || len(got.UserAgent) != 512 ||
		got.Country != "DE" || got.ASN != 64500 || got.ASNOrg != "Example Net" ||
		got.EmailDomain != "example.com" || got.DisposableEmail || !got.CreatedAt.Equal(created) || got.ReducedAt != nil {
		t.Fatalf("facts = %+v; want the first record, address unmapped, user agent trimmed to 512", got)
	}

	now := created.Add(100 * 24 * time.Hour)
	if n, err := store.ReduceAccountSignupFacts(ctx, created, now, 100); err != nil || n != 0 {
		t.Fatalf("reduce with cutoff at creation = %d, %v; want nothing reduced", n, err)
	}
	if n, err := store.ReduceAccountSignupFacts(ctx, now.Add(-api.SignupFactsPersonalRetention), now, 100); err != nil || n < 1 {
		t.Fatalf("reduce after retention = %d, %v; want this record reduced", n, err)
	}
	got, err = store.AccountSignupFacts(ctx, acct.ID)
	if err != nil {
		t.Fatalf("AccountSignupFacts after reduce: %v", err)
	}
	if got.SignupIP != "" || got.UserAgent != "" || got.ReducedAt == nil ||
		got.Country != "DE" || got.ASN != 64500 || got.EmailDomain != "example.com" || got.Method != "password" {
		t.Fatalf("reduced facts = %+v; want address and user agent cleared, the rest kept", got)
	}

	for _, bad := range []state.AccountSignupFacts{
		{AccountID: acct.ID, Method: "Password!", EmailDomain: "x.io", CreatedAt: now},
		{AccountID: acct.ID, Method: "password", SignupIP: "not-an-ip", EmailDomain: "x.io", CreatedAt: now},
		{AccountID: acct.ID, Method: "password", EmailDomain: "x.io"},
	} {
		if err := store.RecordAccountSignupFacts(ctx, bad); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("RecordAccountSignupFacts(%+v) = %v, want ErrInvalidArgument", bad, err)
		}
	}
}

func TestMemStoreAccountSignupFacts(t *testing.T) {
	exerciseAccountSignupFacts(t, state.NewMemStore())
}

func TestPgStoreAccountSignupFacts(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	exerciseAccountSignupFacts(t, state.NewPgStore(pool))
}
