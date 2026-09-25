package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPlatformTenantRateCardAPIAndStatementPricing(t *testing.T) {
	e := setup(t, api.PlanHobby)
	appID := mustSeedApp(t, e, "tenant-rate-card")
	tenant, _, err := e.store.CreatePlatformTenant(context.Background(), e.acct.ID, "commercial-customer", "Commercial Customer", 250)
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := e.store.CreateAPIConsumer(context.Background(), e.acct.ID, appID, "commercial-customer", "Commercial Customer")
	if err != nil {
		t.Fatal(err)
	}
	minute := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Minute)
	appCard, err := e.store.CreateAPIConsumerRateCard(context.Background(), e.acct.ID, appID, "EUR", 10, minute)
	if err != nil {
		t.Fatal(err)
	}
	tenantRatePath := "/v1/account/platform-tenants/" + tenant.ID + "/rate-cards"
	tenantEffective := minute.Add(time.Minute)
	createdCard := e.do(t, http.MethodPost, tenantRatePath, api.CreatePlatformTenantRateCardRequest{
		Currency: "eur", PriceMillicentsPerUnit: 25, EffectiveFrom: &tenantEffective,
	}, nil)
	if createdCard.Code != http.StatusCreated {
		t.Fatalf("create tenant rate card: %d %s", createdCard.Code, createdCard.Body)
	}
	var tenantRate api.PlatformTenantRateCardResponse
	if err := json.Unmarshal(createdCard.Body.Bytes(), &tenantRate); err != nil {
		t.Fatal(err)
	}
	if tenantRate.TenantID != tenant.ID || tenantRate.Currency != "EUR" || tenantRate.Unit != "request" || tenantRate.ID == "" {
		t.Fatalf("tenant rate card = %+v", tenantRate)
	}
	listed := e.do(t, http.MethodGet, tenantRatePath, nil, nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("list tenant rate cards: %d %s", listed.Code, listed.Body)
	}
	var history api.PlatformTenantRateCardListResponse
	if err := json.Unmarshal(listed.Body.Bytes(), &history); err != nil || len(history.RateCards) != 1 || history.RateCards[0].ID != tenantRate.ID {
		t.Fatalf("rate-card history = %+v, err=%v", history, err)
	}
	changedEffective := tenantEffective.Add(time.Minute)
	changedCurrency := e.do(t, http.MethodPost, tenantRatePath, api.CreatePlatformTenantRateCardRequest{
		Currency: "USD", PriceMillicentsPerUnit: 30, EffectiveFrom: &changedEffective,
	}, nil)
	if changedCurrency.Code != http.StatusUnprocessableEntity {
		t.Fatalf("tenant currency change: %d %s", changedCurrency.Code, changedCurrency.Body)
	}

	for i, usage := range []struct {
		at    time.Time
		units int64
	}{{minute, 1}, {tenantEffective, 2}} {
		if _, err := e.store.RecordAPIConsumerUsage(context.Background(), state.APIConsumerUsageEvent{
			EventID: uuid.NewString(), AccountID: e.acct.ID, AppID: appID, ConsumerKey: consumer.ID,
			PlatformTenantID: tenant.ID, WindowStart: usage.at, RequestCount: usage.units, BillableUnits: usage.units,
		}); err != nil {
			t.Fatalf("record usage %d: %v", i, err)
		}
	}
	end := minute.Add(time.Hour)
	statementPath := "/v1/account/platform-tenants/" + tenant.ID + "/usage-statements"
	statementResp := e.do(t, http.MethodPost, statementPath, api.CreateAPIConsumerUsageStatementRequest{
		PeriodStart: &minute, PeriodEnd: &end,
	}, nil)
	if statementResp.Code != http.StatusCreated {
		t.Fatalf("create customer statement: %d %s", statementResp.Code, statementResp.Body)
	}
	var statement api.PlatformTenantStatementResponse
	if err := json.Unmarshal(statementResp.Body.Bytes(), &statement); err != nil {
		t.Fatal(err)
	}
	if statement.Currency != "EUR" || statement.BillableUnits != 3 || statement.AmountMillicents != 60 || len(statement.Lines) != 2 {
		t.Fatalf("cross-app customer pricing = %+v", statement)
	}
	if statement.Lines[0].RateCardID != appCard.ID || statement.Lines[0].PlatformTenantRateCardID != "" || statement.Lines[0].AmountMillicents != 10 {
		t.Fatalf("pre-effective line = %+v", statement.Lines[0])
	}
	if statement.Lines[1].RateCardID != "" || statement.Lines[1].PlatformTenantRateCardID != tenantRate.ID || statement.Lines[1].AmountMillicents != 50 {
		t.Fatalf("tenant-priced line = %+v", statement.Lines[1])
	}
}
