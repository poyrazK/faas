// adr: 20261104085712
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// An agent key limited to a project reaches that project and its apps, lists
// only them, sees every other app and project as missing, and is refused on
// account-wide routes; the limit is checked when the key is minted.
func TestHandlers_AgentKeyLimitedToAProject(t *testing.T) {
	env, _ := apidserver.SetupChangePlan(t, api.PlanPro, "")
	store, acct := *env.Store(), *env.Acct()
	ctx := t.Context()
	call := func(key, method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("Content-Type", "application/json")
		(*env.H()).ServeHTTP(rec, r)
		return rec
	}
	for _, p := range []struct{ project, app string }{{"api", "api-svc"}, {"web", "web-svc"}} {
		project, err := store.CreateProject(ctx, state.Project{ID: uuid.NewString(), AccountID: acct.ID, Slug: p.project})
		if err != nil {
			t.Fatalf("CreateProject %s: %v", p.project, err)
		}
		if _, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: p.app, Type: state.AppTypeApp, ProjectID: project.ID}); err != nil {
			t.Fatalf("CreateApp %s: %v", p.app, err)
		}
	}
	if _, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "loose-svc", Type: state.AppTypeApp}); err != nil {
		t.Fatal(err)
	}

	for body, want := range map[string]int{
		`{"label":"ci","scopes":["deploy:write"],"projects":["api"]}`:            http.StatusBadRequest,
		`{"label":"pg","scopes":["agent","postgres:manage"],"projects":["api"]}`: http.StatusBadRequest,
		`{"label":"missing","scopes":["agent"],"projects":["no-such-project"]}`:  http.StatusNotFound,
	} {
		if rec := call(*env.Key(), http.MethodPost, "/v1/keys", body); rec.Code != want {
			t.Fatalf("POST /v1/keys %s = %d %s, want %d", body, rec.Code, rec.Body, want)
		}
	}
	rec := call(*env.Key(), http.MethodPost, "/v1/keys", `{"label":"api-agent","scopes":["agent"],"projects":["api","api"]}`)
	var created api.APIKeyResponse
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &created) != nil || !slices.Equal(created.Projects, []string{"api"}) {
		t.Fatalf("limited agent key = %d %s", rec.Code, rec.Body)
	}
	agent := created.Plaintext

	var apps []api.AppResponse
	if rec := call(agent, http.MethodGet, "/v1/apps", ""); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &apps) != nil || len(apps) != 1 || apps[0].Slug != "api-svc" {
		t.Fatalf("GET /v1/apps = %d %s; want only api-svc", rec.Code, rec.Body)
	}
	var projects []api.ProjectSummaryResponse
	if rec := call(agent, http.MethodGet, "/v1/projects", ""); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &projects) != nil || len(projects) != 1 || projects[0].Slug != "api" {
		t.Fatalf("GET /v1/projects = %d %s; want only api", rec.Code, rec.Body)
	}
	for path, want := range map[string]int{
		"/v1/apps/api-svc":       http.StatusOK,
		"/v1/apps/web-svc":       http.StatusNotFound,
		"/v1/apps/loose-svc":     http.StatusNotFound,
		"/v1/projects/api":       http.StatusOK,
		"/v1/projects/web":       http.StatusNotFound,
		"/v1/account":            http.StatusOK,
		"/v1/keys":               http.StatusForbidden,
		"/v1/postgres/databases": http.StatusForbidden,
	} {
		if rec := call(agent, http.MethodGet, path, ""); rec.Code != want {
			t.Errorf("GET %s with the limited key = %d %s, want %d", path, rec.Code, rec.Body, want)
		}
	}
	// The account's own key is not limited.
	if rec := call(*env.Key(), http.MethodGet, "/v1/apps/web-svc", ""); rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/apps/web-svc with the account key = %d", rec.Code)
	}
}
