package state_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// The shared publish counter is all or nothing and per account: two
// publishers (two processes) together get the burst once (event-driven hunt
// #16).
func TestPgEventPublishRateIsSharedAndAllOrNothing(t *testing.T) {
	s, ctx := pgStore(t)
	acct, err := s.CreateAccount(ctx, "publish-rate-"+uuid.NewString()+"@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateAccount(ctx, "publish-rate-"+uuid.NewString()+"@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	// A slow refill keeps the test clear of the clock.
	limits := api.EventPublishLimits{PerSecond: 1, Burst: 10}
	check := func(id string, n int, want bool) {
		t.Helper()
		got, err := s.ConsumeEventPublishRate(ctx, id, api.PlanFree, limits, n)
		if err != nil || got != want {
			t.Fatalf("consume %d for %s = %v, %v; want %v", n, id, got, err, want)
		}
	}
	check(acct.ID, 6, true)  // creates the counter
	check(acct.ID, 6, false) // 4 left: all or nothing
	check(acct.ID, 4, true)
	check(acct.ID, 1, false)
	check(other.ID, 10, true) // another account has its own counter
	check(acct.ID, 11, false) // more than the burst never fits
	if got, err := s.ConsumeEventPublishRate(ctx, acct.ID, api.PlanFree, api.EventPublishLimits{}, 1000); err != nil || !got {
		t.Fatalf("zero rate = %v, %v; want admitted", got, err)
	}
}
