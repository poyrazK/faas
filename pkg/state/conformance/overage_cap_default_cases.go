package conformance

import (
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
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
