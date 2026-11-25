package apidserver_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/consumersignup"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261125194510 — with account tools on, an OAuth consumer's
// account_usage names its plan and limits and account_plans lists the
// offered plans with an upgrade link; off, they are refused.
func TestConsumerAccountTools(t *testing.T) {
	withBillingKeys(t)
	e := apidserver.Setup(t, api.PlanHobby)
	stripe := &consumerSignupStripe{}
	srv := httptest.NewServer(stripe)
	defer srv.Close()
	client := &stripeinvoice.Client{BaseURL: srv.URL, HTTP: srv.Client()}
	(*e.S()).StatementInvoices().Stripe = client
	signup := (*e.S()).ConsumerSignup()
	signup.Stripe = client
	signup.UpgradeKey = bytes.Repeat([]byte{7}, 32)
	appID := apidserver.MustSeedApp(t, e, "search")
	decode := func(t *testing.T, res *httptest.ResponseRecorder, want int, out any) {
		t.Helper()
		if res.Code != want {
			t.Fatalf("status %d, want %d: %s", res.Code, want, res.Body)
		}
		if out != nil {
			if err := json.Unmarshal(res.Body.Bytes(), out); err != nil {
				t.Fatalf("decode: %v: %s", err, res.Body)
			}
		}
	}
	plan := func(name string, price int64, limits map[string]api.APIConsumerPlanRouteLimit) string {
		var p api.APIConsumerPlanResponse
		decode(t, e.Do(t, http.MethodPost, "/v1/apps/search/consumer-plans", api.CreateAPIConsumerPlanRequest{Name: name,
			MaxRequestsPerMinute: 60, MaxUnitsPerMonth: 1000, RouteLimits: limits}, nil), http.StatusCreated, &p)
		decode(t, e.Do(t, http.MethodPost, "/v1/apps/search/rate-cards", api.CreateAPIConsumerRateCardRequest{Currency: "USD",
			PriceMillicentsPerUnit: price, PlanID: p.ID}, nil), http.StatusCreated, nil)
		return p.ID
	}
	free := plan("free", 0, map[string]api.APIConsumerPlanRouteLimit{
		api.MCPBillingToolsPrefix + "/search": {MaxRequestsPerMonth: 100}, "GET /status": {MaxRequestsPerMinute: 5}})
	pro := plan("pro", 50, nil)
	decode(t, e.Do(t, http.MethodPut, "/v1/account/billing-connections/stripe", api.PutBillingConnectionRequest{SecretKey: "rk_test_51abcdWXYZ"}, nil),
		http.StatusOK, nil)

	settings := api.PutConsumerSignupRequest{DisplayName: "Search tools", PlanIDs: []string{free, pro}, FreePlanID: free,
		CustomerPlanChanges: true, AccountTools: true}
	if res := e.Do(t, http.MethodPut, "/v1/apps/search/consumer-signup", settings, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("account tools without OAuth consumers: %d %s", res.Code, res.Body)
	}
	settings.OAuthConsumers = true
	var set api.ConsumerSignupSettingsResponse
	decode(t, e.Do(t, http.MethodPut, "/v1/apps/search/consumer-signup", settings, nil), http.StatusOK, &set)
	if !set.AccountTools || !signup.AccountToolsOn(t.Context(), e.Acct().ID, appID) {
		t.Fatalf("settings = %+v; want account tools on", set)
	}

	consumer, _, err := signup.EnrollOAuthConsumer(t.Context(), state.APIConsumerEnrollment{AccountID: e.Acct().ID, AppID: appID,
		Issuer: "https://login.example", Subject: "u1", Email: "u1@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	tool := func(name string, out any) error {
		raw, err := signup.AccountTool(t.Context(), e.Acct().ID, appID, consumer.ID, name)
		if err == nil && out != nil {
			err = json.Unmarshal(raw, out)
		}
		return err
	}

	var usage api.MCPAccountUsage
	if err := tool(api.MCPAccountToolUsage, &usage); err != nil {
		t.Fatal(err)
	}
	if usage.Plan == nil || usage.Plan.Name != "free" || usage.Plan.MaxUnitsPerMonth != 1000 || usage.Plan.MaxRequestsPerMinute != 60 ||
		usage.UnitsLeft == nil || *usage.UnitsLeft != 1000 || usage.Units != 0 || usage.Tools == nil {
		t.Fatalf("usage = %+v", usage)
	}
	if len(usage.Plan.ToolLimits) != 1 || usage.Plan.ToolLimits["search"].MaxRequestsPerMonth != 100 {
		t.Fatalf("tool limits = %+v; want only the search tool's", usage.Plan.ToolLimits)
	}

	var plans api.MCPAccountPlans
	if err := tool(api.MCPAccountToolPlans, &plans); err != nil {
		t.Fatal(err)
	}
	if plans.CurrentPlan != "free" || len(plans.Plans) != 2 || !strings.Contains(plans.UpgradeURL, "/v1/consumer-signup/search/upgrade?token=") ||
		plans.UpgradeURLExpiresAt == nil {
		t.Fatalf("plans = %+v", plans)
	}

	if err := tool("account_delete", nil); !errors.Is(err, consumersignup.ErrAccountToolUnknown) {
		t.Errorf("unknown tool = %v", err)
	}
	if _, err := signup.AccountTool(t.Context(), "00000000-0000-4000-8000-000000000001", appID, consumer.ID, api.MCPAccountToolUsage); err == nil {
		t.Error("another account's app answered")
	}
	if _, err := signup.AccountTool(t.Context(), e.Acct().ID, appID, "00000000-0000-4000-8000-000000000002", api.MCPAccountToolUsage); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("unknown consumer = %v; want not found", err)
	}

	settings.AccountTools = false
	decode(t, e.Do(t, http.MethodPut, "/v1/apps/search/consumer-signup", settings, nil), http.StatusOK, nil)
	if err := tool(api.MCPAccountToolUsage, nil); !errors.Is(err, consumersignup.ErrAccountToolsOff) {
		t.Errorf("tools off = %v; want off", err)
	}
	if signup.AccountToolsOn(t.Context(), e.Acct().ID, appID) {
		t.Error("account tools still on")
	}
}
