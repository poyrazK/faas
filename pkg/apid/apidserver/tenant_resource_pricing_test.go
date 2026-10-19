package apidserver_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1309, 1310, 1319 — a tenant plan prices compute seconds with a volume
// ladder across consumers and surfaces; assigning a plan of another unit
// stops the compute stream with a free card; surface requests are weighted
// by their own routes.
func TestPlatformTenantComputeVolumeAndSourceRoutes(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "tenant-compute")
	tenant := tenantJSON[api.PlatformTenantResponse](t, e, http.MethodPost, "/v1/account/platform-tenants",
		api.CreatePlatformTenantRequest{ExternalRef: "customer-9", Name: "Customer 9"}, http.StatusCreated)
	base := "/v1/account/platform-tenants/" + tenant.ID
	for name, card := range map[string]api.CreatePlatformTenantRateCardRequest{
		"weighted compute": {Currency: "EUR", PriceMillicentsPerUnit: 1, Unit: "compute_second", RouteWeights: map[string]int64{"GET /a": 2}},
		"unknown unit":     {Currency: "EUR", PriceMillicentsPerUnit: 1, Unit: "gpu_hour"},
		"volume, no tiers": {Currency: "EUR", PriceMillicentsPerUnit: 1, TierMode: "volume"},
	} {
		if res := e.Do(t, http.MethodPost, base+"/rate-cards", card, nil); res.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: %d %s, want 422", name, res.Code, res.Body)
		}
	}

	five := int64(5)
	compute := tenantJSON[api.PlatformTenantPlanResponse](t, e, http.MethodPost, "/v1/account/platform-tenant-plans",
		api.PlatformTenantPlanRequest{Name: "compute", Currency: "EUR", Unit: "compute_second", TierMode: "volume",
			Tiers: []api.APIConsumerRateCardTier{{UpTo: &five, PriceMillicentsPerUnit: 0}, {PriceMillicentsPerUnit: 100}}}, http.StatusCreated)
	if compute.Unit != "compute_second" || compute.TierMode != "volume" {
		t.Fatalf("plan = %+v", compute)
	}
	assignment := tenantJSON[api.PlatformTenantPlanAssignmentResponse](t, e, http.MethodPost, base+"/plan-assignments",
		api.AssignPlatformTenantPlanRequest{PlanID: compute.ID}, http.StatusCreated)

	store := *e.Store()
	ctx := context.Background()
	acct := (*e.Acct()).ID
	consumer, err := store.CreateAPIConsumer(ctx, acct, appID, "customer-9-key", "Customer 9")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkPlatformTenantConsumer(ctx, acct, tenant.ID, consumer.ID); err != nil {
		t.Fatal(err)
	}
	minute := assignment.EffectiveFrom.Add(time.Minute)
	surface := uuid.NewString()
	for _, event := range []state.APIConsumerUsageEvent{
		{ConsumerKey: consumer.ID, RequestCount: 2, BillableUnits: 2, ComputeMS: 4400},
		{ConsumerKey: state.AnonymousConsumerKey, PlatformTenantSurfaceID: surface, RequestCount: 1, BillableUnits: 1, ComputeMS: 2600},
	} {
		event.EventID, event.AccountID, event.AppID, event.PlatformTenantID, event.WindowStart = uuid.NewString(), acct, appID, tenant.ID, minute
		if _, err := store.RecordAPIConsumerUsage(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	start := billing.MonthStart(minute)
	end := start.AddDate(0, 1, 0)
	statement := tenantJSON[api.PlatformTenantStatementResponse](t, e, http.MethodPost, base+"/usage-statements",
		api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &start, PeriodEnd: &end}, http.StatusCreated)
	// 4 s from the consumer and 3 s from the surface: 7 s is past the free
	// step, so every second costs 100 millicents.
	if statement.BillableUnits != 7 || statement.AmountMillicents != 700 {
		t.Fatalf("statement = %+v", statement)
	}

	requests := tenantJSON[api.PlatformTenantPlanResponse](t, e, http.MethodPost, "/v1/account/platform-tenant-plans",
		api.PlatformTenantPlanRequest{Name: "requests", Currency: "EUR", PriceMillicentsPerUnit: 10}, http.StatusCreated)
	moved := tenantJSON[api.PlatformTenantPlanAssignmentResponse](t, e, http.MethodPost, base+"/plan-assignments",
		api.AssignPlatformTenantPlanRequest{PlanID: requests.ID}, http.StatusCreated)
	if !moved.EffectiveFrom.After(assignment.EffectiveFrom) || moved.EffectiveFrom.After(time.Now().UTC().Add(3*time.Minute)) {
		t.Fatalf("request plan starts %s, want within minutes after the compute plan (%s)", moved.EffectiveFrom, assignment.EffectiveFrom)
	}
	stopped := false
	for _, card := range tenantJSON[api.PlatformTenantRateCardListResponse](t, e, http.MethodGet, base+"/rate-cards", nil, http.StatusOK).RateCards {
		stopped = stopped || (card.Unit == "compute_second" && card.EffectiveFrom.Equal(moved.EffectiveFrom) &&
			card.PriceMillicentsPerUnit == 0 && len(card.Tiers) == 0)
	}
	if !stopped {
		t.Fatal("assigning a request plan left the compute stream charging")
	}

	weighted := tenantJSON[api.PlatformTenantResponse](t, e, http.MethodPost, "/v1/account/platform-tenants",
		api.CreatePlatformTenantRequest{ExternalRef: "customer-10", Name: "Customer 10"}, http.StatusCreated)
	card := tenantJSON[api.PlatformTenantRateCardResponse](t, e, http.MethodPost, "/v1/account/platform-tenants/"+weighted.ID+"/rate-cards",
		api.CreatePlatformTenantRateCardRequest{Currency: "EUR", PriceMillicentsPerUnit: 100, RouteWeights: map[string]int64{"POST /generate": 5}}, http.StatusCreated)
	at := card.EffectiveFrom.Add(time.Minute)
	for _, route := range []string{"POST /generate", "POST /generate", "GET /status"} {
		if _, err := store.RecordAPIConsumerUsage(ctx, state.APIConsumerUsageEvent{EventID: uuid.NewString(), AccountID: acct, AppID: appID,
			ConsumerKey: state.AnonymousConsumerKey, PlatformTenantID: weighted.ID, PlatformTenantSurfaceID: surface,
			WindowStart: at, RequestCount: 1, BillableUnits: 1, BillingRoute: route}); err != nil {
			t.Fatal(err)
		}
	}
	start, end = billing.MonthStart(at), billing.MonthStart(at).AddDate(0, 1, 0)
	statement = tenantJSON[api.PlatformTenantStatementResponse](t, e, http.MethodPost, "/v1/account/platform-tenants/"+weighted.ID+"/usage-statements",
		api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &start, PeriodEnd: &end}, http.StatusCreated)
	// Two surface requests on POST /generate at weight 5 and one at 1.
	if statement.BillableUnits != 11 || statement.AmountMillicents != 1100 {
		t.Fatalf("weighted surface statement = %+v", statement)
	}
}

// adr: 1309 — an app card's tier_mode is validated and returned.
func TestAPIConsumerVolumeRateCard(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	apidserver.MustSeedApp(t, e, "volume")
	cards := "/v1/apps/volume/rate-cards"
	for name, req := range map[string]api.CreateAPIConsumerRateCardRequest{
		"no tiers":     {Currency: "EUR", PriceMillicentsPerUnit: 1, TierMode: "volume"},
		"unknown mode": {Currency: "EUR", PriceMillicentsPerUnit: 1, TierMode: "stepped"},
	} {
		if res := e.Do(t, http.MethodPost, cards, req, nil); res.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: %d %s, want 422", name, res.Code, res.Body)
		}
	}
	ten := int64(10)
	card := tenantJSON[api.APIConsumerRateCardResponse](t, e, http.MethodPost, cards, api.CreateAPIConsumerRateCardRequest{Currency: "EUR",
		TierMode: "volume", Tiers: []api.APIConsumerRateCardTier{{UpTo: &ten, PriceMillicentsPerUnit: 100}, {PriceMillicentsPerUnit: 50}}}, http.StatusCreated)
	if card.TierMode != "volume" {
		t.Fatalf("card = %+v", card)
	}
	flat := tenantJSON[api.APIConsumerRateCardResponse](t, e, http.MethodPost, cards, api.CreateAPIConsumerRateCardRequest{Currency: "EUR",
		PriceMillicentsPerUnit: 5, EffectiveFrom: func() *time.Time { at := card.EffectiveFrom.Add(time.Hour); return &at }()}, http.StatusCreated)
	if flat.TierMode != "graduated" {
		t.Fatalf("flat card = %+v", flat)
	}
}
