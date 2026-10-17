package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// checkTenantResourcesAndCollection runs the ADR-1309–1311 store contract:
// ladders keep their mode, tenant cards and plans their unit, tenant usage
// minutes sum surface compute and keep its routes, and billing connections
// and exports keep their collection mode.
func checkTenantResourcesAndCollection(t *testing.T, ctx context.Context, st state.Store, accountID, appID string) {
	t.Helper()
	next := time.Now().UTC().Truncate(time.Minute).Add(time.Minute)
	upTo := int64(10)
	ladder := []state.APIConsumerRateCardTier{{UpTo: &upTo, PriceMillicentsPerUnit: 100}, {PriceMillicentsPerUnit: 50}}
	card, err := st.(state.APIConsumerRateCardStore).CreateAPIConsumerRateCardVersion(ctx, state.APIConsumerRateCardInput{AccountID: accountID,
		AppID: appID, Currency: "EUR", Tiers: ladder, TierMode: state.RateCardTierModeVolume, EffectiveFrom: next})
	if err != nil || card.TierMode != state.RateCardTierModeVolume {
		t.Fatalf("volume card = %+v, %v", card, err)
	}
	if _, err := st.(state.APIConsumerRateCardStore).CreateAPIConsumerRateCardVersion(ctx, state.APIConsumerRateCardInput{AccountID: accountID,
		AppID: appID, Currency: "EUR", TierMode: state.RateCardTierModeVolume, EffectiveFrom: next.Add(time.Minute)}); err == nil {
		t.Fatal("a volume card without tiers was accepted")
	}

	tenant, _, err := st.(state.PlatformTenantStore).CreatePlatformTenant(ctx, accountID, "compute-tenant", "Compute tenant", 100)
	if err != nil {
		t.Fatal(err)
	}
	tenantCard, err := st.(state.PlatformTenantRateCardStore).CreatePlatformTenantRateCardVersion(ctx, state.PlatformTenantRateCardInput{
		AccountID: accountID, TenantID: tenant.ID, Currency: "EUR", Tiers: ladder, TierMode: state.RateCardTierModeVolume,
		Unit: state.APIConsumerRateCardUnitComputeSecond, EffectiveFrom: next})
	if err != nil || tenantCard.Unit != state.APIConsumerRateCardUnitComputeSecond || tenantCard.TierMode != state.RateCardTierModeVolume || !tenantCard.MonthlyPricing() {
		t.Fatalf("tenant card = %+v, %v", tenantCard, err)
	}
	plan, err := st.(state.PlatformTenantPlanStore).CreatePlatformTenantPlan(ctx, state.PlatformTenantPlan{AccountID: accountID, Name: "egress",
		Currency: "EUR", PriceMillicentsPerUnit: 900, Unit: state.APIConsumerRateCardUnitResponseMegabyte})
	if err != nil || plan.Unit != state.APIConsumerRateCardUnitResponseMegabyte || plan.TierMode != state.RateCardTierModeGraduated {
		t.Fatalf("plan = %+v, %v", plan, err)
	}

	surface := uuid.NewString()
	minute := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	usage := st.(state.ConsumerUsageStore)
	for _, ms := range []int64{1200, 800} {
		if _, err := usage.RecordAPIConsumerUsage(ctx, state.APIConsumerUsageEvent{EventID: uuid.NewString(), AccountID: accountID, AppID: appID,
			ConsumerKey: state.AnonymousConsumerKey, PlatformTenantID: tenant.ID, PlatformTenantSurfaceID: surface, WindowStart: minute,
			RequestCount: 1, BillableUnits: 1, ComputeMS: ms, ResponseBytes: 1000, BillingRoute: "POST /generate"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := usage.RecordAPIConsumerUsage(ctx, state.APIConsumerUsageEvent{EventID: uuid.NewString(), AccountID: accountID, AppID: appID,
		ConsumerKey: state.AnonymousConsumerKey, WindowStart: minute, RequestCount: 1, BillableUnits: 1, ComputeMS: 5}); err == nil {
		t.Fatal("anonymous usage without a tenant source carried compute")
	}
	minutes, err := st.(state.PlatformTenantStatementStore).ListPlatformTenantUsageMinutes(ctx, accountID, tenant.ID, minute, minute.Add(time.Minute))
	if err != nil || len(minutes) != 1 || minutes[0].SurfaceID != surface || minutes[0].ComputeMS != 2000 || minutes[0].ResponseBytes != 2000 {
		t.Fatalf("tenant minutes = %+v, %v", minutes, err)
	}
	routes, err := st.(state.PlatformTenantSourceRouteUsageStore).ListPlatformTenantSourceRouteUsage(ctx, accountID, tenant.ID, minute, minute.Add(time.Minute))
	if err != nil || len(routes) != 1 || routes[0].SourceKind != "surface" || routes[0].SourceID != surface ||
		routes[0].Route != "POST /generate" || routes[0].BillableUnits != 2 {
		t.Fatalf("source routes = %+v, %v", routes, err)
	}

	exports := st.(state.StatementInvoiceExportStore)
	if _, err := exports.PutBillingConnection(ctx, state.BillingConnection{AccountID: accountID, Provider: state.BillingProviderPaddle,
		SealedSecret: []byte("sealed"), KeyLast4: "WXYZ", Collection: state.CollectionCharge}); err == nil {
		t.Fatal("paddle accepted collection charge")
	}
	if _, err := exports.PutBillingConnection(ctx, state.BillingConnection{AccountID: accountID, Provider: state.BillingProviderStripe,
		SealedSecret: []byte("sealed"), KeyLast4: "WXYZ", Collection: state.CollectionSend, DaysUntilDue: 14}); err != nil {
		t.Fatal(err)
	}
	conn, err := exports.GetBillingConnection(ctx, accountID, state.BillingProviderStripe)
	if err != nil || conn.Collection != state.CollectionSend || conn.DaysUntilDue != 14 {
		t.Fatalf("connection = %+v, %v", conn, err)
	}
	export, _, err := exports.BeginStatementInvoiceExport(ctx, state.StatementInvoiceExport{AccountID: accountID,
		SourceKind: state.StatementSourceAppConsumer, StatementID: uuid.NewString(), Provider: state.BillingProviderStripe,
		Customer: "cus_Collect", Currency: "EUR", AmountMinor: 100, Collection: conn.Collection, DaysUntilDue: conn.DaysUntilDue})
	if err != nil || export.Collection != state.CollectionSend || export.DaysUntilDue != 14 {
		t.Fatalf("export = %+v, %v", export, err)
	}
}

// adr: 1309, 1310, 1311
func TestMemStoreTenantResourcesAndCollection(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	checkTenantResourcesAndCollection(t, ctx, store, accountID, appID)
}
