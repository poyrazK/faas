package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMCPReadinessPolicyHistoryAuthorization(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	app := apidserver.SeedAppForTimeline(t, e, "history-auth")
	h := state.MCPReadinessPolicyHistoryStore(*e.Store())
	old, err := h.SetMCPReadinessPolicy(t.Context(), (*e.Acct()).ID, app.ID, api.SetMCPReadinessPolicyRequest{Scope: "staging", Endpoint: "/mcp", MaxAgeSeconds: 300, Reason: "reviewed staging opt out"})
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/apps/" + app.Slug + "/mcp-readiness-policy"
	restore := base + "/history/" + old.Revision + "/restore"
	body := api.RestoreMCPReadinessPolicyRequest{Scope: "staging", ExpectedRevision: old.Revision, Reason: "reviewed restore"}
	adminKey := *e.Key()
	readKey, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (*e.Store()).CreateAPIKey(t.Context(), (*e.Acct()).ID, hash, "history reader", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	*e.Key() = readKey
	for _, path := range []string{base + "/history?scope=staging", base + "/history/" + old.Revision + "?scope=staging", base + "/diff?scope=staging&from_revision=" + old.Revision} {
		if rec := e.Do(t, "GET", path, nil, nil); rec.Code != 200 {
			t.Fatal("read denied", rec.Code, rec.Body.String())
		}
	}
	if rec := e.Do(t, "POST", restore, body, nil); rec.Code != 403 {
		t.Fatal("reader restored policy", rec.Code, rec.Body.String())
	}
	*e.Key() = adminKey
	rec := e.Do(t, "POST", restore, body, nil)
	var restored api.MCPReadinessPolicy
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &restored) != nil || restored.RestoredFrom != old.Revision {
		t.Fatal("admin restore", rec.Code, rec.Body.String())
	}
	if err = (*e.Store()).MarkMFAEnrolled(t.Context(), (*e.Acct()).ID); err != nil {
		t.Fatal(err)
	}
	sid := uuid.NewString()
	if _, err = (*e.Store()).CreateSession(t.Context(), sid, (*e.Acct()).ID, "192.0.2.10", "history-test"); err != nil {
		t.Fatal(err)
	}
	token, err := (*(*e.S()).Sessions()).IssueWithSession(sid, (*e.Acct()).ID, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(body)
	request := httptest.NewRequest("POST", restore, strings.NewReader(string(raw)))
	request.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: token})
	response := httptest.NewRecorder()
	(*e.H()).ServeHTTP(response, request)
	if response.Code != 403 {
		t.Fatal("pending MFA restore", response.Code, response.Body.String())
	}
}
