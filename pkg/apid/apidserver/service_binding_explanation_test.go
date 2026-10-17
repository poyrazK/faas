package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDeploymentServiceExplanationAPI(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	caller, deployment := apidserver.SeedAppTaskDeployment(t, e, "explain-caller")
	target, _ := apidserver.SeedAppTaskDeployment(t, e, "explain-target")
	base := "/v1/apps/" + caller.Slug + "/bindings/services/" + target.Slug + "/explanation"
	read := func(query url.Values) api.ServiceBindingExplanation {
		t.Helper()
		r := e.Do(t, "GET", base+"?"+query.Encode(), nil, nil)
		if r.Code != 200 || r.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("explain=%d %s", r.Code, r.Body)
		}
		var out api.ServiceBindingExplanation
		if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(r.Body.String(), "PRIVATE_QUERY") {
			t.Fatal("query leaked")
		}
		return out
	}
	query := url.Values{"deployment_id": {deployment.ID}, "path": {"/invoices?token=PRIVATE_QUERY"}}
	got := read(query)
	if got.Result != "allowed" || got.CallerDeploymentID != deployment.ID || got.TargetAppID != target.ID || got.Scope != "default" || got.PolicySource != "app" || got.TargetDeploymentID != "" || got.ReleaseID != "" {
		t.Fatalf("report=%+v", got)
	}
	query.Set("scope", "staging")
	if got := read(query); got.Result != "denied" || got.Checks[0].Reason != "deployment_scope_mismatch" {
		t.Fatalf("scope=%+v", got)
	}
	query.Del("scope")
	if err := (*e.Store()).UpdateDeploymentStatus(t.Context(), deployment.ID, state.DeploySuperseded, ""); err != nil {
		t.Fatal(err)
	}
	if got := read(query); got.Result != "denied" || got.Checks[0].Reason != "caller_deployment_not_live" {
		t.Fatalf("retired=%+v", got)
	}
	for _, tc := range []struct {
		query  string
		status int
	}{
		{"", 400}, {"deployment_id=invalid", 400}, {"deployment_id=" + uuid.Nil.String(), 400},
		{"deployment_id=" + uuid.NewString(), 404},
		{"deployment_id=" + deployment.ID + "&path=%2Fa%2F..%2Fb", 400},
		{"deployment_id=" + deployment.ID + "&scope=production&scope=staging", 400},
		{"deployment_id=" + deployment.ID + "&release_id=ignored", 400},
		{"deployment_id=" + deployment.ID + "&scope=%ZZ", 400},
	} {
		r := e.Do(t, http.MethodGet, base+"?"+tc.query, nil, nil)
		if r.Code != tc.status {
			t.Errorf("query %s: %d %s", tc.query, r.Code, r.Body)
		}
	}
	// Another owned app's deployment is still not a caller deployment.
	_, foreign := apidserver.SeedAppTaskDeployment(t, e, "explain-foreign")
	r := e.Do(t, http.MethodGet, base+"?deployment_id="+foreign.ID, nil, nil)
	if r.Code != 404 {
		t.Fatalf("foreign deployment=%d %s", r.Code, r.Body)
	}
	tasks, err := (*e.Store()).ListAppTasks(t.Context(), (*e.Acct()).ID, caller.ID, 10, 0)
	if err != nil || len(tasks) != 0 {
		t.Fatalf("explain created tasks: %+v %v", tasks, err)
	}
}

func TestDeploymentServiceExplanationReadBoundaries(t *testing.T) {
	for _, tc := range []struct {
		scopes []string
		status int
	}{
		{[]string{api.ScopeAppsRead}, 200},
		{[]string{api.ScopeDeployWrite}, 403},
	} {
		e := apidserver.SetupWithScopes(t, tc.scopes)
		// Seed through the fixture store: this API key deliberately cannot
		// create apps when it has only read permission.
		seed := func(slug string) state.App {
			t.Helper()
			app, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, Slug: slug, Type: state.AppTypeApp, Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			return app
		}
		caller, target := seed("explain-scoped-caller"), seed("explain-scoped-target")
		dep, err := (*e.Store()).CreateDeployment(t.Context(), state.Deployment{AppID: caller.ID, Status: state.DeployLive, Scope: "default"})
		if err != nil {
			t.Fatal(err)
		}
		path := "/v1/apps/" + caller.Slug + "/bindings/services/" + target.Slug + "/explanation?deployment_id=" + dep.ID
		r := e.Do(t, "GET", path, nil, nil)
		if r.Code != tc.status {
			t.Fatalf("scopes %v: %d %s", tc.scopes, r.Code, r.Body)
		}
		other, err := (*e.Store()).CreateAccount(t.Context(), "explain-other@example.test", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: other.ID, Slug: "explain-other"})
		if err != nil {
			t.Fatal(err)
		}
		r = e.Do(t, "GET", "/v1/apps/"+app.Slug+"/bindings/services/private/explanation?deployment_id="+dep.ID, nil, nil)
		want := 404
		if tc.status == 403 {
			want = 403
		}
		if r.Code != want {
			t.Fatalf("foreign caller: %d %s", r.Code, r.Body)
		}
	}
	// A session that has not completed MFA cannot obtain an explanation.
	e := apidserver.SetupWithMFA(t, api.PlanPro, false, false)
	e.GenerateEnrolledAccount(t)
	pending := e.MfaIssueWithPending(t, true)
	r := httptest.NewRequest("GET", "/v1/apps/private/bindings/services/private/explanation?deployment_id="+uuid.NewString(), nil)
	r.AddCookie(pending)
	w := httptest.NewRecorder()
	(*e.H()).ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("MFA pending: %d %s", w.Code, w.Body)
	}
}
