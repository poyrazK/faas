package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPlatformTenantSelfConsumerProvisioningIsScopedBoundedAndRetrySafe(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	tenant, _, err := e.store.CreatePlatformTenant(ctx, e.acct.ID, "self-provisioning", "Self provisioning", 250)
	if err != nil {
		t.Fatal(err)
	}
	appID := mustSeedApp(t, e, "self-provisioning-app")
	limits, ok := api.LimitsFor(e.acct.Plan)
	if !ok {
		t.Fatal("missing account plan limits")
	}
	surface, err := e.store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
		AccountID: e.acct.ID, AppID: appID, Name: "provisioning-surface",
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpdateTenantSurfaceStatus(ctx, surface.ID, state.SurfaceStatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.LinkPlatformTenantSurface(ctx, e.acct.ID, tenant.ID, surface.ID); err != nil {
		t.Fatal(err)
	}
	unlinkedSurface, err := e.store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
		AccountID: e.acct.ID, AppID: appID, Name: "unlinked-provisioning-surface",
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpdateTenantSurfaceStatus(ctx, unlinkedSurface.ID, state.SurfaceStatusActive); err != nil {
		t.Fatal(err)
	}
	otherTenant, _, err := e.store.CreatePlatformTenant(ctx, e.acct.ID, "other-self-provisioning", "Other tenant", 250)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.LinkPlatformTenantSurface(ctx, e.acct.ID, otherTenant.ID, unlinkedSurface.ID); err != nil {
		t.Fatal(err)
	}
	createToken := func(name, scope string) api.CreatePlatformTenantAccessTokenResponse {
		t.Helper()
		created := e.do(t, http.MethodPost, "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens",
			api.CreatePlatformTenantAccessTokenRequest{Name: name, Scopes: []string{scope}}, nil)
		if created.Code != http.StatusCreated {
			t.Fatalf("create %s token: %d %s", scope, created.Code, created.Body)
		}
		var token api.CreatePlatformTenantAccessTokenResponse
		if err := json.Unmarshal(created.Body.Bytes(), &token); err != nil {
			t.Fatal(err)
		}
		return token
	}
	manage := createToken("consumer manager", api.ScopePlatformTenantConsumersManage)
	read := createToken("consumer reader", api.ScopePlatformTenantCredentialsRead)
	request := func(token string, body any) *httptest.ResponseRecorder {
		t.Helper()
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/v1/platform-tenant-self/consumers", bytes.NewReader(encoded))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec
	}
	input := api.CreatePlatformTenantSelfConsumerRequest{SurfaceID: surface.ID, ExternalRef: "external-42", Name: "Customer 42"}
	if disabled := request(manage.Token, input); disabled.Code != http.StatusForbidden || !strings.Contains(disabled.Body.String(), "provisioning_disabled") {
		t.Fatalf("default-disabled policy: %d %s", disabled.Code, disabled.Body)
	}
	if denied := request(read.Token, input); denied.Code != http.StatusForbidden {
		t.Fatalf("read-only token created customer: %d %s", denied.Code, denied.Body)
	}
	if _, err := e.store.SetPlatformTenantConsumerProvisioningPolicy(ctx, e.acct.ID, tenant.ID, true, 1); err != nil {
		t.Fatal(err)
	}
	created := request(manage.Token, input)
	if created.Code != http.StatusCreated || created.Header().Get("Cache-Control") != "no-store" ||
		strings.Contains(created.Body.String(), "app_id") || strings.Contains(created.Body.String(), "account_id") ||
		strings.Contains(created.Body.String(), tenant.ID) {
		t.Fatalf("create response is not redacted or failed: %d %s", created.Code, created.Body)
	}
	var first api.PlatformTenantSelfConsumerResponse
	if err := json.Unmarshal(created.Body.Bytes(), &first); err != nil || first.ConsumerID == "" || first.ExternalRef != input.ExternalRef {
		t.Fatalf("created consumer=%+v err=%v body=%s", first, err, created.Body)
	}
	replay := request(manage.Token, input)
	var replayed api.PlatformTenantSelfConsumerResponse
	if replay.Code != http.StatusOK || json.Unmarshal(replay.Body.Bytes(), &replayed) != nil || replayed.ConsumerID != first.ConsumerID {
		t.Fatalf("identical replay: %d %+v %s", replay.Code, replayed, replay.Body)
	}
	second := input
	second.ExternalRef, second.Name = "external-43", "Customer 43"
	if limited := request(manage.Token, second); limited.Code != http.StatusForbidden || !strings.Contains(limited.Body.String(), "limit_reached") {
		t.Fatalf("active-customer cap: %d %s", limited.Code, limited.Body)
	}
	foreignSurface := input
	foreignSurface.SurfaceID, foreignSurface.ExternalRef = unlinkedSurface.ID, "foreign-surface-customer"
	if hidden := request(manage.Token, foreignSurface); hidden.Code != http.StatusNotFound {
		t.Fatalf("other tenant surface status=%d body=%s", hidden.Code, hidden.Body)
	}
	unknown := struct {
		api.CreatePlatformTenantSelfConsumerRequest
		AppID string `json:"app_id"`
	}{CreatePlatformTenantSelfConsumerRequest: input, AppID: appID}
	if rejected := request(manage.Token, unknown); rejected.Code != http.StatusBadRequest {
		t.Fatalf("caller-selected app_id status=%d body=%s", rejected.Code, rejected.Body)
	}
	if _, err := e.store.SetPlatformTenantConsumerProvisioningPolicy(ctx, e.acct.ID, tenant.ID, false, 0); err != nil {
		t.Fatal(err)
	}
	if replay := request(manage.Token, input); replay.Code != http.StatusOK {
		t.Fatalf("replay after disable status=%d body=%s", replay.Code, replay.Body)
	}
}
