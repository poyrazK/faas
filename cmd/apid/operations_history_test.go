// adr: 521
package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOperationHistoryHTTPClosedAdmissionAndCustomerAuthority(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx, store := context.Background(), state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "history-http@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "history-http", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeployLive, Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	def, err := store.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, Scope: dep.Scope, DeploymentID: dep.ID, Spec: api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"generating"}, InputSchema: []byte(`{"type":"object"}`), OutputSchema: []byte(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	token := func(name string, scopes []string) (string, string) {
		t.Helper()
		tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, name, name, 100)
		if err != nil {
			t.Fatal(err)
		}
		raw, prefix, hash, err := api.GeneratePlatformTenantAccessToken()
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.CreatePlatformTenantAccessToken(ctx, state.PlatformTenantAccessTokenInput{AccountID: acct.ID, TenantID: tenant.ID, Name: name, Prefix: prefix, TokenHash: hash, Scopes: scopes, ExpiresAt: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		return raw, tenant.ID
	}
	alice, owner := token("alice", []string{api.ScopePlatformTenantOperationsRead})
	bob, _ := token("bob", []string{api.ScopePlatformTenantOperationsRead})
	manage, _ := token("manage-only", []string{api.ScopePlatformTenantOperationsManage})
	op, _, err := store.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, PlatformTenantID: owner, DefinitionID: def.ID, IdempotencyKey: "export", Input: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	key, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "account", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
	handler := srv.handler() // default closed admission
	base := "/v1/platform-tenant-self/customer-operations"
	query := url.Values{"app_id": {app.ID}, "scope": {dep.Scope}}.Encode()
	read := func(bearer, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set(api.PlatformTenantIDHeader, owner) // forged header cannot select identity
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	for _, tc := range []struct {
		token  string
		status int
		count  int
	}{{alice, 200, 1}, {bob, 200, 0}, {key, 403, 0}, {manage, 403, 0}, {"", 401, 0}} {
		w := read(tc.token, base+"?"+query)
		if w.Code != tc.status {
			t.Fatalf("authority: %d want %d: %s", w.Code, tc.status, w.Body.String())
		}
		if w.Code == 200 {
			var page api.OperationListResponse
			if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Operations) != tc.count {
				t.Fatalf("scoped page: %s %v", w.Body.String(), err)
			}
			if tc.count > 0 && page.Operations[0].ID != op.ID {
				t.Fatal("history lost accepted identity")
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("history can be cached")
			}
		}
	}
	for _, suffix := range []string{"&tenant_id=" + owner, "&app_id=" + app.ID, "&scope=", "&scope=staging", "&limit=0", "&limit=101", "&cursor=invalid", "&state=unknown", "&name=%00", "&bad=%XX"} {
		if w := read(alice, base+"?"+query+suffix); w.Code != 400 {
			t.Fatalf("invalid query %s: %d %s", suffix, w.Code, w.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodOptions, base, nil)
	req.Header.Set("Origin", "https://customer.example")
	req.Header.Set("Access-Control-Request-Method", "GET")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 204 || w.Header().Get("Access-Control-Allow-Methods") != "GET, POST" || w.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("history preflight: %d %v", w.Code, w.Header())
	}
}
