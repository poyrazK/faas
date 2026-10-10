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

// adr: 939 — a tenant allowance prices a calendar month across apps, and
// late usage re-prices the month as a non-negative adjustment.
func TestPlatformTenantMonthStatementAllowanceAndAdjustment(t *testing.T) {
	e := setup(t, api.PlanHobby)
	ctx := context.Background()
	appA, appB := mustSeedApp(t, e, "tenant-month-a"), mustSeedApp(t, e, "tenant-month-b")
	tenant, _, err := e.store.CreatePlatformTenant(ctx, e.acct.ID, "month-customer", "Month Customer", 250)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	end := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	start := end.AddDate(0, -1, 0)
	// An app allowance no longer blocks the statement: the tenant card prices every minute.
	if _, err := e.store.CreateAPIConsumerRateCardVersion(ctx, state.APIConsumerRateCardInput{AccountID: e.acct.ID, AppID: appA,
		Currency: "EUR", PriceMillicentsPerUnit: 5, IncludedUnitsPerMonth: 1000, EffectiveFrom: start}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreatePlatformTenantRateCardVersion(ctx, state.PlatformTenantRateCardInput{AccountID: e.acct.ID, TenantID: tenant.ID,
		Currency: "EUR", PriceMillicentsPerUnit: 100, IncludedUnitsPerMonth: 10, EffectiveFrom: start}); err != nil {
		t.Fatal(err)
	}
	record := func(appID string, minute time.Time, units int64) {
		t.Helper()
		consumer, err := e.store.CreateAPIConsumer(ctx, e.acct.ID, appID, "month-customer-"+uuid.NewString()[:8], "Month Customer")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.store.RecordAPIConsumerUsage(ctx, state.APIConsumerUsageEvent{EventID: uuid.NewString(), AccountID: e.acct.ID,
			AppID: appID, ConsumerKey: consumer.ID, PlatformTenantID: tenant.ID, WindowStart: minute,
			RequestCount: units, BillableUnits: units}); err != nil {
			t.Fatal(err)
		}
	}
	record(appA, start.Add(10*time.Minute), 6)
	record(appB, start.Add(20*time.Minute), 6)

	path := "/v1/account/platform-tenants/" + tenant.ID + "/usage-statements"
	week := start.AddDate(0, 0, 7)
	if res := e.do(t, http.MethodPost, path, api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &start, PeriodEnd: &week}, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("weekly period under a tenant allowance: %d %s, want 422", res.Code, res.Body)
	}
	period := api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &start, PeriodEnd: &end}
	res := e.do(t, http.MethodPost, path, period, nil)
	var first api.PlatformTenantStatementResponse
	if err := json.Unmarshal(res.Body.Bytes(), &first); err != nil || res.Code != http.StatusCreated {
		t.Fatalf("month statement: %d %s", res.Code, res.Body)
	}
	if first.BillableUnits != 12 || first.AmountMillicents != 200 {
		t.Fatalf("month statement = %+v, want 12 units with 10 free = 200", first)
	}
	if res := e.do(t, http.MethodPost, path+"/"+first.ID+"/finalize", struct{}{}, nil); res.Code != http.StatusOK {
		t.Fatalf("finalize: %d %s", res.Code, res.Body)
	}

	record(appA, start.Add(5*time.Minute), 3)
	res = e.do(t, http.MethodPost, path, period, nil)
	var adjustment api.PlatformTenantStatementResponse
	if err := json.Unmarshal(res.Body.Bytes(), &adjustment); err != nil || res.Code != http.StatusCreated {
		t.Fatalf("adjustment: %d %s", res.Code, res.Body)
	}
	negative := false
	for _, line := range adjustment.Lines {
		negative = negative || line.BillableUnits < 0
	}
	if adjustment.Revision != 2 || adjustment.BillableUnits != 3 || adjustment.AmountMillicents != 300 || !negative {
		t.Fatalf("adjustment = %+v, want +3 units billing 300 with a negative free line", adjustment)
	}
}

// adr: 939
func TestPlatformTenantRateCardAllowanceAndTiersAPI(t *testing.T) {
	e := setup(t, api.PlanHobby)
	tenant, _, err := e.store.CreatePlatformTenant(context.Background(), e.acct.ID, "tiered-customer", "Tiered Customer", 250)
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/account/platform-tenants/" + tenant.ID + "/rate-cards"
	past := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	if res := e.do(t, http.MethodPost, path, api.CreatePlatformTenantRateCardRequest{Currency: "EUR", PriceMillicentsPerUnit: 10,
		IncludedUnitsPerMonth: 100, EffectiveFrom: &past}, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("backdated allowance: %d %s, want 422", res.Code, res.Body)
	}
	thousand := int64(1000)
	ladder := []api.APIConsumerRateCardTier{{UpTo: &thousand, PriceMillicentsPerUnit: 0}, {PriceMillicentsPerUnit: 20}}
	if res := e.do(t, http.MethodPost, path, api.CreatePlatformTenantRateCardRequest{Currency: "EUR", IncludedUnitsPerMonth: 5, Tiers: ladder}, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("tiers with included units: %d %s, want 422", res.Code, res.Body)
	}
	res := e.do(t, http.MethodPost, path, api.CreatePlatformTenantRateCardRequest{Currency: "EUR", Tiers: ladder}, nil)
	var card api.PlatformTenantRateCardResponse
	if err := json.Unmarshal(res.Body.Bytes(), &card); err != nil || res.Code != http.StatusCreated || len(card.Tiers) != 2 || card.PriceMillicentsPerUnit != 20 {
		t.Fatalf("tiered card: %d %s", res.Code, res.Body)
	}
	if res := e.do(t, http.MethodPost, path, api.CreatePlatformTenantRateCardRequest{Currency: "EUR", PriceMillicentsPerUnit: 5, EffectiveFrom: &past}, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("backdated flat card after a tiered one: %d %s, want 422", res.Code, res.Body)
	}
}
