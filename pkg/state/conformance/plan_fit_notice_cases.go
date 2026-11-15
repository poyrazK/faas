package conformance

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// testPlanFitNoticeRoundTrip pins the plan-fit notice record (billing review
// H17-4): absent until put, the last put wins, and clear removes it.
func testPlanFitNoticeRoundTrip(t *testing.T, fx *Fixture) {
	notices, ok := fx.Store.(state.PlanFitNoticeStore)
	if !ok {
		t.Skip("store has no plan-fit notices")
	}
	ctx, id := fx.Ctx, fx.Account.ID
	if _, found, err := notices.PlanFitNotice(ctx, id); err != nil || found {
		t.Fatalf("initial notice found=%v err=%v, want none", found, err)
	}
	first := state.PlanFitNotice{Plan: api.PlanFree, AppsDigest: state.PlanFitDigest([]string{"b", "a"})}
	if first.AppsDigest != state.PlanFitDigest([]string{"a", "b"}) {
		t.Fatal("digest depends on order")
	}
	if err := notices.PutPlanFitNotice(ctx, id, first); err != nil {
		t.Fatalf("put: %v", err)
	}
	second := state.PlanFitNotice{Plan: api.PlanHobby, AppsDigest: state.PlanFitDigest([]string{"c"})}
	if err := notices.PutPlanFitNotice(ctx, id, second); err != nil {
		t.Fatalf("put again: %v", err)
	}
	if got, found, err := notices.PlanFitNotice(ctx, id); err != nil || !found || got != second {
		t.Fatalf("notice = %+v found=%v err=%v, want %+v", got, found, err, second)
	}
	if err := notices.ClearPlanFitNotice(ctx, id); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, found, err := notices.PlanFitNotice(ctx, id); err != nil || found {
		t.Fatalf("after clear found=%v err=%v, want none", found, err)
	}
}
