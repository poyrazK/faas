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

func TestPlatformTenantSelfConsumerRevocationIsScopedAtomicAndRetrySafe(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	tenant, _, err := e.store.CreatePlatformTenant(ctx, e.acct.ID, "self-revocation", "Self revocation", 250)
	if err != nil {
		t.Fatal(err)
	}
	otherTenant, _, err := e.store.CreatePlatformTenant(ctx, e.acct.ID, "other-self-revocation", "Other tenant", 250)
	if err != nil {
		t.Fatal(err)
	}

	type customer struct {
		app      string
		consumer state.APIConsumer
		key      state.ConsumerKey
	}
	makeCustomer := func(slug, tenantID, externalRef string) customer {
		t.Helper()
		appID := mustSeedApp(t, e, slug)
		consumer, err := e.store.CreateAPIConsumer(ctx, e.acct.ID, appID, externalRef, "Customer "+externalRef)
		if err != nil {
			t.Fatal(err)
		}
		if tenantID != "" {
			consumer, err = e.store.LinkPlatformTenantConsumer(ctx, e.acct.ID, tenantID, consumer.ID)
			if err != nil {
				t.Fatal(err)
			}
		}
		key, err := e.store.CreateConsumerKeyForConsumer(ctx, e.acct.ID, consumer.ID,
			"initial", "pfx-"+externalRef[:8], api.HashAPIKey("secret-"+externalRef), []string{"read"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return customer{app: appID, consumer: consumer, key: key}
	}
	first := makeCustomer("self-revocation-app-a", tenant.ID, "customer-a")
	second := makeCustomer("self-revocation-app-b", tenant.ID, "customer-b")
	foreign := makeCustomer("self-revocation-app-c", otherTenant.ID, "foreign-customer")
	unlinked := makeCustomer("self-revocation-app-d", "", "unlinked-customer")

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
	manage := createToken("customer manager", api.ScopePlatformTenantConsumersManage)
	read := createToken("customer reader", api.ScopePlatformTenantCredentialsRead)
	request := func(token, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/v1/platform-tenant-self/consumers/revoke", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec
	}
	ids, err := json.Marshal(api.RevokePlatformTenantSelfConsumersRequest{ConsumerIDs: []string{first.consumer.ID, second.consumer.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if denied := request(read.Token, string(ids)); denied.Code != http.StatusForbidden {
		t.Fatalf("read-only token revoked customers: %d %s", denied.Code, denied.Body)
	}
	if badExtra := request(manage.Token, `{"consumer_ids":["`+first.consumer.ID+`"],"tenant_id":"`+otherTenant.ID+`"}`); badExtra.Code != http.StatusBadRequest {
		t.Fatalf("caller-selected tenant_id status=%d body=%s", badExtra.Code, badExtra.Body)
	}
	if duplicate := request(manage.Token, `{"consumer_ids":["`+first.consumer.ID+`","`+first.consumer.ID+`"]}`); duplicate.Code != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate ID status=%d body=%s", duplicate.Code, duplicate.Body)
	}
	for _, invalid := range [][]string{{first.consumer.ID, foreign.consumer.ID}, {first.consumer.ID, unlinked.consumer.ID}} {
		body, _ := json.Marshal(api.RevokePlatformTenantSelfConsumersRequest{ConsumerIDs: invalid})
		if hidden := request(manage.Token, string(body)); hidden.Code != http.StatusNotFound || strings.Contains(hidden.Body.String(), foreign.consumer.ID) || strings.Contains(hidden.Body.String(), unlinked.consumer.ID) {
			t.Fatalf("foreign or unlinked customer was enumerable: %d %s", hidden.Code, hidden.Body)
		}
		stillActive, err := e.store.GetAPIConsumerByID(ctx, e.acct.ID, first.consumer.ID)
		if err != nil || !stillActive.Active() {
			t.Fatalf("failed batch partially revoked owned customer: %+v err=%v", stillActive, err)
		}
	}

	if _, err := e.store.SetPlatformTenantStatus(ctx, e.acct.ID, tenant.ID, state.PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
	response := request(manage.Token, string(ids))
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" ||
		strings.Contains(response.Body.String(), "app_id") || strings.Contains(response.Body.String(), "account_id") || strings.Contains(response.Body.String(), tenant.ID) {
		t.Fatalf("suspended-tenant revoke response leaked data or failed: %d %s", response.Code, response.Body)
	}
	var revoked api.PlatformTenantSelfConsumerRevocationResponse
	if err := json.Unmarshal(response.Body.Bytes(), &revoked); err != nil {
		t.Fatal(err)
	}
	if revoked.RevokedKeys != 2 || len(revoked.Consumers) != 2 {
		t.Fatalf("revocation response=%+v body=%s", revoked, response.Body)
	}
	for _, item := range []customer{first, second} {
		loaded, err := e.store.GetAPIConsumerByID(ctx, e.acct.ID, item.consumer.ID)
		if err != nil || loaded.Status != state.APIConsumerStatusRevoked {
			t.Fatalf("customer %s status=%+v err=%v", item.consumer.ID, loaded, err)
		}
		keys, err := e.store.ListConsumerKeysForApp(ctx, e.acct.ID, item.app)
		if err != nil || len(keys) != 1 || keys[0].ID != item.key.ID || keys[0].RevokedAt == nil {
			t.Fatalf("customer %s keys=%+v err=%v", item.consumer.ID, keys, err)
		}
	}
	for _, item := range []customer{foreign, unlinked} {
		loaded, err := e.store.GetAPIConsumerByID(ctx, e.acct.ID, item.consumer.ID)
		if err != nil || !loaded.Active() {
			t.Fatalf("unselected customer %s was changed: %+v err=%v", item.consumer.ID, loaded, err)
		}
	}

	replay := request(manage.Token, string(ids))
	var replayed api.PlatformTenantSelfConsumerRevocationResponse
	if replay.Code != http.StatusOK || json.Unmarshal(replay.Body.Bytes(), &replayed) != nil || replayed.RevokedKeys != 0 || len(replayed.Consumers) != 2 {
		t.Fatalf("idempotent replay: %d %+v %s", replay.Code, replayed, replay.Body)
	}
}

func TestPlatformTenantSelfConsumerRevocationRejectsEmptyBatch(t *testing.T) {
	e := setup(t, api.PlanPro)
	tenant, _, err := e.store.CreatePlatformTenant(context.Background(), e.acct.ID, "empty-revocation", "Empty revocation", 250)
	if err != nil {
		t.Fatal(err)
	}
	created := e.do(t, http.MethodPost, "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens",
		api.CreatePlatformTenantAccessTokenRequest{Name: "customer manager", Scopes: []string{api.ScopePlatformTenantConsumersManage}}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create manager token: %d %s", created.Code, created.Body)
	}
	var token api.CreatePlatformTenantAccessTokenResponse
	if err := json.Unmarshal(created.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/platform-tenant-self/consumers/revoke", bytes.NewBufferString(`{"consumer_ids":[]}`))
	req.Header.Set("Authorization", "Bearer "+token.Token)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty batch status=%d body=%s", rec.Code, rec.Body)
	}
}
