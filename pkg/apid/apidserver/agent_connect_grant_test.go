// adr: 20261127160000
package apidserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// A hosted MCP sign-in the person approved with the Connect grant mints an
// mcp-login key without the grant as a scope, and records who granted it.
func TestCliAuthExchangeRecordsTheConnectGrant(t *testing.T) {
	cases := []struct {
		name   string
		scopes []string
		grant  bool
	}{
		{"deploy access with the grant", []string{api.ScopeDeployWrite, api.ScopeConnectApproval}, true},
		{"deploy access and a capability with the grant", []string{api.ScopeDeployWrite, api.ScopeConnectApproval, api.ScopeEnvWrite}, true},
		{"deploy access without the grant", []string{api.ScopeDeployWrite}, false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, store := apidserver.NewCliAuthTestServer(t)
			body, _ := json.Marshal(api.CliAuthCodeRequest{Purpose: api.CliAuthPurposeMCP, Scopes: tc.scopes})
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/cli-auth/code", bytes.NewReader(body)))
			var code api.CliAuthCodeResponse
			if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &code) != nil {
				t.Fatalf("mint = %d %s", rec.Code, rec.Body)
			}
			email := "connect-" + string(rune('a'+i)) + "@example.com"
			cookie, form := apidserver.RenderCliAuthForTestAs(t, srv, code.Code, email)
			if claim := apidserver.PostCliAuthFormForTest(t, srv, code.Code, email, cookie, form, url.Values{}); claim.Code != http.StatusFound {
				t.Fatalf("approve = %d %s", claim.Code, claim.Body)
			}
			exchangeBody, _ := json.Marshal(api.CliAuthExchangeRequest{Code: code.Code})
			rec = httptest.NewRecorder()
			srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/cli-auth/exchange", bytes.NewReader(exchangeBody)))
			var exchange api.CliAuthExchangeResponse
			if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &exchange) != nil {
				t.Fatalf("exchange = %d %s", rec.Code, rec.Body)
			}
			key, err := store.APIKeyByHash(t.Context(), api.HashToken([]byte(exchange.Plaintext)))
			if err != nil {
				t.Fatal(err)
			}
			if key.Label != api.MCPLoginKeyLabel || slices.Contains(key.Scopes, api.ScopeConnectApproval) || !slices.Contains(key.Scopes, api.ScopeDeployWrite) {
				t.Fatalf("key = %s %v; want an mcp-login key with deploy:write and no approvals:connect", key.Label, key.Scopes)
			}
			grant, ok, err := store.APIKeyConnectGrant(t.Context(), key.ID)
			if err != nil || ok != tc.grant {
				t.Fatalf("grant = %+v, %v, %v; want present %v", grant, ok, err, tc.grant)
			}
			if ok && (grant.GrantedBy != key.AccountID || grant.GrantedAt.IsZero()) {
				t.Fatalf("grant = %+v; want granted by %s with a time", grant, key.AccountID)
			}
		})
	}
}

// The Connect grant is the person's approval of exactly what the Connect
// screen names (ADR-20261127160000 §3): those requests go through without
// a 428, each recorded in the granting person's audit log. Everything else,
// a connection without the grant, and one whose categories a person chose,
// keep asking.
func TestHandlers_ConnectGrantApprovesWhatTheConsentCovers(t *testing.T) {
	srv, store := apidserver.NewCliAuthTestServer(t)
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "connect-grant@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	for _, app := range []state.App{
		{AccountID: acct.ID, Slug: "prod-svc", Type: state.AppTypeApp},
		{AccountID: acct.ID, Slug: "prod-svc-pr-1", Type: state.AppTypeApp, PreviewOfSlug: "prod-svc", PreviewPrNumber: 1},
	} {
		if _, err := store.CreateApp(ctx, app); err != nil {
			t.Fatalf("CreateApp %s: %v", app.Slug, err)
		}
	}
	mint := func(grant bool, categories []string) (string, state.APIKey) {
		t.Helper()
		plaintext, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		key, err := store.CreateAPIKey(ctx, acct.ID, hash, api.MCPLoginKeyLabel, []string{api.ScopeAppsRead, api.ScopeDeployWrite, api.ScopeEnvWrite})
		if err != nil {
			t.Fatal(err)
		}
		if grant {
			if err := store.CreateAPIKeyConnectGrant(ctx, key.ID, acct.ID); err != nil {
				t.Fatal(err)
			}
		}
		if categories != nil {
			if err := store.SetAPIKeyApprovalCategories(ctx, key.ID, categories); err != nil {
				t.Fatal(err)
			}
		}
		return plaintext, key
	}
	granted, grantedKey := mint(true, nil)
	ungranted, _ := mint(false, nil)
	chosen, _ := mint(true, []string{api.AgentApprovalProduction})
	call := func(key, method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		srv.ServeHTTP(rec, r)
		return rec
	}
	cronID := uuid.NewString()
	cases := []struct {
		name               string
		method, path, body string
		category           string
		covered            bool
	}{
		{"rollback", http.MethodPost, "/v1/apps/prod-svc/rollback", `{}`, api.AgentApprovalProduction, true},
		{"restart", http.MethodPost, "/v1/apps/prod-svc/restart", ``, api.AgentApprovalProduction, true},
		{"configuration", http.MethodPut, "/v1/apps/prod-svc/env/GREETING", `{"value":"hi"}`, api.AgentApprovalProduction, true},
		{"cron removal", http.MethodDelete, "/v1/crons/" + cronID, ``, api.AgentApprovalDelete, true},
		{"undo", http.MethodPost, "/v1/agent-connections/self/undo", `{}`, api.AgentApprovalUndo, true},
		{"app deletion", http.MethodDelete, "/v1/apps/prod-svc", ``, api.AgentApprovalDelete, false},
		{"secret write", http.MethodPut, "/v1/apps/prod-svc/secrets/TOKEN", `{"value":"x"}`, api.AgentApprovalProduction, false},
		{"command run", http.MethodPost, "/v1/apps/prod-svc/tasks", `{"command":["true"]}`, api.AgentApprovalProduction, false},
		{"credential", http.MethodPost, "/v1/keys", `{"label":"more","scopes":["apps:read"]}`, api.AgentApprovalCredentials, false},
		{"billing", http.MethodPatch, "/v1/account/plan", `{"plan":"scale"}`, api.AgentApprovalBilling, false},
	}
	asked := func(rec *httptest.ResponseRecorder) (string, bool) {
		var problem api.Problem
		if rec.Code != http.StatusPreconditionRequired || json.Unmarshal(rec.Body.Bytes(), &problem) != nil || problem.AgentApproval == nil {
			return "", false
		}
		return problem.AgentApproval.Category, true
	}
	since := time.Now().Add(-time.Minute)
	wantEvents := 0
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := call(granted, tc.method, tc.path, tc.body)
			category, ok := asked(rec)
			if tc.covered && ok {
				t.Fatalf("granted %s %s asked for approval: %s", tc.method, tc.path, rec.Body)
			}
			if !tc.covered && (!ok || category != tc.category) {
				t.Fatalf("granted %s %s = %d %s; want 428 for %s", tc.method, tc.path, rec.Code, rec.Body, tc.category)
			}
			if tc.covered {
				wantEvents++
			}
			if category, ok := asked(call(ungranted, tc.method, tc.path, tc.body)); !ok || category != tc.category {
				t.Fatalf("without the grant %s %s did not ask for %s", tc.method, tc.path, tc.category)
			}
		})
	}
	// A key whose categories a person chose follows that choice.
	if category, ok := asked(call(chosen, http.MethodPost, "/v1/apps/prod-svc/restart", ``)); !ok || category != api.AgentApprovalProduction {
		t.Fatal("a key that chose production approvals was let through by the grant")
	}
	// A preview is not production: nothing to approve, nothing to record.
	if _, ok := asked(call(granted, http.MethodPost, "/v1/apps/prod-svc-pr-1/restart", ``)); ok {
		t.Fatal("a preview restart asked for approval")
	}

	events, err := store.ListEventsByKindSince(ctx, "agent_approval.connect_grant", since, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != wantEvents {
		t.Fatalf("%d connect_grant audit events; want one per covered request (%d)", len(events), wantEvents)
	}
	subject := uuid.MustParse(acct.ID)
	for _, event := range events {
		var data map[string]any
		if err := json.Unmarshal(event.Data, &data); err != nil {
			t.Fatal(err)
		}
		if event.Subject == nil || *event.Subject != subject || data["key_id"] != grantedKey.ID || data["granted_by"] != acct.ID || data["granted_at"] == "" || data["path"] == "" || data["category"] == "" {
			t.Fatalf("audit event = %+v %s; want the grant, key, path and category", event, event.Data)
		}
	}
}
