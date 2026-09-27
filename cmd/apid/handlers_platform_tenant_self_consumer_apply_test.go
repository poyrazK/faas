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

func TestPlatformTenantSelfConsumerApplyIsAtomicPreviewableAndRetrySafe(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	tenant, _, err := e.store.CreatePlatformTenant(ctx, e.acct.ID, "self-apply", "Self apply", 250)
	if err != nil {
		t.Fatal(err)
	}
	otherTenant, _, err := e.store.CreatePlatformTenant(ctx, e.acct.ID, "other-self-apply", "Other tenant", 250)
	if err != nil {
		t.Fatal(err)
	}
	makeSurface := func(slug, name, linkedTenant string) (string, state.TenantSurface) {
		t.Helper()
		appID := mustSeedApp(t, e, slug)
		limits, ok := api.LimitsFor(e.acct.Plan)
		if !ok {
			t.Fatal("missing account plan limits")
		}
		surface, err := e.store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
			AccountID: e.acct.ID, AppID: appID, Name: name,
		}, limits)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.store.UpdateTenantSurfaceStatus(ctx, surface.ID, state.SurfaceStatusActive); err != nil {
			t.Fatal(err)
		}
		if linkedTenant != "" {
			if _, err := e.store.LinkPlatformTenantSurface(ctx, e.acct.ID, linkedTenant, surface.ID); err != nil {
				t.Fatal(err)
			}
		}
		return appID, surface
	}
	appA, surfaceA := makeSurface("self-apply-app-a", "surface-a", tenant.ID)
	appB, surfaceB := makeSurface("self-apply-app-b", "surface-b", tenant.ID)
	appForeign, foreignSurface := makeSurface("self-apply-app-foreign", "surface-foreign", otherTenant.ID)
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
	manage := createToken("customer bundle manager", api.ScopePlatformTenantConsumersManage)
	read := createToken("customer bundle reader", api.ScopePlatformTenantCredentialsRead)
	request := func(token string, body any) *httptest.ResponseRecorder {
		t.Helper()
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/v1/platform-tenant-self/consumers/apply", bytes.NewReader(encoded))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec
	}
	input := api.ApplyPlatformTenantSelfConsumersRequest{ExternalRef: "bundle-customer", Name: "Bundle Customer",
		SurfaceIDs: []string{surfaceB.ID, surfaceA.ID}}
	if denied := request(read.Token, input); denied.Code != http.StatusForbidden {
		t.Fatalf("read-only token applied customer identities: %d %s", denied.Code, denied.Body)
	}
	if disabled := request(manage.Token, input); disabled.Code != http.StatusForbidden || !strings.Contains(disabled.Body.String(), "provisioning_disabled") {
		t.Fatalf("default-disabled policy: %d %s", disabled.Code, disabled.Body)
	}
	if badExtra := request(manage.Token, struct {
		api.ApplyPlatformTenantSelfConsumersRequest
		TenantID string `json:"tenant_id"`
	}{ApplyPlatformTenantSelfConsumersRequest: input, TenantID: otherTenant.ID}); badExtra.Code != http.StatusBadRequest {
		t.Fatalf("caller-selected tenant_id status=%d body=%s", badExtra.Code, badExtra.Body)
	}
	duplicate := input
	duplicate.SurfaceIDs = []string{surfaceA.ID, surfaceA.ID}
	if rejected := request(manage.Token, duplicate); rejected.Code != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate surface status=%d body=%s", rejected.Code, rejected.Body)
	}
	foreign := input
	foreign.ExternalRef, foreign.SurfaceIDs = "foreign-surface", []string{foreignSurface.ID}
	if hidden := request(manage.Token, foreign); hidden.Code != http.StatusNotFound || strings.Contains(hidden.Body.String(), appForeign) {
		t.Fatalf("foreign surface was exposed: %d %s", hidden.Code, hidden.Body)
	}
	if _, err := e.store.SetPlatformTenantConsumerProvisioningPolicy(ctx, e.acct.ID, tenant.ID, true, 2); err != nil {
		t.Fatal(err)
	}

	previewInput := input
	previewInput.DryRun = true
	preview := request(manage.Token, previewInput)
	var planned api.ApplyPlatformTenantSelfConsumersResponse
	if preview.Code != http.StatusOK || preview.Header().Get("Cache-Control") != "no-store" ||
		json.Unmarshal(preview.Body.Bytes(), &planned) != nil || !planned.DryRun || len(planned.Consumers) != 2 {
		t.Fatalf("dry-run response: %d %+v %s", preview.Code, planned, preview.Body)
	}
	for _, item := range planned.Consumers {
		if item.Action != "create" || item.ConsumerID != "" || item.Status != string(state.APIConsumerStatusActive) {
			t.Errorf("dry-run item exposes an ID or has wrong action: %+v", item)
		}
	}
	for _, appID := range []string{appA, appB} {
		consumers, err := e.store.ListAPIConsumersForApp(ctx, e.acct.ID, appID)
		if err != nil || len(consumers) != 0 {
			t.Fatalf("dry-run mutated app %s: %+v err=%v", appID, consumers, err)
		}
	}

	created := request(manage.Token, input)
	var applied api.ApplyPlatformTenantSelfConsumersResponse
	if created.Code != http.StatusCreated || created.Header().Get("Cache-Control") != "no-store" ||
		strings.Contains(created.Body.String(), "app_id") || strings.Contains(created.Body.String(), "account_id") ||
		strings.Contains(created.Body.String(), tenant.ID) || json.Unmarshal(created.Body.Bytes(), &applied) != nil || len(applied.Consumers) != 2 {
		t.Fatalf("apply response is not redacted or failed: %d %s", created.Code, created.Body)
	}
	bySurface := make(map[string]api.PlatformTenantSelfConsumerApplyItemResponse, len(applied.Consumers))
	for _, item := range applied.Consumers {
		if item.Action != "create" || item.ConsumerID == "" || item.ExternalRef != input.ExternalRef {
			t.Errorf("created item=%+v", item)
		}
		bySurface[item.SurfaceID] = item
	}
	if bySurface[surfaceA.ID].ConsumerID == "" || bySurface[surfaceB.ID].ConsumerID == "" ||
		bySurface[surfaceA.ID].ConsumerID == bySurface[surfaceB.ID].ConsumerID {
		t.Fatalf("expected one distinct app-local identity per surface: %+v", bySurface)
	}
	if _, err := e.store.CreateAPIConsumer(ctx, e.acct.ID, appB, "conflicting-bundle", "Owner identity"); err != nil {
		t.Fatal(err)
	}
	conflict := api.ApplyPlatformTenantSelfConsumersRequest{ExternalRef: "conflicting-bundle", Name: "Different name",
		SurfaceIDs: []string{surfaceA.ID, surfaceB.ID}}
	if rejected := request(manage.Token, conflict); rejected.Code != http.StatusConflict {
		t.Fatalf("conflicting second app status=%d body=%s", rejected.Code, rejected.Body)
	}
	if consumers, err := e.store.ListAPIConsumersForApp(ctx, e.acct.ID, appA); err != nil || len(consumers) != 1 {
		t.Fatalf("conflicting batch partially created in first app: %+v err=%v", consumers, err)
	}

	if _, err := e.store.SetPlatformTenantConsumerProvisioningPolicy(ctx, e.acct.ID, tenant.ID, false, 0); err != nil {
		t.Fatal(err)
	}
	replay := request(manage.Token, input)
	var replayed api.ApplyPlatformTenantSelfConsumersResponse
	if replay.Code != http.StatusOK || json.Unmarshal(replay.Body.Bytes(), &replayed) != nil || len(replayed.Consumers) != 2 {
		t.Fatalf("replay after disable: %d %+v %s", replay.Code, replayed, replay.Body)
	}
	for _, item := range replayed.Consumers {
		if item.Action != "unchanged" || item.ConsumerID != bySurface[item.SurfaceID].ConsumerID {
			t.Errorf("replay item=%+v", item)
		}
	}
}
