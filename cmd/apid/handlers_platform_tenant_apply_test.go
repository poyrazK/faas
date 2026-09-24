package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPlatformTenantApplyDryRunReplayAndAtomicConflict(t *testing.T) {
	e := setup(t, api.PlanPro)
	appA := mustSeedApp(t, e, "apply-api")
	appB := mustSeedApp(t, e, "apply-worker")
	surfaceID := seedTenantSurface(t, e, appA, "apply-customer-hosts")
	req := api.ApplyPlatformTenantRequest{ExternalRef: "customer-42", Name: "Customer 42",
		Consumers: []api.ApplyPlatformTenantConsumerRequest{
			{AppID: appA, ExternalRef: "customer-42", Name: "Customer 42"},
			{AppID: appB, ExternalRef: "customer-42", Name: "Customer 42"},
		}, SurfaceIDs: []string{surfaceID}}
	req.DryRun = true
	preview := readApplyResponse(t, e.do(t, http.MethodPost, "/v1/account/platform-tenants/apply", req, nil))
	if preview.Action != "create" || preview.TenantID != "" || len(preview.Consumers) != 2 ||
		preview.Consumers[0].Action != "create" || preview.Surfaces[0].Action != "link" || !preview.DryRun {
		t.Fatalf("preview = %+v", preview)
	}
	page, err := e.store.ListPlatformTenants(context.Background(), e.acct.ID, 100, 0)
	if err != nil || len(page) != 0 {
		t.Fatalf("dry run wrote tenant: %+v, %v", page, err)
	}
	req.DryRun = false
	applied := readApplyResponse(t, e.do(t, http.MethodPost, "/v1/account/platform-tenants/apply", req, nil))
	if applied.Action != "create" || applied.TenantID == "" || applied.Consumers[0].ID == "" || applied.Consumers[1].ID == "" {
		t.Fatalf("apply = %+v", applied)
	}
	replay := readApplyResponse(t, e.do(t, http.MethodPost, "/v1/account/platform-tenants/apply", req, nil))
	if replay.Action != "unchanged" || replay.TenantID != applied.TenantID ||
		replay.Consumers[0].Action != "unchanged" || replay.Consumers[0].ID != applied.Consumers[0].ID ||
		replay.Surfaces[0].Action != "unchanged" {
		t.Fatalf("replay = %+v", replay)
	}
	linked, err := e.store.GetAPIConsumerByID(context.Background(), e.acct.ID, applied.Consumers[0].ID)
	if err != nil || linked.PlatformTenantID != applied.TenantID {
		t.Fatalf("consumer link = %+v, %v", linked, err)
	}
	other, _, err := e.store.CreatePlatformTenant(context.Background(), e.acct.ID, "other", "Other", 100)
	if err != nil {
		t.Fatal(err)
	}
	otherConsumer, err := e.store.CreateAPIConsumer(context.Background(), e.acct.ID, appB, "other", "Other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.LinkPlatformTenantConsumer(context.Background(), e.acct.ID, other.ID, otherConsumer.ID); err != nil {
		t.Fatal(err)
	}
	conflicting := api.ApplyPlatformTenantRequest{ExternalRef: "new-customer", Name: "New Customer",
		Consumers: []api.ApplyPlatformTenantConsumerRequest{
			{AppID: appA, ExternalRef: "new-customer", Name: "New Customer"},
			{AppID: appB, ExternalRef: "other", Name: "Other"},
		}}
	resp := e.do(t, http.MethodPost, "/v1/account/platform-tenants/apply", conflicting, nil)
	if resp.Code != http.StatusConflict {
		t.Fatalf("conflict = %d %s", resp.Code, resp.Body.String())
	}
	page, err = e.store.ListPlatformTenants(context.Background(), e.acct.ID, 100, 0)
	if err != nil || len(page) != 2 {
		t.Fatalf("partial tenant after conflict: %+v, %v", page, err)
	}
	consumers, err := e.store.ListAPIConsumersForApp(context.Background(), e.acct.ID, appA)
	if err != nil || len(consumers) != 1 {
		t.Fatalf("partial consumer after conflict: %+v, %v", consumers, err)
	}
}

func TestPlatformTenantApplyRejectsInvalidAndCrossAccountResources(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "apply-owned")
	for _, req := range []api.ApplyPlatformTenantRequest{
		{ExternalRef: "x", Name: "X", Consumers: []api.ApplyPlatformTenantConsumerRequest{{AppID: "bad", ExternalRef: "x", Name: "X"}}},
		{ExternalRef: "x", Name: "X", SurfaceIDs: []string{"bad"}},
		{ExternalRef: "x", Name: "X", Consumers: []api.ApplyPlatformTenantConsumerRequest{
			{AppID: appID, ExternalRef: "x", Name: "X"}, {AppID: appID, ExternalRef: "x", Name: "X"}}},
	} {
		resp := e.do(t, http.MethodPost, "/v1/account/platform-tenants/apply", req, nil)
		if resp.Code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid bundle = %d %s", resp.Code, resp.Body.String())
		}
	}
	resp := e.do(t, http.MethodPost, "/v1/account/platform-tenants/apply", api.ApplyPlatformTenantRequest{
		ExternalRef: "x", Name: "X", SurfaceIDs: []string{"00000000-0000-4000-8000-000000000001"}}, nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("missing surface = %d %s", resp.Code, resp.Body.String())
	}
	foreign, err := e.store.CreateAccount(context.Background(), "apply-foreign@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreignApp, err := e.store.CreateApp(context.Background(), state.App{AccountID: foreign.ID,
		Slug: "apply-foreign", Type: state.AppTypeApp, RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	foreignSurface, err := e.store.CreateTenantSurfaceIfUnderQuota(context.Background(), state.CreateTenantSurfaceParams{
		AccountID: foreign.ID, AppID: foreignApp.ID, Name: "foreign-hosts"}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range []api.ApplyPlatformTenantRequest{
		{ExternalRef: "x", Name: "X", Consumers: []api.ApplyPlatformTenantConsumerRequest{
			{AppID: foreignApp.ID, ExternalRef: "x", Name: "X"}}},
		{ExternalRef: "x", Name: "X", SurfaceIDs: []string{foreignSurface.ID}},
	} {
		resp := e.do(t, http.MethodPost, "/v1/account/platform-tenants/apply", req, nil)
		if resp.Code != http.StatusNotFound {
			t.Fatalf("cross-account bundle = %d %s", resp.Code, resp.Body.String())
		}
	}
	page, err := e.store.ListPlatformTenants(context.Background(), e.acct.ID, 100, 0)
	if err != nil || len(page) != 0 {
		t.Fatalf("invalid bundle wrote tenant: %+v, %v", page, err)
	}
}

func TestPlatformTenantApplyLinksExistingConsumerWithoutResumingTenant(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "apply-existing")
	consumer, err := e.store.CreateAPIConsumer(context.Background(), e.acct.ID, appID, "existing", "Existing")
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := e.store.CreatePlatformTenant(context.Background(), e.acct.ID, "existing", "Existing", 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.SetPlatformTenantStatus(context.Background(), e.acct.ID, tenant.ID, state.PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
	resp := e.do(t, http.MethodPost, "/v1/account/platform-tenants/apply", api.ApplyPlatformTenantRequest{
		ExternalRef: "existing", Name: "Existing", Consumers: []api.ApplyPlatformTenantConsumerRequest{
			{AppID: appID, ExternalRef: "existing", Name: "Existing"}},
	}, nil)
	result := readApplyResponse(t, resp)
	if result.Status != state.PlatformTenantSuspended || result.Action != "unchanged" ||
		len(result.Consumers) != 1 || result.Consumers[0].Action != "link" || result.Consumers[0].ID != consumer.ID {
		t.Fatalf("existing consumer = %+v", result)
	}
	loaded, err := e.store.GetAPIConsumerByID(context.Background(), e.acct.ID, consumer.ID)
	if err != nil || loaded.PlatformTenantID != tenant.ID {
		t.Fatalf("linked existing consumer = %+v, %v", loaded, err)
	}
}

func readApplyResponse(t *testing.T, resp interface{ Result() *http.Response }) api.ApplyPlatformTenantResponse {
	t.Helper()
	r := resp.Result()
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Fatalf("apply = %d", r.StatusCode)
	}
	var out api.ApplyPlatformTenantResponse
	if err := json.NewDecoder(r.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

var _ state.PlatformTenantApplyStore = (*state.MemStore)(nil)
