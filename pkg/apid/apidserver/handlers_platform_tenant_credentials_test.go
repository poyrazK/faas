package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestPlatformTenantCredentialApplyNeverHandlesPlaintext(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	_ = apidserver.MustSeedApp(t, e, "credential-app")
	created := e.Do(t, http.MethodPost, "/v1/account/platform-tenants", api.CreatePlatformTenantRequest{
		ExternalRef: "customer-42", Name: "Customer 42"}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("tenant = %d %s", created.Code, created.Body.String())
	}
	var tenant api.PlatformTenantResponse
	if err := json.Unmarshal(created.Body.Bytes(), &tenant); err != nil {
		t.Fatal(err)
	}
	consumerID := seedPlatformTenantConsumer(t, e, "credential-app", tenant.ID)
	wanted, plaintext, err := api.PreparePlatformTenantCredential(consumerID, "v1", []string{"read"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/account/platform-tenants/" + tenant.ID + "/credentials/apply"
	request := api.ApplyPlatformTenantCredentialsRequest{DryRun: true, Keys: []api.PlatformTenantCredentialIntent{wanted}}
	preview := e.Do(t, http.MethodPost, path, request, map[string]string{"Idempotency-Key": "preview"})
	if preview.Code != http.StatusOK || !strings.Contains(preview.Body.String(), `"action":"create"`) {
		t.Fatalf("preview = %d %s", preview.Code, preview.Body.String())
	}
	request.DryRun = false
	applied := e.Do(t, http.MethodPost, path, request, map[string]string{"Idempotency-Key": "apply"})
	if applied.Code != http.StatusOK || applied.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("apply = %d %s", applied.Code, applied.Body.String())
	}
	if strings.Contains(applied.Body.String(), plaintext) || strings.Contains(applied.Body.String(), `"key":`) || strings.Contains(applied.Body.String(), wanted.Hash) {
		t.Fatalf("apply leaked credential material: %s", applied.Body.String())
	}
	replay := e.Do(t, http.MethodPost, path, request, map[string]string{"Idempotency-Key": "another"})
	if replay.Code != http.StatusOK || !strings.Contains(replay.Body.String(), `"action":"unchanged"`) {
		t.Fatalf("replay = %d %s", replay.Code, replay.Body.String())
	}
	list := e.Do(t, http.MethodGet, "/v1/account/platform-tenants/"+tenant.ID+"/credentials", nil, nil)
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), plaintext) || !strings.Contains(list.Body.String(), wanted.Prefix) {
		t.Fatalf("list = %d %s", list.Code, list.Body.String())
	}
}

func TestLegacyConsumerKeyResponseNotStoredByIdempotency(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "legacy-key-app")
	created := e.Do(t, http.MethodPost, "/v1/account/platform-tenants", api.CreatePlatformTenantRequest{
		ExternalRef: "customer-42", Name: "Customer 42"}, nil)
	var tenant api.PlatformTenantResponse
	if err := json.Unmarshal(created.Body.Bytes(), &tenant); err != nil {
		t.Fatal(err)
	}
	consumerID := seedPlatformTenantConsumer(t, e, "legacy-key-app", tenant.ID)
	path := "/v1/apps/legacy-key-app/consumers/" + consumerID + "/keys"
	response := e.Do(t, http.MethodPost, path, api.CreateConsumerKeyRequest{Name: "primary", Scopes: []string{"read"}},
		map[string]string{"Idempotency-Key": "legacy-plaintext"})
	if response.Code != http.StatusCreated || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), `"key":"ck_`) {
		t.Fatalf("legacy create = %d %s", response.Code, response.Body.String())
	}
	replayed := e.Do(t, http.MethodPost, path, api.CreateConsumerKeyRequest{Name: "primary", Scopes: []string{"read"}},
		map[string]string{"Idempotency-Key": "legacy-plaintext"})
	if replayed.Code != http.StatusConflict || strings.Contains(replayed.Body.String(), `"key":"ck_`) {
		t.Fatalf("legacy replay = %d %s", replayed.Code, replayed.Body.String())
	}
	var key api.ConsumerKeyResponse
	if err := json.Unmarshal(response.Body.Bytes(), &key); err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Store()).RevokeConsumerKey(context.Background(), (*e.Acct()).ID, key.ID); err != nil {
		t.Fatal(err)
	}
	if count, err := (*e.S()).CountConsumerKeys(context.Background(), (*e.Acct()).ID, appID); err != nil || count != 0 {
		t.Fatalf("revoked app quota = %d, %v", count, err)
	}
	app, err := (*e.Store()).AppByID(context.Background(), appID)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := (*e.S()).CountAccountConsumerKeys(context.Background(), app); err != nil || count != 0 {
		t.Fatalf("revoked account quota = %d, %v", count, err)
	}
}
