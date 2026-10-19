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

// adr: 20261019100331 — a tenant's spend cap counts its usage across apps
// after prepaid credit and refuses it through tenant admission; new credit
// lifts the cap, and finalizing draws the credit into the statement.
func TestPlatformTenantCreditAndSpendCap(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "tenant-credit")
	tenant := tenantJSON[api.PlatformTenantResponse](t, e, http.MethodPost, "/v1/account/platform-tenants",
		api.CreatePlatformTenantRequest{ExternalRef: "credit-co", Name: "Credit Co"}, http.StatusCreated)
	base := "/v1/account/platform-tenants/" + tenant.ID
	ctx := context.Background()
	store := *e.Store()
	acct := (*e.Acct()).ID

	tenantJSON[api.Problem](t, e, http.MethodPost, base+"/credits", api.CreateAPIConsumerCreditRequest{Currency: "eur", AmountMillicents: 1},
		http.StatusUnprocessableEntity)
	first := tenantJSON[api.PlatformTenantCreditResponse](t, e, http.MethodPost, base+"/credits",
		api.CreateAPIConsumerCreditRequest{Currency: "EUR", AmountMillicents: 100000, Note: "prepay"}, http.StatusCreated)
	if first.TenantID != tenant.ID || first.Status != api.ConsumerCreditActive || first.RemainingMillicents != 100000 {
		t.Fatalf("credit = %+v", first)
	}

	card := tenantJSON[api.PlatformTenantRateCardResponse](t, e, http.MethodPost, base+"/rate-cards",
		api.CreatePlatformTenantRateCardRequest{Currency: "EUR", PriceMillicentsPerUnit: 1250}, http.StatusCreated)
	minute := card.EffectiveFrom.Add(time.Minute)
	if _, err := store.RecordAPIConsumerUsage(ctx, state.APIConsumerUsageEvent{EventID: uuid.NewString(), AccountID: acct, AppID: appID,
		ConsumerKey: state.AnonymousConsumerKey, PlatformTenantID: tenant.ID, PlatformTenantSurfaceID: uuid.NewString(),
		WindowStart: minute, RequestCount: 100, BillableUnits: 100}); err != nil {
		t.Fatal(err)
	}

	// EUR 1.25 of usage against EUR 1.00 of credit passes a zero cap.
	zero := int64(0)
	tenantJSON[api.Problem](t, e, http.MethodPut, base+"/spend-cap", api.PutAPIConsumerSpendCapRequest{Currency: "EUR"},
		http.StatusUnprocessableEntity)
	capped := tenantJSON[api.PlatformTenantSpendCapResponse](t, e, http.MethodPut, base+"/spend-cap",
		api.PutAPIConsumerSpendCapRequest{Currency: "EUR", MonthlyCapMillicents: &zero}, http.StatusOK)
	if !capped.Capped || capped.AmountDueMillicents == nil || *capped.AmountDueMillicents != 25000 || capped.TenantID != tenant.ID {
		t.Fatalf("spend cap = %+v", capped)
	}
	decision, err := store.AdmitPlatformTenantRequest(ctx, acct, tenant.ID)
	if err != nil || decision.Allowed || decision.Scope != state.PlatformTenantBudgetScopeSpendCap || decision.Observed != 25000 ||
		decision.SpendCurrency != "EUR" {
		t.Fatalf("capped tenant admission = %+v, %v", decision, err)
	}

	second := tenantJSON[api.PlatformTenantCreditResponse](t, e, http.MethodPost, base+"/credits",
		api.CreateAPIConsumerCreditRequest{Currency: "EUR", AmountMillicents: 50000}, http.StatusCreated)
	if decision, err := store.AdmitPlatformTenantRequest(ctx, acct, tenant.ID); err != nil || !decision.Allowed {
		t.Fatalf("new credit did not lift the cap: %+v, %v", decision, err)
	}
	if list := tenantJSON[api.PlatformTenantCreditListResponse](t, e, http.MethodGet, base+"/credits", nil, http.StatusOK); len(list.Credits) != 2 ||
		len(list.Balances) != 1 || list.Balances[0].AvailableMillicents != 150000 {
		t.Fatalf("credits = %+v", list)
	}

	start := billing.MonthStart(minute)
	end := start.AddDate(0, 1, 0)
	statement := tenantJSON[api.PlatformTenantStatementResponse](t, e, http.MethodPost, base+"/usage-statements",
		api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &start, PeriodEnd: &end}, http.StatusCreated)
	final := tenantJSON[api.PlatformTenantStatementResponse](t, e, http.MethodPost, base+"/usage-statements/"+statement.ID+"/finalize", nil, http.StatusOK)
	if final.AmountMillicents != 125000 || final.CreditAppliedMillicents != 125000 || final.AmountDueMillicents != 0 {
		t.Fatalf("finalized statement = %+v", final)
	}

	// Credit already applied this month still counts: the worker finds
	// nothing due.
	service := (*e.S()).ConsumerCredits()
	service.Now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if err := service.CheckOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := tenantJSON[api.PlatformTenantSpendCapResponse](t, e, http.MethodGet, base+"/spend-cap", nil, http.StatusOK); got.Capped ||
		got.AmountDueMillicents == nil || *got.AmountDueMillicents != 0 {
		t.Fatalf("spend cap after worker check = %+v", got)
	}
	voided := tenantJSON[api.PlatformTenantCreditResponse](t, e, http.MethodDelete, base+"/credits/"+second.ID, nil, http.StatusOK)
	if voided.Status != api.ConsumerCreditVoided || voided.RemainingMillicents != 0 {
		t.Fatalf("voided = %+v", voided)
	}
	if res := e.Do(t, http.MethodDelete, base+"/spend-cap", nil, nil); res.Code != http.StatusNoContent {
		t.Fatalf("delete spend cap: %d %s", res.Code, res.Body)
	}
	tenantJSON[api.Problem](t, e, http.MethodGet, base+"/spend-cap", nil, http.StatusNotFound)
}
