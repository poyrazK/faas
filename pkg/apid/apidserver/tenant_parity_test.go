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

// adr: 1319 — a tenant prices requests and app-reported tokens side by side;
// a surface's reported units are billed on the tenant statement.
func TestPlatformTenantUnitStreamsAndAppUnits(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "tenant-tokens")
	tenant := tenantJSON[api.PlatformTenantResponse](t, e, http.MethodPost, "/v1/account/platform-tenants",
		api.CreatePlatformTenantRequest{ExternalRef: "tokens-co", Name: "Tokens Co"}, http.StatusCreated)
	base := "/v1/account/platform-tenants/" + tenant.ID
	if res := e.Do(t, http.MethodPost, base+"/rate-cards", api.CreatePlatformTenantRateCardRequest{Currency: "EUR",
		PriceMillicentsPerUnit: 1, Unit: "app:"}, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty meter: %d %s, want 422", res.Code, res.Body)
	}
	requests := tenantJSON[api.PlatformTenantRateCardResponse](t, e, http.MethodPost, base+"/rate-cards",
		api.CreatePlatformTenantRateCardRequest{Currency: "EUR", PriceMillicentsPerUnit: 10}, http.StatusCreated)
	tokens := tenantJSON[api.PlatformTenantRateCardResponse](t, e, http.MethodPost, base+"/rate-cards",
		api.CreatePlatformTenantRateCardRequest{Currency: "EUR", PriceMillicentsPerUnit: 2, Unit: "app:tokens",
			EffectiveFrom: &requests.EffectiveFrom}, http.StatusCreated)
	if tokens.Unit != "app:tokens" || !tokens.EffectiveFrom.Equal(requests.EffectiveFrom) {
		t.Fatalf("tokens card = %+v", tokens)
	}

	store := *e.Store()
	minute := requests.EffectiveFrom.Add(time.Minute)
	if _, err := store.RecordAPIConsumerUsage(context.Background(), state.APIConsumerUsageEvent{EventID: uuid.NewString(),
		AccountID: (*e.Acct()).ID, AppID: appID, ConsumerKey: state.AnonymousConsumerKey, PlatformTenantID: tenant.ID,
		PlatformTenantSurfaceID: uuid.NewString(), WindowStart: minute, RequestCount: 3, BillableUnits: 3,
		MeterUnits: map[string]int64{"tokens": 250}}); err != nil {
		t.Fatal(err)
	}
	start := billing.MonthStart(minute)
	end := start.AddDate(0, 1, 0)
	statement := tenantJSON[api.PlatformTenantStatementResponse](t, e, http.MethodPost, base+"/usage-statements",
		api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &start, PeriodEnd: &end}, http.StatusCreated)
	// 3 requests at 10 and 250 tokens at 2.
	if statement.AmountMillicents != 30+500 || len(statement.Lines) != 2 {
		t.Fatalf("statement = %+v", statement)
	}
}

// adr: 1322, 1324 — an idle tenant on a fee card gets a fee-only statement;
// a trial is set and ended; the account's overdue rule holds a tenant whose
// exported invoice is unpaid and refuses its traffic until the rule goes.
func TestPlatformTenantFeesTrialAndOverdue(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	tenant := tenantJSON[api.PlatformTenantResponse](t, e, http.MethodPost, "/v1/account/platform-tenants",
		api.CreatePlatformTenantRequest{ExternalRef: "fees-co", Name: "Fees Co"}, http.StatusCreated)
	base := "/v1/account/platform-tenants/" + tenant.ID
	ctx := context.Background()
	store := *e.Store()
	acct := (*e.Acct()).ID
	now := time.Now().UTC()
	start := billing.MonthStart(now)
	end := start.AddDate(0, 1, 0)
	// A card in force since the 1st, before the tenant existed, so its
	// month starts at its creation.
	card, err := store.CreatePlatformTenantRateCardVersion(ctx, state.PlatformTenantRateCardInput{
		AccountID: acct, TenantID: tenant.ID, Currency: "EUR", PriceMillicentsPerUnit: 10, EffectiveFrom: start,
		BaseFeeMillicentsPerMonth: 5000})
	if err != nil {
		t.Fatal(err)
	}
	statement := tenantJSON[api.PlatformTenantStatementResponse](t, e, http.MethodPost, base+"/usage-statements",
		api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &start, PeriodEnd: &end}, http.StatusCreated)
	if statement.AmountMillicents != 5000 || len(statement.Fees) != 1 || statement.Fees[0].RateCardID != card.ID || len(statement.Lines) != 0 {
		t.Fatalf("fee-only statement = %+v", statement)
	}
	tenantJSON[api.PlatformTenantStatementResponse](t, e, http.MethodPost, base+"/usage-statements/"+statement.ID+"/finalize", nil, http.StatusOK)

	trial := tenantJSON[api.PlatformTenantTrialResponse](t, e, http.MethodPut, base+"/trial", api.PutAPIConsumerTrialRequest{Days: 7}, http.StatusOK)
	if !trial.Active || trial.TenantID != tenant.ID || trial.EndsAt.Sub(trial.StartsAt) != 7*24*time.Hour {
		t.Fatalf("trial = %+v", trial)
	}
	tenantJSON[api.PlatformTenantTrialResponse](t, e, http.MethodGet, base+"/trial", nil, http.StatusOK)
	if res := e.Do(t, http.MethodDelete, base+"/trial", nil, nil); res.Code != http.StatusNoContent {
		t.Fatalf("end trial: %d %s", res.Code, res.Body)
	}

	days := 3
	policy := tenantJSON[api.PlatformTenantBillingPolicyResponse](t, e, http.MethodPut, "/v1/account/platform-tenant-billing-policy",
		api.PutAPIConsumerBillingPolicyRequest{BlockOverdueAfterDays: &days}, http.StatusOK)
	if policy.BlockOverdueAfterDays != 3 {
		t.Fatalf("policy = %+v", policy)
	}
	exports := store
	export, _, err := exports.BeginStatementInvoiceExport(ctx, state.StatementInvoiceExport{AccountID: acct,
		SourceKind: state.StatementSourcePlatformTenant, StatementID: statement.ID, Provider: state.BillingProviderStripe,
		Customer: "cus_Fees", Currency: "EUR", AmountMinor: 5})
	if err != nil {
		t.Fatal(err)
	}
	if err := exports.SetStatementInvoiceExportInvoice(ctx, acct, export.ID, "in_fees"); err != nil {
		t.Fatal(err)
	}
	if _, err := exports.CompleteStatementInvoiceExport(ctx, acct, export.ID); err != nil {
		t.Fatal(err)
	}
	due, next := now.AddDate(0, 0, -10), now.AddDate(0, 0, 1)
	if _, err := store.RecordStatementInvoicePayment(ctx, export.ID, state.StatementInvoicePayment{
		Status: state.InvoicePaymentOpen, AmountRemainingMinor: 5, DueAt: &due}, now, &next); err != nil {
		t.Fatal(err)
	}
	if err := (*e.S()).ConsumerControls().SyncOverdueOnce(ctx); err != nil {
		t.Fatal(err)
	}
	holds := tenantJSON[api.PlatformTenantOverdueHoldListResponse](t, e, http.MethodGet, "/v1/account/platform-tenant-overdue-holds", nil, http.StatusOK)
	if len(holds.Holds) != 1 || holds.Holds[0].TenantID != tenant.ID || holds.Holds[0].ExportID != export.ID {
		t.Fatalf("holds = %+v", holds)
	}
	if decision, err := store.AdmitPlatformTenantRequest(ctx, acct, tenant.ID); err != nil ||
		decision.Scope != state.PlatformTenantBudgetScopeOverdue {
		t.Fatalf("held tenant admission = %+v, %v", decision, err)
	}
	if res := e.Do(t, http.MethodDelete, "/v1/account/platform-tenant-billing-policy", nil, nil); res.Code != http.StatusNoContent {
		t.Fatalf("delete policy: %d %s", res.Code, res.Body)
	}
	if err := (*e.S()).ConsumerControls().SyncOverdueOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if holds := tenantJSON[api.PlatformTenantOverdueHoldListResponse](t, e, http.MethodGet, "/v1/account/platform-tenant-overdue-holds",
		nil, http.StatusOK); len(holds.Holds) != 0 {
		t.Fatalf("holds after removing the rule = %+v", holds)
	}
}
