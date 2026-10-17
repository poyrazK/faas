package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// checkPlatformTenantPlans runs the ADR-1307 store contract: plans keep
// unique names and their terms, assignments are listed newest first, and
// tenant cards keep route weights.
func checkPlatformTenantPlans(t *testing.T, ctx context.Context, st state.Store, accountID string) {
	t.Helper()
	plans := st.(state.PlatformTenantPlanStore)
	tenants := st.(state.PlatformTenantStore)
	cards := st.(state.PlatformTenantRateCardStore)
	perMinute, perDay := int64(60), int64(1000)
	plan, err := plans.CreatePlatformTenantPlan(ctx, state.PlatformTenantPlan{AccountID: accountID, Name: "growth", Currency: "EUR",
		PriceMillicentsPerUnit: 100, RouteWeights: map[string]int64{"POST /generate": 5},
		MaxRequestsPerMinute: &perMinute, MaxRequestsPerDay: &perDay})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plans.CreatePlatformTenantPlan(ctx, state.PlatformTenantPlan{AccountID: accountID, Name: "growth", Currency: "EUR"}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate name = %v", err)
	}
	if _, err := plans.CreatePlatformTenantPlan(ctx, state.PlatformTenantPlan{AccountID: accountID, Name: "half", Currency: "EUR",
		MaxRequestsPerMinute: &perMinute}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("half a budget = %v", err)
	}
	plan.Name, plan.PriceMillicentsPerUnit = "growth-2", 200
	updated, err := plans.UpdatePlatformTenantPlan(ctx, plan)
	if err != nil || updated.Name != "growth-2" || updated.RouteWeights["POST /generate"] != 5 || *updated.MaxRequestsPerDay != 1000 {
		t.Fatalf("updated = %+v, %v", updated, err)
	}
	if got, err := plans.GetPlatformTenantPlan(ctx, accountID, plan.ID); err != nil || got.PriceMillicentsPerUnit != 200 {
		t.Fatalf("get = %+v, %v", got, err)
	}

	tenant, _, err := tenants.CreatePlatformTenant(ctx, accountID, "plan-tenant", "Plan tenant", 100)
	if err != nil {
		t.Fatal(err)
	}
	card, err := cards.CreatePlatformTenantRateCardVersion(ctx, state.PlatformTenantRateCardInput{AccountID: accountID, TenantID: tenant.ID,
		Currency: "EUR", PriceMillicentsPerUnit: 200, RouteWeights: map[string]int64{"POST /generate": 5},
		EffectiveFrom: time.Now().UTC().Truncate(time.Minute).Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := cards.ListPlatformTenantRateCards(ctx, accountID, tenant.ID)
	if err != nil || len(listed) != 1 || listed[0].RouteWeights["POST /generate"] != 5 || !listed[0].MonthlyPricing() {
		t.Fatalf("cards = %+v, %v", listed, err)
	}
	if _, err := plans.RecordPlatformTenantPlanAssignment(ctx, state.PlatformTenantPlanAssignment{AccountID: accountID, TenantID: tenant.ID,
		PlanID: plan.ID, RateCardID: card.ID, EffectiveFrom: card.EffectiveFrom}); err != nil {
		t.Fatal(err)
	}
	history, err := plans.ListPlatformTenantPlanAssignments(ctx, accountID, tenant.ID)
	if err != nil || len(history) != 1 || history[0].PlanID != plan.ID || history[0].RateCardID != card.ID {
		t.Fatalf("history = %+v, %v", history, err)
	}
}

// adr: 1307
func TestMemStorePlatformTenantPlans(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	accountID, _ := seedConsumerKeyAccountApp(t, ctx, store)
	checkPlatformTenantPlans(t, ctx, store, accountID)
}
