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
)

func TestPlatformTenantSelfCredentialManagementIsPolicyBoundAndRedacted(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	tenant, _, err := e.store.CreatePlatformTenant(ctx, e.acct.ID, "self-credentials", "Self credentials", 250)
	if err != nil {
		t.Fatal(err)
	}
	otherTenant, _, err := e.store.CreatePlatformTenant(ctx, e.acct.ID, "other-self-credentials", "Other tenant", 250)
	if err != nil {
		t.Fatal(err)
	}
	appID := mustSeedApp(t, e, "self-credentials-app")
	consumer, err := e.store.CreateAPIConsumer(ctx, e.acct.ID, appID, "self-customer", "Self customer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.LinkPlatformTenantConsumer(ctx, e.acct.ID, tenant.ID, consumer.ID); err != nil {
		t.Fatal(err)
	}
	otherConsumer, err := e.store.CreateAPIConsumer(ctx, e.acct.ID, appID, "other-customer", "Other customer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.LinkPlatformTenantConsumer(ctx, e.acct.ID, otherTenant.ID, otherConsumer.ID); err != nil {
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
	readToken := createToken("credential reader", api.ScopePlatformTenantCredentialsRead)
	manageToken := createToken("credential manager", api.ScopePlatformTenantCredentialsManage)
	request := func(token string, method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var reader *bytes.Reader
		if body != nil {
			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			reader = bytes.NewReader(encoded)
		} else {
			reader = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, path, reader)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec
	}
	consumers := request(readToken.Token, http.MethodGet, "/v1/platform-tenant-self/consumers", nil)
	if consumers.Code != http.StatusOK || consumers.Header().Get("Cache-Control") != "no-store" ||
		!strings.Contains(consumers.Body.String(), consumer.ID) || strings.Contains(consumers.Body.String(), otherConsumer.ID) ||
		strings.Contains(consumers.Body.String(), "app_id") {
		t.Fatalf("tenant consumer inventory leaked or failed: %d %s", consumers.Code, consumers.Body)
	}

	wanted, plaintext, err := api.PreparePlatformTenantCredential(consumer.ID, "self-v1", []string{"read"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	input := api.ApplyPlatformTenantCredentialsRequest{Keys: []api.PlatformTenantCredentialIntent{wanted}}
	if denied := request(readToken.Token, http.MethodPost, "/v1/platform-tenant-self/credentials/apply", input); denied.Code != http.StatusForbidden {
		t.Fatalf("read-only credential token wrote keys: %d %s", denied.Code, denied.Body)
	}
	if denied := request(manageToken.Token, http.MethodPost, "/v1/platform-tenant-self/credentials/apply", input); denied.Code != http.StatusForbidden || !strings.Contains(denied.Body.String(), "delegation_disabled") {
		t.Fatalf("default-disabled policy status=%d body=%s", denied.Code, denied.Body)
	}

	if _, err := e.store.SetPlatformTenantCredentialPolicy(ctx, e.acct.ID, tenant.ID, []string{"read"}, 1); err != nil {
		t.Fatal(err)
	}
	created := request(manageToken.Token, http.MethodPost, "/v1/platform-tenant-self/credentials/apply", input)
	if created.Code != http.StatusOK || created.Header().Get("Cache-Control") != "no-store" ||
		strings.Contains(created.Body.String(), plaintext) || strings.Contains(created.Body.String(), wanted.Hash) || strings.Contains(created.Body.String(), "hashed_secret") {
		t.Fatalf("delegated creation leaked secret or failed: %d %s", created.Code, created.Body)
	}
	var result api.ApplyPlatformTenantCredentialsResponse
	if err := json.Unmarshal(created.Body.Bytes(), &result); err != nil || len(result.Keys) != 1 || result.Keys[0].Action != "create" {
		t.Fatalf("delegated creation response=%+v err=%v body=%s", result, err, created.Body)
	}

	writeKey, _, err := api.PreparePlatformTenantCredential(consumer.ID, "self-write", []string{"write"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if denied := request(manageToken.Token, http.MethodPost, "/v1/platform-tenant-self/credentials/apply",
		api.ApplyPlatformTenantCredentialsRequest{Keys: []api.PlatformTenantCredentialIntent{writeKey}}); denied.Code != http.StatusForbidden || !strings.Contains(denied.Body.String(), "scope_denied") {
		t.Fatalf("owner-denied scope status=%d body=%s", denied.Code, denied.Body)
	}
	secondKey, _, err := api.PreparePlatformTenantCredential(consumer.ID, "self-v2", []string{"read"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if denied := request(manageToken.Token, http.MethodPost, "/v1/platform-tenant-self/credentials/apply",
		api.ApplyPlatformTenantCredentialsRequest{Keys: []api.PlatformTenantCredentialIntent{secondKey}}); denied.Code != http.StatusForbidden || !strings.Contains(denied.Body.String(), "limit_reached") {
		t.Fatalf("per-consumer cap status=%d body=%s", denied.Code, denied.Body)
	}

	unlinked, _, err := api.PreparePlatformTenantCredential(otherConsumer.ID, "wrong-tenant", []string{"read"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if conflict := request(manageToken.Token, http.MethodPost, "/v1/platform-tenant-self/credentials/apply",
		api.ApplyPlatformTenantCredentialsRequest{Keys: []api.PlatformTenantCredentialIntent{unlinked}}); conflict.Code != http.StatusConflict {
		t.Fatalf("other tenant consumer status=%d body=%s", conflict.Code, conflict.Body)
	}
	if denied := request(manageToken.Token, http.MethodGet, "/v1/platform-tenant-self/credentials", nil); denied.Code != http.StatusForbidden {
		t.Fatalf("manage-only token read status=%d body=%s", denied.Code, denied.Body)
	}
	listed := request(readToken.Token, http.MethodGet, "/v1/platform-tenant-self/credentials?limit=100&offset=0", nil)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), wanted.Prefix) ||
		strings.Contains(listed.Body.String(), wanted.Hash) || strings.Contains(listed.Body.String(), plaintext) {
		t.Fatalf("redacted credential list status=%d body=%s", listed.Code, listed.Body)
	}
	if denied := request(manageToken.Token, http.MethodGet, "/v1/account/platform-tenants/"+otherTenant.ID+"/credentials", nil); denied.Code != http.StatusForbidden {
		t.Fatalf("tenant token reached account/other-tenant route: %d %s", denied.Code, denied.Body)
	}

	if _, err := e.store.SetPlatformTenantCredentialPolicy(ctx, e.acct.ID, tenant.ID, nil, 0); err != nil {
		t.Fatal(err)
	}
	revoked := request(manageToken.Token, http.MethodPost, "/v1/platform-tenant-self/credentials/apply",
		api.ApplyPlatformTenantCredentialsRequest{RevokeKeyIDs: []string{result.Keys[0].ID}})
	if revoked.Code != http.StatusOK || !strings.Contains(revoked.Body.String(), `"action":"revoke"`) {
		t.Fatalf("revocation after policy disable status=%d body=%s", revoked.Code, revoked.Body)
	}
	if denied := request(manageToken.Token, http.MethodPost, "/v1/platform-tenant-self/credentials/apply",
		api.ApplyPlatformTenantCredentialsRequest{Keys: []api.PlatformTenantCredentialIntent{secondKey}}); denied.Code != http.StatusForbidden {
		t.Fatalf("disabled policy create status=%d body=%s", denied.Code, denied.Body)
	}
}
