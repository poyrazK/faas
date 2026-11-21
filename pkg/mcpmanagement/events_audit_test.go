// adr: 20261118161500
package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

// eventsFixture serves event, consumer, audit and budget routes for app
// alpha on top of the operation fixture.
type eventsFixture struct {
	*operationFixture
	vmu         sync.Mutex
	recoveries  []api.EventRecoveryRequest
	consumers   []api.CreateAPIConsumerRequest
	auditAppIDs []string
	auditLogs   int
	budgets     []api.CreateFinancialBudgetRequest
}

func newEventsFixture(t *testing.T, capabilities []string, allowed map[string]string) (*management, *eventsFixture) {
	t.Helper()
	f := &eventsFixture{operationFixture: &operationFixture{base: newLocalFixture(t, true, "release-base"), revision: 1, values: map[string]string{}}}
	backend := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(backend.Close)
	_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, agentKey: true, Capabilities: capabilities, AllowedApps: allowed, Toolsets: []string{ToolsetAll}})
	if err != nil {
		t.Fatal(err)
	}
	return m, f
}

func (f *eventsFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.vmu.Lock()
	defer f.vmu.Unlock()
	encode := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	appID := f.base.app.ID
	switch path := r.URL.Path; {
	case path == "/v1/apps/alpha/event-subscriptions":
		encode(api.EventSubscriptionListResponse{AppSlug: "alpha", Subscriptions: []api.EventSubscriptionResponse{{ID: "sub-1", AppID: appID}}})
	case path == "/v1/apps/alpha/event-recoveries/preview":
		encode(api.EventRecoveryPreview{MatchedCount: 3})
	case path == "/v1/apps/alpha/event-recoveries" && r.Method == http.MethodPost:
		var req api.EventRecoveryRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.recoveries = append(f.recoveries, req)
		encode(api.EventRecoveryJob{ID: "job-1", AppID: appID})
	case path == "/v1/event-recoveries/job-other":
		encode(api.EventRecoveryJob{ID: "job-other", AppID: "another-app"})
	case path == "/v1/apps/alpha/consumers/c-1":
		encode(api.APIConsumerResponse{ID: "c-1", AppID: appID})
	case path == "/v1/apps/alpha/consumers/c-other":
		encode(api.APIConsumerResponse{ID: "c-other", AppID: "another-app"})
	case path == "/v1/apps/alpha/consumers" && r.Method == http.MethodPost:
		var req api.CreateAPIConsumerRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.consumers = append(f.consumers, req)
		encode(api.APIConsumerResponse{ID: "c-new", AppID: appID, Name: req.Name})
	case path == "/v1/audit-events":
		f.auditAppIDs = append(f.auditAppIDs, r.URL.Query().Get("app_id"))
		encode(api.ListAuditEventsResponse{})
	case path == "/v1/audit-log":
		f.auditLogs++
		encode(api.ListAuditLogResponse{})
	case path == "/v1/billing/budgets/b-notify":
		encode(api.FinancialBudgetResponse{ID: "b-notify", Revision: 3, Spec: financial.BudgetSpec{Name: "egress", Currency: "usd", Scope: financial.BudgetScope{Kind: "account"}, Meters: []string{"egress"}, Basis: "gross_usage", LimitMillicents: 100, NotifyMillicents: []int64{50}, Mode: "monitored", Action: "notify", ResumeRule: "manual", Enabled: false}})
	case path == "/v1/billing/budgets/b-strict":
		encode(api.FinancialBudgetResponse{ID: "b-strict", Revision: 2, Spec: financial.BudgetSpec{Scope: financial.BudgetScope{Kind: "account"}, Mode: "strict", Action: "reject_traffic"}})
	case path == "/v1/billing/forecast":
		allocations := make([]financial.Allocation, 20000)
		encode(api.FinancialForecastResponse{Currency: "usd", Meters: []api.FinancialMeterCosts{{Meter: "compute", Accrued: financial.ContractCosts{Meter: "compute", NetMillicents: 1200, Contracts: []financial.MeterCost{{NetMillicents: 1200, Allocations: allocations}}}}}})
	case path == "/v1/billing/budgets/preview":
		encode(api.FinancialBudgetPreviewResponse{})
	case path == "/v1/billing/budgets" && r.Method == http.MethodPost:
		var req api.CreateFinancialBudgetRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.budgets = append(f.budgets, req)
		encode(api.FinancialBudgetResponse{ID: "b-new", Spec: req.Spec})
	default:
		f.vmu.Unlock()
		f.serveHTTP(w, r)
		f.vmu.Lock()
	}
}

func TestEventRecoveryPlanIsStrictAndKeyed(t *testing.T) {
	m, f := newEventsFixture(t, nil, nil)
	for _, rec := range []map[string]any{
		{"mode": "routing", "webhook": "x"},
		{"mode": "routing", "request_id": "mine"},
	} {
		if _, err := m.planEventChange(t.Context(), planEventArgs{App: "alpha", Op: "recover", Recovery: rec, Reason: "x"}); err == nil {
			t.Errorf("accepted recovery %v", rec)
		}
	}
	plan, err := m.planEventChange(t.Context(), planEventArgs{App: "alpha", Op: "recover", Recovery: map[string]any{"mode": "routing", "subscription_id": "sub-1"}, Reason: "consumer fixed"})
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := json.Marshal(plan); !strings.Contains(string(raw), "Replays 3 matched events") {
		t.Fatalf("plan = %s", raw)
	}
	if len(f.recoveries) != 0 {
		t.Fatal("planning started a recovery")
	}
	id := plan.(releasePlan).ID
	if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: id}); err != nil {
		t.Fatal(err)
	}
	if len(f.recoveries) != 1 || f.recoveries[0].RequestID != "mcp-operation-"+id || f.recoveries[0].Reason != "consumer fixed" {
		t.Fatalf("recoveries = %+v", f.recoveries)
	}
	if _, err := m.planEventChange(t.Context(), planEventArgs{App: "alpha", Op: "cancel_recovery", RecoveryID: "job-other", Reason: "x"}); err == nil {
		t.Fatal("another app's recovery accepted")
	}
	if _, err := m.planEventChange(t.Context(), planEventArgs{App: "alpha", Op: "pause", Subscription: "sub-other", Reason: "x"}); err == nil {
		t.Fatal("another app's subscription accepted")
	}
}

func TestAPIConsumerChangesStayInTheApp(t *testing.T) {
	m, f := newEventsFixture(t, nil, nil)
	if _, err := m.planAPIConsumerChange(t.Context(), planConsumerArgs{App: "alpha", Op: "revoke", Consumer: "c-other", Reason: "x"}); err == nil {
		t.Fatal("another app's consumer accepted")
	}
	if _, err := m.planAPIConsumerChange(t.Context(), planConsumerArgs{App: "alpha", Op: "set_spend_cap", Consumer: "c-1", Reason: "x"}); err == nil {
		t.Fatal("spend cap without currency accepted")
	}
	plan, err := m.planAPIConsumerChange(t.Context(), planConsumerArgs{App: "alpha", Op: "create", ExternalRef: "cus_1", Name: "Acme", Reason: "onboard"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: plan.(releasePlan).ID}); err != nil {
		t.Fatal(err)
	}
	if len(f.consumers) != 1 || f.consumers[0].ExternalRef != "cus_1" {
		t.Fatalf("consumers = %+v", f.consumers)
	}
}

func TestAuditEventsFollowAppConsent(t *testing.T) {
	m, f := newEventsFixture(t, nil, map[string]string{"alpha": "app-one"})
	if _, err := m.listAuditEvents(t.Context(), auditArgs{}); err == nil || !strings.Contains(err.Error(), "limited to some apps") {
		t.Fatalf("account log on a limited connection: %v", err)
	}
	if _, err := m.listAuditEvents(t.Context(), auditArgs{App: "alpha", KindPrefix: "deploy."}); err != nil {
		t.Fatal(err)
	}
	if len(f.auditAppIDs) != 1 || f.auditAppIDs[0] != "app-one" || f.auditLogs != 0 {
		t.Fatalf("app ids %v, logs %d", f.auditAppIDs, f.auditLogs)
	}
	if _, err := m.listAuditEvents(t.Context(), auditArgs{App: "alpha", KindPrefix: "Deploy&"}); err == nil {
		t.Fatal("bad kind prefix accepted")
	}
}

func TestBudgetsNotifyOnly(t *testing.T) {
	m, _ := newEventsFixture(t, nil, nil)
	if m.toolNames["gregale_plan_budget_change"] || m.toolNames["gregale_get_spend_forecast"] || !m.toolNames["gregale_list_audit_events"] {
		t.Fatal("spend tools listed without usage:read")
	}
	m, f := newEventsFixture(t, []string{api.ScopeUsageRead}, nil)
	if _, err := m.planBudgetChange(t.Context(), planBudgetArgs{Op: "update", BudgetID: "b-strict", Name: "x", Currency: "usd", LimitMillicents: 1, Reason: "x"}); err == nil {
		t.Fatal("update of an enforcing budget accepted")
	}
	plan, err := m.planBudgetChange(t.Context(), planBudgetArgs{Op: "create", Scope: "account", Name: "Monthly", Currency: "usd", LimitMillicents: 5_000_000, NotifyMillicents: []int64{2_500_000}, Reason: "watch spend"})
	if err != nil {
		t.Fatal(err)
	}
	id := plan.(budgetPlan).ID
	for range 2 {
		if _, err := m.applyBudgetChange(t.Context(), applyArgs{PlanID: id}); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.budgets) != 1 || f.budgets[0].Spec.Action != "notify" || f.budgets[0].Spec.Mode != "monitored" || f.budgets[0].Spec.Scope.Kind != "account" {
		t.Fatalf("budgets = %+v", f.budgets)
	}
	update, err := m.planBudgetChange(t.Context(), planBudgetArgs{Op: "update", BudgetID: "b-notify", LimitMillicents: 900, Reason: "raise"})
	if err != nil {
		t.Fatal(err)
	}
	got := update.(budgetPlan).Spec
	if got.LimitMillicents != 900 || got.Enabled || got.Basis != "gross_usage" || len(got.Meters) != 1 || got.Meters[0] != "egress" || got.Name != "egress" || len(got.NotifyMillicents) != 1 {
		t.Fatalf("update dropped fields: %+v", got)
	}
	limited, _ := newEventsFixture(t, []string{api.ScopeUsageRead}, map[string]string{"alpha": "app-one"})
	if _, err := limited.planBudgetChange(t.Context(), planBudgetArgs{Op: "create", Scope: "account", Name: "x", Currency: "usd", LimitMillicents: 1, Reason: "x"}); err == nil {
		t.Fatal("account budget on a limited connection accepted")
	}
}

// The forecast's per-usage allocations were over 300 KB on an ordinary
// account, so gregale_get_spend_forecast always failed with
// response_too_large. They are omitted and counted.
func TestSpendForecastOmitsAllocations(t *testing.T) {
	m, _ := newEventsFixture(t, []string{api.ScopeUsageRead}, nil)
	out, err := m.getSpendForecast(t.Context(), forecastArgs{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	if len(raw) > maxResultBytes/8 {
		t.Fatalf("forecast is %d bytes", len(raw))
	}
	f := out.(spendForecast)
	if f.AllocationsOmitted != 20000 || f.Meters[0].Accrued.NetMillicents != 1200 || f.Meters[0].Accrued.Contracts[0].Allocations != nil {
		t.Fatalf("forecast = %+v", f)
	}
}
