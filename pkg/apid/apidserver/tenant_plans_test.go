package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

func tenantJSON[T any](t *testing.T, e apidserver.TestEnv, method, path string, body any, want int) T {
	t.Helper()
	res := e.Do(t, method, path, body, nil)
	var out T
	if res.Code != want || (res.Body.Len() > 0 && json.Unmarshal(res.Body.Bytes(), &out) != nil) {
		t.Fatalf("%s %s: %d %s, want %d", method, path, res.Code, res.Body, want)
	}
	return out
}

// adr: 1307 — a tenant plan bundles a weighted card and a request budget;
// assigning it creates the tenant's card and budget, and the month's
// statement charges consumer requests by route weight.
func TestPlatformTenantPlanAndRouteWeights(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "tenant-plans")
	tenant := tenantJSON[api.PlatformTenantResponse](t, e, http.MethodPost, "/v1/account/platform-tenants",
		api.CreatePlatformTenantRequest{ExternalRef: "customer-7", Name: "Customer 7"}, http.StatusCreated)

	plan := api.PlatformTenantPlanRequest{Name: "growth", Currency: "EUR", PriceMillicentsPerUnit: 100,
		RouteWeights:  map[string]int64{"POST /generate": 5},
		RequestBudget: &api.PlatformTenantPlanBudget{MaxRequestsPerMinute: 600, MaxRequestsPerDay: 100000}}
	created := tenantJSON[api.PlatformTenantPlanResponse](t, e, http.MethodPost, "/v1/account/platform-tenant-plans", plan, http.StatusCreated)
	tenantJSON[api.Problem](t, e, http.MethodPost, "/v1/account/platform-tenant-plans", plan, http.StatusConflict)
	bad := plan
	bad.Name, bad.RouteWeights = "bad", map[string]int64{"generate": 5}
	tenantJSON[api.Problem](t, e, http.MethodPost, "/v1/account/platform-tenant-plans", bad, http.StatusUnprocessableEntity)

	base := "/v1/account/platform-tenants/" + tenant.ID
	assignment := tenantJSON[api.PlatformTenantPlanAssignmentResponse](t, e, http.MethodPost, base+"/plan-assignments",
		api.AssignPlatformTenantPlanRequest{PlanID: created.ID}, http.StatusCreated)
	cards := tenantJSON[api.PlatformTenantRateCardListResponse](t, e, http.MethodGet, base+"/rate-cards", nil, http.StatusOK)
	if len(cards.RateCards) != 1 || cards.RateCards[0].ID != assignment.RateCardID || cards.RateCards[0].RouteWeights["POST /generate"] != 5 {
		t.Fatalf("cards = %+v, assignment = %+v", cards, assignment)
	}
	budget := tenantJSON[api.PlatformTenantRequestBudgetResponse](t, e, http.MethodGet, base+"/request-budget", nil, http.StatusOK)
	if budget.MaxRequestsPerMinute != 600 || budget.MaxRequestsPerDay != 100000 {
		t.Fatalf("budget = %+v", budget)
	}
	history := tenantJSON[api.PlatformTenantPlanAssignmentListResponse](t, e, http.MethodGet, base+"/plan-assignments", nil, http.StatusOK)
	if len(history.Assignments) != 1 || history.Assignments[0].PlanName != "growth" {
		t.Fatalf("history = %+v", history)
	}

	store := *e.Store()
	ctx := context.Background()
	consumer, err := store.CreateAPIConsumer(ctx, (*e.Acct()).ID, appID, "customer-7-key", "Customer 7")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkPlatformTenantConsumer(ctx, (*e.Acct()).ID, tenant.ID, consumer.ID); err != nil {
		t.Fatal(err)
	}
	minute := assignment.EffectiveFrom.Add(time.Minute)
	routes := []string{"POST /generate", "POST /generate", "POST /generate", "POST /generate"}
	for range 6 {
		routes = append(routes, "GET /items")
	}
	for _, route := range routes {
		if _, err := store.RecordAPIConsumerUsage(ctx, state.APIConsumerUsageEvent{EventID: uuid.NewString(), AccountID: (*e.Acct()).ID,
			AppID: appID, ConsumerKey: consumer.ID, PlatformTenantID: tenant.ID, WindowStart: minute,
			RequestCount: 1, BillableUnits: 1, BillingRoute: route}); err != nil {
			t.Fatal(err)
		}
	}
	start := billing.MonthStart(minute)
	end := start.AddDate(0, 1, 0)
	statement := tenantJSON[api.PlatformTenantStatementResponse](t, e, http.MethodPost, base+"/usage-statements",
		api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &start, PeriodEnd: &end}, http.StatusCreated)
	// 6 requests at weight 1 and 4 at weight 5 = 26 units at 100 millicents.
	if statement.BillableUnits != 26 || statement.AmountMillicents != 2600 {
		t.Fatalf("statement = %+v", statement)
	}
	week := start.AddDate(0, 0, 7)
	tenantJSON[api.Problem](t, e, http.MethodPost, base+"/usage-statements",
		api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &start, PeriodEnd: &week}, http.StatusUnprocessableEntity)
}
