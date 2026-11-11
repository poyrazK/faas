// adr: 20261111140000

package apidserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// A complimentary account returns to Free with no spend cap at its end date;
// one without an end date stays; one that subscribed meanwhile keeps its plan.
func TestComplimentaryAccountsExpire(t *testing.T) {
	ctx := context.Background()
	srv, _, store := apidserver.V1AuthTestHarness(t)
	comps, ok := store.(state.ComplimentaryAccountStore)
	if !ok {
		t.Fatal("the test store keeps no complimentary accounts")
	}
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	comp := func(email string, plan api.Plan, until time.Time) state.Account {
		t.Helper()
		acct, err := store.CreateAccount(ctx, email, plan)
		if err != nil {
			t.Fatal(err)
		}
		zero := int64(0)
		if err := store.UpdateAccountOverageCapCents(ctx, acct.ID, &zero); err != nil {
			t.Fatal(err)
		}
		if err := comps.SetComplimentaryAccount(ctx, state.ComplimentaryAccount{AccountID: acct.ID, Plan: plan,
			Reason: "partner_program", GrantedBy: acct.ID, GrantedAt: now.Add(-time.Hour), ExpiresAt: until}); err != nil {
			t.Fatal(err)
		}
		return acct
	}
	dated := comp("dated@example.com", api.PlanPro, now.Add(24*time.Hour))
	forever := comp("forever@example.com", api.PlanScale, time.Time{})
	subscribed := comp("subscribed-later@example.com", api.PlanHobby, now.Add(time.Hour))
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, subscribed.ID, "sub_later"); err != nil {
		t.Fatal(err)
	}

	if n := srv.ExpireComplimentaryAccounts(ctx, now); n != 0 {
		t.Fatalf("before any end date: ended %d", n)
	}
	if n := srv.ExpireComplimentaryAccounts(ctx, now.Add(48*time.Hour)); n != 2 {
		t.Fatalf("past both end dates: ended %d, want 2", n)
	}
	for _, c := range []struct {
		acct    state.Account
		plan    api.Plan
		capped  bool
		hasComp bool
	}{
		{dated, api.PlanFree, false, false},
		{forever, api.PlanScale, true, true},
		{subscribed, api.PlanHobby, true, false},
	} {
		got, err := store.AccountByID(ctx, c.acct.ID)
		if err != nil || got.Plan != c.plan {
			t.Fatalf("%s plan = %q, %v; want %q", c.acct.Email, got.Plan, err, c.plan)
		}
		if _, capped, _ := store.GetAccountOverageCapCents(ctx, c.acct.ID); capped != c.capped {
			t.Fatalf("%s capped = %v, want %v", c.acct.Email, capped, c.capped)
		}
		if row, _ := comps.ComplimentaryAccount(ctx, c.acct.ID); (row != nil) != c.hasComp {
			t.Fatalf("%s complimentary row = %+v, want present=%v", c.acct.Email, row, c.hasComp)
		}
	}
}

// A complimentary account cannot raise its spend cap or elect egress overage:
// it stops at the plan's included amounts.
func TestComplimentaryAccountRefusesOverage(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	var comps state.ComplimentaryAccountStore = *e.Store()
	acct := *e.Acct()
	if err := comps.SetComplimentaryAccount(context.Background(), state.ComplimentaryAccount{AccountID: acct.ID, Plan: api.PlanPro,
		Reason: "partner_program", GrantedBy: acct.ID, GrantedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		method, path string
		body         map[string]any
	}{
		{http.MethodPost, "/v1/account/overage-cap", map[string]any{"overage_cap_cents": 5000}},
		{http.MethodPut, "/v1/account/egress-overage", map[string]any{"enabled": true}},
	} {
		r := e.Do(t, c.method, c.path, c.body, nil)
		if r.Code != http.StatusConflict || !strings.Contains(r.Body.String(), "complimentary") {
			t.Fatalf("%s %s = %d %s, want a complimentary refusal", c.method, c.path, r.Code, r.Body.String())
		}
	}
	if r := e.Do(t, http.MethodPut, "/v1/account/egress-overage", map[string]any{"enabled": false}, nil); r.Code == http.StatusConflict {
		t.Fatalf("turning egress overage off is refused: %d %s", r.Code, r.Body.String())
	}
}
