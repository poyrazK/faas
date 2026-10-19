package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestMCPReadinessMaintenanceAuthorization(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	app := apidserver.SeedAppForTimeline(t, e, "maintenance-auth")
	path := "/v1/apps/" + app.Slug + "/mcp-readiness-maintenance"
	admin := *e.Key()
	reader, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (*e.Store()).CreateAPIKey(t.Context(), (*e.Acct()).ID, hash, "maintenance reader", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	body := api.CreateMCPReadinessMaintenanceRequest{Scope: "staging", EndsAt: time.Now().Add(time.Hour).UTC(), Reason: "upgrade"}
	*e.Key() = reader
	if w := e.Do(t, "GET", path+"?scope=staging", nil, nil); w.Code != 200 {
		t.Fatal("reader denied", w.Code, w.Body.String())
	}
	if w := e.Do(t, "POST", path, body, nil); w.Code != 403 {
		t.Fatal("reader created window", w.Code, w.Body.String())
	}
	*e.Key() = admin
	w := e.Do(t, "POST", path, body, nil)
	var window api.MCPReadinessMaintenanceWindow
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &window) != nil {
		t.Fatal("admin create", w.Code, w.Body.String())
	}
	*e.Key() = reader
	if w := e.Do(t, "POST", path+"/"+window.ID+"/cancel", api.CancelMCPReadinessMaintenanceRequest{Reason: "reader"}, nil); w.Code != 403 {
		t.Fatal("reader cancelled", w.Code, w.Body.String())
	}
	*e.Key() = admin
	if err = (*e.Store()).MarkMFAEnrolled(t.Context(), (*e.Acct()).ID); err != nil {
		t.Fatal(err)
	}
	sid := uuid.NewString()
	if _, err = (*e.Store()).CreateSession(t.Context(), sid, (*e.Acct()).ID, "192.0.2.10", "maintenance-test"); err != nil {
		t.Fatal(err)
	}
	token, err := (*(*e.S()).Sessions()).IssueWithSession(sid, (*e.Acct()).ID, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", path, strings.NewReader(string(raw)))
	r.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: token})
	response := httptest.NewRecorder()
	(*e.H()).ServeHTTP(response, r)
	if response.Code != 403 {
		t.Fatal("pending MFA created window", response.Code, response.Body.String())
	}
}
