// adr: 20261111140000 20261103131742

package apidserver_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// A complimentary account that subscribes is billed by its subscription from
// then on: the complimentary row goes, so meterd bills and holds its usage
// again, and its $0 spend cap becomes its plan's default, so it is not paused
// (bug hunt 2026-11-13 R7-3).
func TestComplimentaryAccountEndsWhenItSubscribes(t *testing.T) {
	ctx := context.Background()
	srv, _, store := apidserver.V1AuthTestHarness(t)
	comps := store.(state.ComplimentaryAccountStore)
	acct, err := store.CreateAccount(ctx, "comp-subscribes@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	if err := store.UpdateAccountOverageCapCents(ctx, acct.ID, &zero); err != nil {
		t.Fatal(err)
	}
	if err := comps.SetComplimentaryAccount(ctx, state.ComplimentaryAccount{AccountID: acct.ID, Plan: api.PlanHobby,
		Reason: "partner_program", GrantedBy: acct.ID, GrantedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	// A revoke (no subscription) leaves the complimentary account alone.
	if err := srv.StampActiveBillingSubscription(ctx, acct, ""); err != nil {
		t.Fatal(err)
	}
	if row, _ := comps.ComplimentaryAccount(ctx, acct.ID); row == nil {
		t.Fatal("an empty subscription ended the complimentary account")
	}

	if err := srv.StampActiveBillingSubscription(ctx, acct, "sub_comp"); err != nil {
		t.Fatal(err)
	}
	if row, err := comps.ComplimentaryAccount(ctx, acct.ID); err != nil || row != nil {
		t.Fatalf("complimentary row after subscribing = %+v, %v; want none", row, err)
	}
	want, _ := api.PlanHobby.DefaultOverageCapCents()
	if cents, capped, err := store.GetAccountOverageCapCents(ctx, acct.ID); err != nil || !capped || cents != want {
		t.Fatalf("spend cap after subscribing = %d (capped %v), %v; want the Hobby default %d", cents, capped, err, want)
	}
	// The webhook's plan change then moves the default with the plan.
	if err := store.UpdateAccountPlan(ctx, acct.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	wantPro, _ := api.PlanPro.DefaultOverageCapCents()
	if cents, _, _ := store.GetAccountOverageCapCents(ctx, acct.ID); cents != wantPro {
		t.Fatalf("spend cap after the upgrade = %d, want the Pro default %d", cents, wantPro)
	}
}

// The monthly quota stop parks a subject's apps only: its managed PostgreSQL
// databases keep running on their own allowances. Payment, abuse and operator
// suspensions still stop them (bug hunt 2026-11-13 R10-1).
func TestManagedPostgresKeepsRunningThroughQuotaStop(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "egress-stop@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	inactive := func() bool {
		t.Helper()
		current, err := store.AccountByID(ctx, acct.ID)
		if err != nil {
			t.Fatal(err)
		}
		got, err := apidserver.ManagedPostgresSubjectInactive(ctx, store, current)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if inactive() {
		t.Fatal("an active account's databases are inactive")
	}
	if ok, err := store.SuspendAccountForFreeQuota(ctx, acct.ID); err != nil || !ok {
		t.Fatalf("quota stop = %v, %v", ok, err)
	}
	if inactive() {
		t.Fatal("the quota stop stopped the databases")
	}
	if ok, err := store.RestoreFreeQuotaSuspension(ctx, acct.ID); err != nil || !ok {
		t.Fatalf("quota restore = %v, %v", ok, err)
	}
	if err := store.UpdateAccountStatus(ctx, acct.ID, state.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	if !inactive() {
		t.Fatal("an operator suspension left the databases running")
	}
}
