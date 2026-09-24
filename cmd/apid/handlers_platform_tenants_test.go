package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 226 — a platform customer has one account identity across app consumers.
func TestPlatformTenantCrossAppLifecycle(t *testing.T) {
	e := setup(t, api.PlanPro)
	appA := mustSeedApp(t, e, "tenant-api")
	appB := mustSeedApp(t, e, "tenant-worker")
	create := e.do(t, http.MethodPost, "/v1/account/platform-tenants", api.CreatePlatformTenantRequest{
		ExternalRef: "customer-42", Name: "Customer 42",
	}, nil)
	if create.Code != http.StatusCreated {
		t.Fatalf("create tenant: %d %s", create.Code, create.Body.String())
	}
	var tenant api.PlatformTenantResponse
	if err := json.Unmarshal(create.Body.Bytes(), &tenant); err != nil {
		t.Fatal(err)
	}
	if tenant.ID == "" || tenant.Status != "active" {
		t.Fatalf("tenant = %+v", tenant)
	}
	replay := e.do(t, http.MethodPost, "/v1/account/platform-tenants", api.CreatePlatformTenantRequest{
		ExternalRef: "customer-42", Name: "Customer 42",
	}, nil)
	if replay.Code != http.StatusOK {
		t.Fatalf("idempotent external ref: %d %s", replay.Code, replay.Body.String())
	}
	conflict := e.do(t, http.MethodPost, "/v1/account/platform-tenants", api.CreatePlatformTenantRequest{
		ExternalRef: "customer-42", Name: "Another customer",
	}, nil)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflicting external ref: %d %s", conflict.Code, conflict.Body.String())
	}
	consumerA := seedPlatformTenantConsumer(t, e, "tenant-api", tenant.ID)
	consumerB := seedPlatformTenantConsumer(t, e, "tenant-worker", tenant.ID)
	surfaceID := seedTenantSurface(t, e, appA, "customer-42-domain")
	linkedSurface := e.do(t, http.MethodPost, "/v1/account/platform-tenants/"+tenant.ID+"/surfaces",
		api.LinkPlatformTenantSurfaceRequest{SurfaceID: surfaceID}, nil)
	if linkedSurface.Code != http.StatusOK {
		t.Fatalf("link surface: %d %s", linkedSurface.Code, linkedSurface.Body.String())
	}
	detail := e.do(t, http.MethodGet, "/v1/account/platform-tenants/"+tenant.ID, nil, nil)
	if detail.Code != http.StatusOK {
		t.Fatalf("tenant detail: %d %s", detail.Code, detail.Body.String())
	}
	var linked api.PlatformTenantDetailResponse
	if err := json.Unmarshal(detail.Body.Bytes(), &linked); err != nil {
		t.Fatal(err)
	}
	if len(linked.Consumers) != 2 || len(linked.Surfaces) != 1 || linked.Surfaces[0].ID != surfaceID {
		t.Fatalf("tenant links = %+v", linked)
	}
	keyA := seedPlatformTenantKey(t, e, "tenant-api", consumerA)
	keyB := seedPlatformTenantKey(t, e, "tenant-worker", consumerB)
	otherTenant, _, err := e.store.CreatePlatformTenant(context.Background(), e.acct.ID, "customer-43", "Customer 43", 2500)
	if err != nil {
		t.Fatal(err)
	}
	otherConsumer, err := e.store.CreateAPIConsumer(context.Background(), e.acct.ID, appA, "customer-43", "Customer 43")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.LinkPlatformTenantConsumer(context.Background(), e.acct.ID, otherTenant.ID, otherConsumer.ID); err != nil {
		t.Fatal(err)
	}
	_, otherPrefix, otherHash, err := api.GenerateConsumerKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateConsumerKeyForConsumer(context.Background(), e.acct.ID, otherConsumer.ID,
		"other-primary", otherPrefix, otherHash, []string{"read"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.LinkPlatformTenantConsumer(context.Background(), e.acct.ID, otherTenant.ID, consumerA); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("linking consumer to two platform tenants = %v", err)
	}
	if _, err := e.store.LinkPlatformTenantSurface(context.Background(), e.acct.ID, otherTenant.ID, surfaceID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("linking surface to two platform tenants = %v", err)
	}
	if _, err := e.store.ConsumerKeyByAppAndPrefix(context.Background(), e.acct.ID, appA, keyA.Prefix); err != nil {
		t.Fatalf("active key A: %v", err)
	}
	minute := time.Now().UTC().Add(-24 * time.Hour).Truncate(24 * time.Hour).Add(12 * time.Hour)
	for _, event := range []state.APIConsumerUsageEvent{
		{EventID: uuid.NewString(), AccountID: e.acct.ID, AppID: appA, ConsumerKey: consumerA,
			WindowStart: minute, RequestCount: 10, ErrorCount: 2, BillableUnits: 8},
		{EventID: uuid.NewString(), AccountID: e.acct.ID, AppID: appA, ConsumerKey: consumerA,
			WindowStart: minute.Add(time.Minute), RequestCount: 4, ErrorCount: 0, BillableUnits: 3},
		{EventID: uuid.NewString(), AccountID: e.acct.ID, AppID: appB, ConsumerKey: consumerB,
			WindowStart: minute, RequestCount: 7, ErrorCount: 1, BillableUnits: 6},
	} {
		if _, err := e.store.RecordAPIConsumerUsage(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	usage := e.do(t, http.MethodGet, "/v1/account/platform-tenants/"+tenant.ID+"/usage", nil, nil)
	if usage.Code != http.StatusOK {
		t.Fatalf("usage: %d %s", usage.Code, usage.Body.String())
	}
	var totals api.PlatformTenantUsageResponse
	if err := json.Unmarshal(usage.Body.Bytes(), &totals); err != nil {
		t.Fatal(err)
	}
	if totals.RequestCount != 21 || totals.ErrorCount != 3 || totals.BillableUnits != 17 || len(totals.Buckets) != 2 {
		t.Fatalf("cross-app usage = %+v", totals)
	}
	var dailyA api.PlatformTenantUsageBucketResponse
	for _, bucket := range totals.Buckets {
		if bucket.ConsumerID == consumerA {
			dailyA = bucket
		}
	}
	if dailyA.RequestCount != 14 || !dailyA.WindowStart.Equal(minute.Truncate(24*time.Hour)) {
		t.Fatalf("daily consumer aggregation = %+v", dailyA)
	}
	suspended := e.do(t, http.MethodPatch, "/v1/account/platform-tenants/"+tenant.ID,
		api.SetPlatformTenantStatusRequest{Status: "suspended"}, nil)
	if suspended.Code != http.StatusOK {
		t.Fatalf("suspend: %d %s", suspended.Code, suspended.Body.String())
	}
	if blocked, err := e.store.PlatformTenantSurfaceSuspended(context.Background(), surfaceID); err != nil || !blocked {
		t.Fatalf("suspended surface = %v, %v", blocked, err)
	}
	for _, pair := range []struct{ app, prefix string }{{appA, keyA.Prefix}, {appB, keyB.Prefix}} {
		if _, err := e.store.ConsumerKeyByAppAndPrefix(context.Background(), e.acct.ID, pair.app, pair.prefix); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("suspended key lookup = %v, want not found", err)
		}
	}
	if _, err := e.store.ConsumerKeyByAppAndPrefix(context.Background(), e.acct.ID, appA, otherPrefix); err != nil {
		t.Fatalf("unrelated customer's key was blocked: %v", err)
	}
	newKey := e.do(t, http.MethodPost, "/v1/apps/tenant-api/consumers/"+consumerA+"/keys",
		api.CreateConsumerKeyRequest{Name: "suspended-key", Scopes: []string{"read"}}, nil)
	if newKey.Code != http.StatusConflict {
		t.Fatalf("suspended tenant issued key: %d %s", newKey.Code, newKey.Body.String())
	}
	resumed := e.do(t, http.MethodPatch, "/v1/account/platform-tenants/"+tenant.ID,
		api.SetPlatformTenantStatusRequest{Status: "active"}, nil)
	if resumed.Code != http.StatusOK {
		t.Fatalf("resume: %d %s", resumed.Code, resumed.Body.String())
	}
	if blocked, err := e.store.PlatformTenantSurfaceSuspended(context.Background(), surfaceID); err != nil || blocked {
		t.Fatalf("resumed surface = %v, %v", blocked, err)
	}
	if _, err := e.store.ConsumerKeyByAppAndPrefix(context.Background(), e.acct.ID, appA, keyA.Prefix); err != nil {
		t.Fatalf("resumed key A: %v", err)
	}
	if _, err := e.store.ConsumerKeyByAppAndPrefix(context.Background(), e.acct.ID, appB, keyB.Prefix); err != nil {
		t.Fatalf("resumed key B: %v", err)
	}

	other, err := e.store.CreateAccount(context.Background(), "another-platform@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.GetPlatformTenant(context.Background(), other.ID, tenant.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account tenant = %v", err)
	}
	if _, err := e.store.LinkPlatformTenantConsumer(context.Background(), other.ID, tenant.ID, consumerA); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account consumer link = %v", err)
	}
}

func seedPlatformTenantConsumer(t *testing.T, e testEnv, slug, tenantID string) string {
	t.Helper()
	created := e.do(t, http.MethodPost, "/v1/apps/"+slug+"/consumers", api.CreateAPIConsumerRequest{
		ExternalRef: "customer-42", Name: "Customer 42",
	}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create %s consumer: %d %s", slug, created.Code, created.Body.String())
	}
	var consumer api.APIConsumerResponse
	if err := json.Unmarshal(created.Body.Bytes(), &consumer); err != nil {
		t.Fatal(err)
	}
	linked := e.do(t, http.MethodPost, "/v1/account/platform-tenants/"+tenantID+"/consumers",
		api.LinkPlatformTenantConsumerRequest{ConsumerID: consumer.ID}, nil)
	if linked.Code != http.StatusOK {
		t.Fatalf("link %s consumer: %d %s", slug, linked.Code, linked.Body.String())
	}
	return consumer.ID
}

func seedPlatformTenantKey(t *testing.T, e testEnv, slug, consumerID string) api.ConsumerKeyResponse {
	t.Helper()
	created := e.do(t, http.MethodPost, "/v1/apps/"+slug+"/consumers/"+consumerID+"/keys",
		api.CreateConsumerKeyRequest{Name: "primary", Scopes: []string{"read"}}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create %s key: %d %s", slug, created.Code, created.Body.String())
	}
	var key api.ConsumerKeyResponse
	if err := json.Unmarshal(created.Body.Bytes(), &key); err != nil {
		t.Fatal(err)
	}
	return key
}
