package floor

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// appPlanResolver resolves by account like fakePlanResolver, and by the app's
// organization when it has one.
type appPlanResolver struct {
	fakePlanResolver
	orgs map[string]api.Plan
}

func (p *appPlanResolver) ResolveAppPlan(_ context.Context, app state.App) (api.Plan, bool) {
	if plan, ok := p.orgs[app.OrgID]; ok {
		return plan, true
	}
	return p.ResolvePlan(context.Background(), app.AccountID)
}

// A resolver that knows the app decides a company app's floor by its
// organization's plan, the subject meterd bills the floor to; an account-only
// resolver keeps the creator's plan, and an unresolved subject has no floor.
// adr: 20261101113742
func TestTriggerResolvePlanFollowsTheAppsSubject(t *testing.T) {
	ctx := context.Background()
	app := state.App{AccountID: "member", OrgID: "company"}
	byAccount := &fakePlanResolver{plans: map[string]api.Plan{"member": api.PlanFree}}
	if got := (&Trigger{planResolver: byAccount}).resolvePlan(ctx, app); got != api.PlanFree {
		t.Fatalf("account resolver plan = %s, want the creator's free", got)
	}
	byApp := &appPlanResolver{fakePlanResolver: *byAccount, orgs: map[string]api.Plan{"company": api.PlanPro}}
	if got := (&Trigger{planResolver: byApp}).resolvePlan(ctx, app); got != api.PlanPro {
		t.Fatalf("app resolver plan = %s, want the organization's pro", got)
	}
	if got := (&Trigger{planResolver: byApp}).resolvePlan(ctx, state.App{AccountID: "unknown"}); got != api.PlanFree {
		t.Fatalf("unresolved subject plan = %s, want free", got)
	}
	if got := (&Trigger{}).resolvePlan(ctx, app); got != api.PlanFree {
		t.Fatalf("no resolver plan = %s, want free", got)
	}
}
