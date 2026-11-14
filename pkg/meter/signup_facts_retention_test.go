package meter_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261113160000 — the sign-up address and user agent are cleared after
// api.SignupFactsPersonalRetention; younger records keep them.
func TestSignupFactsRetentionOnce(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	now := time.Unix(1_800_000_000, 0).UTC()
	record := func(email string, age time.Duration) string {
		acct, err := store.CreateAccount(ctx, email, api.PlanFree)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.RecordAccountSignupFacts(ctx, state.AccountSignupFacts{
			AccountID: acct.ID, Method: "password", SignupIP: "203.0.113.5", UserAgent: "ua",
			Country: "FR", EmailDomain: "example.com", CreatedAt: now.Add(-age),
		}); err != nil {
			t.Fatal(err)
		}
		return acct.ID
	}
	old := record("old@example.com", api.SignupFactsPersonalRetention+time.Hour)
	young := record("young@example.com", api.SignupFactsPersonalRetention-time.Hour)

	n, err := meter.SignupFactsRetentionOnce(ctx, store, now)
	if err != nil || n != 1 {
		t.Fatalf("SignupFactsRetentionOnce = %d, %v; want 1 reduced", n, err)
	}
	if f, _ := store.AccountSignupFacts(ctx, old); f.SignupIP != "" || f.UserAgent != "" || f.ReducedAt == nil || f.Country != "FR" {
		t.Fatalf("old record = %+v; want address and user agent cleared, country kept", f)
	}
	if f, _ := store.AccountSignupFacts(ctx, young); f.SignupIP != "203.0.113.5" || f.ReducedAt != nil {
		t.Fatalf("young record = %+v; want untouched", f)
	}
}
