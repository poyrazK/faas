package mcpreadiness

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMCPReadinessPolicyHistoryHTTP(t *testing.T) {
	s := state.NewMemStore()
	ctx := t.Context()
	a, e := s.CreateAccount(ctx, "history-http@example.com", api.PlanPro)
	if e != nil {
		t.Fatal(e)
	}
	app, e := s.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "history-http", Type: state.AppTypeApp, Status: state.AppActive})
	if e != nil {
		t.Fatal(e)
	}
	request := api.SetMCPReadinessPolicyRequest{Scope: "staging", Endpoint: "/mcp", MaxAgeSeconds: 300, Reason: "original staging opt out"}
	old, e := s.SetMCPReadinessPolicy(ctx, a.ID, app.ID, request)
	if e != nil {
		t.Fatal(e)
	}
	request.ExpectedRevision = old.Revision
	request.Endpoint = "/new-mcp"
	request.Reason = "reviewed new endpoint"
	current, e := s.SetMCPReadinessPolicy(ctx, a.ID, app.ID, request)
	if e != nil {
		t.Fatal(e)
	}
	h := &Handler{Store: s}
	call := func(method, path, revision, body string, account state.Account) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.SetPathValue("slug", app.Slug)
		if revision != "" {
			r.SetPathValue("revision", revision)
		}
		w := httptest.NewRecorder()
		h.PolicyHistory(w, r, account)
		return w
	}
	base := "/v1/apps/history-http/mcp-readiness-policy"
	w := call("GET", base+"/history?scope=staging&limit=1", "", "", a)
	var page api.MCPReadinessPolicyHistory
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Policies) != 1 || page.Policies[0].Revision != current.Revision || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call("GET", base+"/diff?scope=staging&from_revision="+old.Revision, "", "", a)
	var diff api.MCPReadinessPolicyDiff
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &diff) != nil || len(diff.Changes) != 1 || diff.Changes[0].Path != "/endpoint" {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, query := range []string{"scope=staging&scope=default", "scope=staging&limit=0", "scope=staging&limit=51", "scope=staging&unknown=yes", "scope=%zz"} {
		w = call("GET", base+"/history?"+query, "", "", a)
		if w.Code != 400 {
			t.Fatal(query, w.Code)
		}
	}
	w = call("GET", base+"/history/"+old.Revision+"?scope=default", old.Revision, "", a)
	if w.Code != 404 {
		t.Fatal("scope leaked", w.Code)
	}
	foreign, e := s.CreateAccount(ctx, "foreign-history-http@example.com", api.PlanPro)
	if e != nil {
		t.Fatal(e)
	}
	if w = call("GET", base+"/history?scope=staging", "", "", foreign); w.Code != 404 {
		t.Fatal("foreign history", w.Code)
	}
	body := `{"scope":"staging","expected_revision":"` + current.Revision + `","reason":"restore reviewed prior endpoint"}`
	w = call("POST", base+"/history/"+old.Revision+"/restore?scope=default", old.Revision, body, a)
	if w.Code != 400 {
		t.Fatal("conflicting restore scope", w.Code)
	}
	w = call("POST", base+"/history/"+old.Revision+"/restore", old.Revision, body, a)
	var restored api.MCPReadinessPolicy
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &restored) != nil || restored.RestoredFrom != old.Revision || restored.Revision == old.Revision || restored.Endpoint != "/mcp" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call("POST", base+"/history/"+old.Revision+"/restore", old.Revision, body, a); w.Code != 409 {
		t.Fatal("stale restore CAS", w.Code, w.Body.String())
	}
}
