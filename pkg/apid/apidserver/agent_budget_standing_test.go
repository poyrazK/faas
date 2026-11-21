// adr: 20261111114059 — cost-control review 2026-11-19 (N9, N10): an agent
// budget holds the standing cost its changes keep running; lowering cost
// gives budget back.
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestHandlers_AgentBudgetIsStandingCost(t *testing.T) {
	srv, store := apidserver.NewCliAuthTestServer(t)
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "standing-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "standing-svc", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5}); err != nil {
		t.Fatal(err)
	}
	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.CreateAPIKey(ctx, acct.ID, hash, "standing-agent", []string{api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAPIKeyApprovalCategories(ctx, key.ID, []string{}); err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+plaintext)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		srv.ServeHTTP(rec, r)
		return rec
	}
	spent := func() int64 {
		t.Helper()
		var out api.AgentBudgetResponse
		rec := call(http.MethodGet, "/v1/agent-connections/self/budget", "")
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
			t.Fatalf("budget = %d %s", rec.Code, rec.Body)
		}
		return out.SpentCents
	}
	one := api.CentsCeil(api.AgentStandingMillicents(1, 512))
	if rec := call(http.MethodPatch, "/v1/apps/standing-svc", `{"min_instances":1}`); rec.Code != http.StatusOK {
		t.Fatalf("scale up = %d %s", rec.Code, rec.Body)
	}
	if got := spent(); got != one {
		t.Fatalf("standing after scale up = %d, want %d", got, one)
	}
	// Lowering cost gives the budget back.
	if rec := call(http.MethodPatch, "/v1/apps/standing-svc", `{"min_instances":0}`); rec.Code != http.StatusOK {
		t.Fatalf("scale down = %d %s", rec.Code, rec.Body)
	}
	if got := spent(); got != 0 {
		t.Fatalf("standing after scale down = %d, want 0", got)
	}
	// A refused change keeps no reservation.
	if rec := call(http.MethodPatch, "/v1/apps/standing-svc", `{"min_instances":1,"ram_mb":-5}`); rec.Code < 400 {
		t.Fatalf("invalid change = %d; want refused", rec.Code)
	}
	if got := spent(); got != 0 {
		t.Fatalf("standing after a refused change = %d, want 0", got)
	}
}
