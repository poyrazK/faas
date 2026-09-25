package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 238 — one platform-customer statement across apps with additive late-usage adjustments.
func TestPlatformTenantStatementCrossAppAndLateUsage(t *testing.T) {
	e := setup(t, api.PlanHobby)
	appA, appB := mustSeedApp(t, e, "tenant-statement-a"), mustSeedApp(t, e, "tenant-statement-b")
	tenant, _, err := e.store.CreatePlatformTenant(context.Background(), e.acct.ID, "billing-customer", "Billing Customer", 250)
	if err != nil {
		t.Fatal(err)
	}
	consumerA, err := e.store.CreateAPIConsumer(context.Background(), e.acct.ID, appA, "billing-customer", "Billing Customer")
	if err != nil {
		t.Fatal(err)
	}
	consumerB, err := e.store.CreateAPIConsumer(context.Background(), e.acct.ID, appB, "billing-customer", "Billing Customer")
	if err != nil {
		t.Fatal(err)
	}
	minute := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Minute)
	for _, pair := range []struct {
		appID string
		price int64
	}{{appA, 10}, {appB, 25}} {
		if _, err := e.store.CreateAPIConsumerRateCard(context.Background(), e.acct.ID, pair.appID, "EUR", pair.price, minute); err != nil {
			t.Fatal(err)
		}
	}
	for _, pair := range []struct {
		appID, consumerID string
		units             int64
	}{{appA, consumerA.ID, 3}, {appB, consumerB.ID, 2}} {
		if _, err := e.store.RecordAPIConsumerUsage(context.Background(), state.APIConsumerUsageEvent{
			EventID: uuid.NewString(), AccountID: e.acct.ID, AppID: pair.appID, ConsumerKey: pair.consumerID,
			PlatformTenantID: tenant.ID, WindowStart: minute, RequestCount: pair.units, BillableUnits: pair.units,
		}); err != nil {
			t.Fatal(err)
		}
	}
	end := minute.Add(time.Hour)
	path := "/v1/account/platform-tenants/" + tenant.ID + "/usage-statements"
	period := api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &minute, PeriodEnd: &end}
	created := e.do(t, http.MethodPost, path, period, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body)
	}
	var first api.PlatformTenantStatementResponse
	if err := json.Unmarshal(created.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.Revision != 1 || first.AmountMillicents != 80 || len(first.Lines) != 2 || first.UnpricedUnits != 0 {
		t.Fatalf("cross-app statement = %+v", first)
	}
	listed := e.do(t, http.MethodGet, path+"?period_start="+url.QueryEscape(minute.Format(time.RFC3339))+"&period_end="+url.QueryEscape(end.Format(time.RFC3339)), nil, nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("list: %d %s", listed.Code, listed.Body)
	}
	if got := e.do(t, http.MethodPost, path+"/"+first.ID+"/finalize", struct{}{}, nil); got.Code != http.StatusOK {
		t.Fatalf("finalize: %d %s", got.Code, got.Body)
	}
	claim := e.do(t, http.MethodPost, path+"/"+first.ID+"/handoff", api.ClaimAPIConsumerUsageStatementRequest{ExternalInvoiceID: "tenant-invoice-1"}, nil)
	if claim.Code != http.StatusCreated {
		t.Fatalf("handoff: %d %s", claim.Code, claim.Body)
	}
	appStatement, _, err := e.store.CreateAPIConsumerUsageStatement(context.Background(), state.APIConsumerUsageStatementInput{
		AccountID: e.acct.ID, AppID: appA, ConsumerID: consumerA.ID, PeriodStart: minute, PeriodEnd: end,
		Currency: "EUR", BillableUnits: 3, AmountMillicents: 30, Priced: true, AsOf: time.Now().UTC(),
		Buckets: []state.APIConsumerUsageStatementBucket{{WindowStart: minute, BillableUnits: 3, RateCardID: first.Lines[0].RateCardID,
			Currency: "EUR", PriceMillicentsPerUnit: 10, AmountMillicents: 30}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.store.FinalizeAPIConsumerUsageStatement(context.Background(), e.acct.ID, appA, consumerA.ID, appStatement.ID); err != nil {
		t.Fatal(err)
	}
	appClaim := e.do(t, http.MethodPost, "/v1/apps/tenant-statement-a/consumers/"+consumerA.ID+"/usage-statements/"+appStatement.ID+"/handoff",
		api.ClaimAPIConsumerUsageStatementRequest{ExternalInvoiceID: "app-invoice-overlap"}, nil)
	if appClaim.Code != http.StatusConflict {
		t.Fatalf("app overlap should conflict: %d %s", appClaim.Code, appClaim.Body)
	}
	if _, err := e.store.RecordAPIConsumerUsage(context.Background(), state.APIConsumerUsageEvent{
		EventID: uuid.NewString(), AccountID: e.acct.ID, AppID: appB, ConsumerKey: consumerB.ID,
		PlatformTenantID: tenant.ID, WindowStart: minute, RequestCount: 1, BillableUnits: 1,
	}); err != nil {
		t.Fatal(err)
	}
	adjusted := e.do(t, http.MethodPost, path, period, nil)
	if adjusted.Code != http.StatusCreated {
		t.Fatalf("adjustment: %d %s", adjusted.Code, adjusted.Body)
	}
	var second api.PlatformTenantStatementResponse
	if err := json.Unmarshal(adjusted.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if second.Revision != 2 || second.BillableUnits != 1 || second.AmountMillicents != 25 || len(second.Lines) != 1 || second.Lines[0].AppID != appB {
		t.Fatalf("adjustment = %+v", second)
	}
	if got := e.do(t, http.MethodPost, path+"/"+second.ID+"/finalize", struct{}{}, nil); got.Code != http.StatusOK {
		t.Fatalf("adjustment finalize: %d %s", got.Code, got.Body)
	}
	if got := e.do(t, http.MethodPost, path+"/"+second.ID+"/handoff", api.ClaimAPIConsumerUsageStatementRequest{ExternalInvoiceID: "tenant-invoice-2"}, nil); got.Code != http.StatusCreated {
		t.Fatalf("adjustment handoff: %d %s", got.Code, got.Body)
	}
}

func TestPlatformTenantStatementSupersedesUnpricedDraft(t *testing.T) {
	e := setup(t, api.PlanHobby)
	appID := mustSeedApp(t, e, "tenant-draft-reprice")
	tenant, _, err := e.store.CreatePlatformTenant(context.Background(), e.acct.ID, "draft-customer", "Draft Customer", 250)
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := e.store.CreateAPIConsumer(context.Background(), e.acct.ID, appID, "draft-customer", "Draft Customer")
	if err != nil {
		t.Fatal(err)
	}
	minute := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Minute)
	if _, err := e.store.RecordAPIConsumerUsage(context.Background(), state.APIConsumerUsageEvent{
		EventID: uuid.NewString(), AccountID: e.acct.ID, AppID: appID, ConsumerKey: consumer.ID,
		PlatformTenantID: tenant.ID, WindowStart: minute, RequestCount: 2, BillableUnits: 2,
	}); err != nil {
		t.Fatal(err)
	}
	end := minute.Add(time.Hour)
	path := "/v1/account/platform-tenants/" + tenant.ID + "/usage-statements"
	period := api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &minute, PeriodEnd: &end}
	firstResp := e.do(t, http.MethodPost, path, period, nil)
	if firstResp.Code != http.StatusCreated {
		t.Fatalf("unpriced create: %d %s", firstResp.Code, firstResp.Body)
	}
	var first api.PlatformTenantStatementResponse
	if err := json.Unmarshal(firstResp.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.UnpricedUnits != 2 || first.Revision != 1 {
		t.Fatalf("unpriced first = %+v", first)
	}
	if got := e.do(t, http.MethodPost, path+"/"+first.ID+"/finalize", struct{}{}, nil); got.Code != http.StatusConflict {
		t.Fatalf("unpriced finalize: %d %s", got.Code, got.Body)
	}
	if _, err := e.store.CreateAPIConsumerRateCard(context.Background(), e.acct.ID, appID, "EUR", 10, minute); err != nil {
		t.Fatal(err)
	}
	secondResp := e.do(t, http.MethodPost, path, period, nil)
	if secondResp.Code != http.StatusCreated {
		t.Fatalf("reprice create: %d %s", secondResp.Code, secondResp.Body)
	}
	var second api.PlatformTenantStatementResponse
	if err := json.Unmarshal(secondResp.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if second.Revision != 2 || second.UnpricedUnits != 0 || second.AmountMillicents != 20 {
		t.Fatalf("repriced second = %+v", second)
	}
	old := e.do(t, http.MethodGet, path+"/"+first.ID, nil, nil)
	if old.Code != http.StatusOK {
		t.Fatalf("get superseded: %d %s", old.Code, old.Body)
	}
	var superseded api.PlatformTenantStatementResponse
	if err := json.Unmarshal(old.Body.Bytes(), &superseded); err != nil || superseded.Status != "superseded" || superseded.UnpricedUnits != 2 {
		t.Fatalf("superseded = %+v err=%v", superseded, err)
	}
	if got := e.do(t, http.MethodPost, path+"/"+second.ID+"/finalize", struct{}{}, nil); got.Code != http.StatusOK {
		t.Fatalf("repriced finalize: %d %s", got.Code, got.Body)
	}
}

// adr: 239
func TestPlatformTenantSurfaceUsageAndStatementAPI(t *testing.T) {
	e := setup(t, api.PlanHobby)
	appID := mustSeedApp(t, e, "tenant-surface-billing")
	tenant, _, err := e.store.CreatePlatformTenant(context.Background(), e.acct.ID, "surface-customer", "Surface Customer", 250)
	if err != nil {
		t.Fatal(err)
	}
	surfaceID := uuid.NewString()
	minute := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Minute)
	if _, err := e.store.CreateAPIConsumerRateCard(context.Background(), e.acct.ID, appID, "EUR", 11, minute); err != nil {
		t.Fatal(err)
	}
	for _, units := range []int64{2} {
		if _, err := e.store.RecordAPIConsumerUsage(context.Background(), state.APIConsumerUsageEvent{
			EventID: uuid.NewString(), AccountID: e.acct.ID, AppID: appID,
			ConsumerKey: state.AnonymousConsumerKey, PlatformTenantID: tenant.ID,
			PlatformTenantSurfaceID: surfaceID, WindowStart: minute,
			RequestCount: units, BillableUnits: units,
		}); err != nil {
			t.Fatal(err)
		}
	}
	usagePath := "/v1/account/platform-tenants/" + tenant.ID + "/usage"
	if rows, err := e.store.ListPlatformTenantUsage(context.Background(), e.acct.ID, tenant.ID, minute, minute.Add(time.Hour)); err != nil || len(rows) != 1 {
		t.Fatalf("direct tenant surface usage=%+v err=%v", rows, err)
	}
	usage := e.do(t, http.MethodGet, usagePath+"?since="+url.QueryEscape(minute.Format(time.RFC3339))+"&until="+url.QueryEscape(minute.Add(24*time.Hour).Format(time.RFC3339)), nil, nil)
	if usage.Code != http.StatusOK {
		t.Fatalf("surface usage: %d %s", usage.Code, usage.Body)
	}
	var usageBody api.PlatformTenantUsageResponse
	if err := json.Unmarshal(usage.Body.Bytes(), &usageBody); err != nil || len(usageBody.Buckets) != 1 || usageBody.Buckets[0].SurfaceID != surfaceID || usageBody.Buckets[0].ConsumerID != "" {
		t.Fatalf("surface usage response=%+v err=%v", usageBody, err)
	}
	end := minute.Add(time.Hour)
	path := "/v1/account/platform-tenants/" + tenant.ID + "/usage-statements"
	period := api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &minute, PeriodEnd: &end}
	created := e.do(t, http.MethodPost, path, period, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("surface statement: %d %s", created.Code, created.Body)
	}
	var first api.PlatformTenantStatementResponse
	if err := json.Unmarshal(created.Body.Bytes(), &first); err != nil || first.BillableUnits != 2 || first.AmountMillicents != 22 || len(first.Lines) != 1 || first.Lines[0].SurfaceID != surfaceID || first.Lines[0].ConsumerID != "" {
		t.Fatalf("surface statement response=%+v err=%v", first, err)
	}
	if got := e.do(t, http.MethodPost, path+"/"+first.ID+"/finalize", struct{}{}, nil); got.Code != http.StatusOK {
		t.Fatalf("surface finalize: %d %s", got.Code, got.Body)
	}
	if _, err := e.store.RecordAPIConsumerUsage(context.Background(), state.APIConsumerUsageEvent{
		EventID: uuid.NewString(), AccountID: e.acct.ID, AppID: appID,
		ConsumerKey: state.AnonymousConsumerKey, PlatformTenantID: tenant.ID,
		PlatformTenantSurfaceID: surfaceID, WindowStart: minute,
		RequestCount: 1, BillableUnits: 1,
	}); err != nil {
		t.Fatal(err)
	}
	adjusted := e.do(t, http.MethodPost, path, period, nil)
	if adjusted.Code != http.StatusCreated {
		t.Fatalf("surface adjustment: %d %s", adjusted.Code, adjusted.Body)
	}
	var second api.PlatformTenantStatementResponse
	if err := json.Unmarshal(adjusted.Body.Bytes(), &second); err != nil || second.Revision != 2 || second.AmountMillicents != 11 || len(second.Lines) != 1 || second.Lines[0].SurfaceID != surfaceID {
		t.Fatalf("surface adjustment response=%+v err=%v", second, err)
	}
}
