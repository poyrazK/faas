package conformance

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// testOverageCapDefaultFollowsThePlan pins the default spend cap
// (ADR-20261029180000): becoming paid sets the plan's default, a default cap
// follows plan changes and is removed on Free, and a cap the customer set or
// cleared is kept.
func testOverageCapDefaultFollowsThePlan(t *testing.T, fx *Fixture) {
	s, ctx := fx.Store, fx.Ctx
	acct, err := s.CreateAccount(ctx, "overage-cap-default-"+uuid.NewString()+"@example.com", api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	expect := func(step string, want int64, wantSet bool) {
		t.Helper()
		got, ok, err := s.GetAccountOverageCapCents(ctx, acct.ID)
		if err != nil {
			t.Fatalf("%s: GetAccountOverageCapCents: %v", step, err)
		}
		if ok != wantSet || (wantSet && got != want) {
			t.Fatalf("%s: cap = (%d, %v), want (%d, %v)", step, got, ok, want, wantSet)
		}
	}
	move := func(plan api.Plan) {
		t.Helper()
		if err := s.UpdateAccountPlan(ctx, acct.ID, plan); err != nil {
			t.Fatalf("UpdateAccountPlan(%s): %v", plan, err)
		}
	}
	hobby, _ := api.PlanHobby.DefaultOverageCapCents()
	scale, _ := api.PlanScale.DefaultOverageCapCents()

	expect("free", 0, false)
	move(api.PlanHobby)
	expect("free -> hobby", hobby, true)
	move(api.PlanHobby)
	expect("hobby -> hobby", hobby, true)
	move(api.PlanScale)
	expect("hobby -> scale", scale, true)
	move(api.PlanFree)
	expect("scale -> free", 0, false)

	move(api.PlanPro)
	custom := int64(12_345)
	if err := s.UpdateAccountOverageCapCents(ctx, acct.ID, &custom); err != nil {
		t.Fatalf("UpdateAccountOverageCapCents: %v", err)
	}
	move(api.PlanScale)
	expect("customer cap across an upgrade", custom, true)
	move(api.PlanFree)
	expect("customer cap on free", custom, true)

	move(api.PlanHobby)
	if err := s.UpdateAccountOverageCapCents(ctx, acct.ID, nil); err != nil {
		t.Fatalf("UpdateAccountOverageCapCents(nil): %v", err)
	}
	move(api.PlanPro)
	expect("cleared cap across an upgrade", 0, false)
}

// testOrgSpendCap pins ADR-20261030093512: an organization that owns its
// resources is its own billing subject, so it has its own spend cap, its
// default follows its plan, meterd loads it with the accounts', and a spend
// cap hold may name it.
func testOrgSpendCap(t *testing.T, fx *Fixture) {
	s, ctx := fx.Store, fx.Ctx
	org, err := s.CreateOrg(ctx, state.Org{Slug: "cap-org-" + uuid.NewString()[:8], Name: "Cap org", Plan: api.PlanFree, Status: state.OrgStatusActive})
	if err != nil {
		t.Fatalf("CreateOrg: %v", err)
	}
	expect := func(step string, want int64, wantSet bool) {
		t.Helper()
		got, ok, err := s.GetAccountOverageCapCents(ctx, org.ID)
		if err != nil {
			t.Fatalf("%s: GetAccountOverageCapCents: %v", step, err)
		}
		if ok != wantSet || (wantSet && got != want) {
			t.Fatalf("%s: cap = (%d, %v), want (%d, %v)", step, got, ok, want, wantSet)
		}
		all, err := s.LoadAllOverageCapCents(ctx)
		if err != nil {
			t.Fatalf("%s: LoadAllOverageCapCents: %v", step, err)
		}
		if loaded, present := all[org.ID]; present != wantSet || (wantSet && loaded != want) {
			t.Fatalf("%s: loaded cap = (%d, %v), want (%d, %v)", step, loaded, present, want, wantSet)
		}
	}
	move := func(plan api.Plan) {
		t.Helper()
		if err := s.UpdateAccountPlan(ctx, org.ID, plan); err != nil {
			t.Fatalf("UpdateAccountPlan(%s): %v", plan, err)
		}
	}
	pro, _ := api.PlanPro.DefaultOverageCapCents()

	expect("free organization", 0, false)
	move(api.PlanPro)
	expect("free -> pro", pro, true)
	custom := int64(4_200)
	if err := s.UpdateAccountOverageCapCents(ctx, org.ID, &custom); err != nil {
		t.Fatalf("UpdateAccountOverageCapCents: %v", err)
	}
	expect("organization's own cap", custom, true)
	if err := s.UpdateAccountOverageCapCents(ctx, org.ID, nil); err != nil {
		t.Fatalf("UpdateAccountOverageCapCents(nil): %v", err)
	}
	expect("cleared", 0, false)

	holds, ok := s.(state.SpendCapHoldStore)
	if !ok {
		return
	}
	month := time.Now().UTC()
	month = time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	if err := holds.PutSpendCapHold(ctx, state.SpendCapHold{AccountID: org.ID, Month: month, CapCents: 100, ObservedCents: 150}); err != nil {
		t.Fatalf("PutSpendCapHold for an organization: %v", err)
	}
	if hold, found, err := holds.GetSpendCapHold(ctx, org.ID); err != nil || !found || hold.CapCents != 100 {
		t.Fatalf("GetSpendCapHold = %+v, %v, %v; want the organization's hold", hold, found, err)
	}
}
