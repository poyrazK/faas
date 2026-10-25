package apidserver

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http/httptest"
	"strings"
	"testing"
)

func stagingAPIIntent(t *testing.T, e testEnv) api.DeploymentStagingRequest {
	t.Helper()
	r := e.do(t, "GET", "/v1/apps/staging/deployment-staging", nil, nil)
	if r.Code != 200 {
		t.Fatalf("snapshot=%d %s", r.Code, r.Body)
	}
	var snap api.DeploymentStagingSnapshot
	if err := json.Unmarshal(r.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	return api.DeploymentStagingRequest{SessionID: strings.Repeat("a", 32), Operation: "stage", ExpectedAppID: snap.AppID, ExpectedRevision: snap.Revision, ExpectedDeploymentID: snap.LatestDeploymentID,
		Before: snap.Settings, Desired: map[string]json.RawMessage{"profiling": json.RawMessage(`{"enabled":false,"window_seconds":10}`), "tracing": json.RawMessage(`{"enabled":false,"sample_ratio":0.2}`)}}
}

func TestDeploymentPolicyStagingAPI(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "staging"}, nil)
	p := stagingAPIIntent(t, e)
	path := "/v1/apps/staging/deployment-staging"
	for range 2 {
		r := e.do(t, "POST", path, p, nil)
		if r.Code != 200 {
			t.Fatalf("stage=%d %s", r.Code, r.Body)
		}
	}
	app, err := e.store.AppByID(t.Context(), p.ExpectedAppID)
	if err != nil || app.Manifest.Profiling == nil || app.Manifest.Profiling.WindowSeconds != 10 {
		t.Fatalf("staged app=%+v err=%v", app, err)
	}
	timeout := 30
	r := e.do(t, "PATCH", "/v1/apps/staging", api.UpdateAppRequest{RequestTimeoutS: &timeout}, nil)
	if r.Code != 200 {
		t.Fatalf("unrelated edit=%d %s", r.Code, r.Body)
	}
	p.Operation = "restore"
	for range 2 {
		r := e.do(t, "POST", path, p, nil)
		if r.Code != 200 {
			t.Fatalf("restore=%d %s", r.Code, r.Body)
		}
	}
	app, err = e.store.AppByID(t.Context(), p.ExpectedAppID)
	if err != nil || app.Manifest.Profiling != nil || app.Manifest.Tracing != nil || app.Manifest.RequestTimeoutS != 30 {
		t.Fatalf("cleanup=%+v err=%v", app.Manifest, err)
	}
	p.Operation = "stage"
	r = e.do(t, "POST", path, p, nil)
	if r.Code != 409 {
		t.Fatalf("late stage=%d %s", r.Code, r.Body)
	}
	deps, err := e.store.ListDeploymentsForApp(t.Context(), app.ID, 10, 0)
	if err != nil || len(deps) != 0 {
		t.Fatalf("policies submitted builds: %+v err=%v", deps, err)
	}
}

func TestDeploymentPolicyStagingAPIPreservesExplicitSameValueEdit(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "staging"}, nil)
	p := stagingAPIIntent(t, e)
	path := "/v1/apps/staging/deployment-staging"
	r := e.do(t, "POST", path, p, nil)
	if r.Code != 200 {
		t.Fatalf("stage=%d %s", r.Code, r.Body)
	}
	r = e.do(t, "PATCH", "/v1/apps/staging", api.UpdateAppRequest{Profiling: &api.ProfilingConfig{Enabled: false, WindowSeconds: 10}}, nil)
	if r.Code != 200 {
		t.Fatalf("user edit=%d %s", r.Code, r.Body)
	}
	p.Operation = "restore"
	r = e.do(t, "POST", path, p, nil)
	var problem api.Problem
	_ = json.Unmarshal(r.Body.Bytes(), &problem)
	if r.Code != 409 || problem.Code != api.CodeDeploymentStagingConflict {
		t.Fatalf("cleanup=%d %s", r.Code, r.Body)
	}
	app, _ := e.store.AppByID(t.Context(), p.ExpectedAppID)
	if app.Manifest.Profiling == nil || app.Manifest.Profiling.WindowSeconds != 10 {
		t.Fatal("cleanup overwrote deliberate user policy")
	}
}

func TestDeploymentPolicyStagingAPIScopeAndValidation(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "staging"}, nil)
	p := stagingAPIIntent(t, e)
	key, hash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "read", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "POST"} {
		body := strings.NewReader("")
		if method == "POST" {
			data, _ := json.Marshal(p)
			body = strings.NewReader(string(data))
		}
		r := httptest.NewRequest(method, "/v1/apps/staging/deployment-staging", body)
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, r)
		want := 200
		if method == "POST" {
			want = 403
		}
		if w.Code != want {
			t.Fatalf("%s scope=%d %s", method, w.Code, w.Body)
		}
	}
	p.Desired["profiling"] = json.RawMessage(`{"enabled":true,"secret":"value"}`)
	r := e.do(t, "POST", "/v1/apps/staging/deployment-staging", p, nil)
	if r.Code != 400 {
		t.Fatalf("unsafe nested field=%d %s", r.Code, r.Body)
	}
	p = stagingAPIIntent(t, e)
	p.ExpectedAppID = "another-app"
	r = e.do(t, "POST", "/v1/apps/staging/deployment-staging", p, nil)
	if r.Code != 409 {
		t.Fatalf("wrong app=%d %s", r.Code, r.Body)
	}
	r = e.do(t, "GET", "/v1/apps/missing/deployment-staging", nil, nil)
	if r.Code != 404 {
		t.Fatalf("missing=%d %s", r.Code, r.Body)
	}
}

func TestDeploymentPolicyStagingAPIRequiresInstallationSupport(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "staging"}, nil)
	p := stagingAPIIntent(t, e)
	p.Desired["profiling"] = json.RawMessage(`{"enabled":true}`)
	r := e.do(t, "POST", "/v1/apps/staging/deployment-staging", p, nil)
	if r.Code != 503 {
		t.Fatalf("unavailable profiling=%d %s", r.Code, r.Body)
	}
	app, _ := e.store.AppByID(t.Context(), p.ExpectedAppID)
	if app.Manifest.Profiling != nil || app.Manifest.Tracing != nil {
		t.Fatal("unavailable stage changed policies")
	}
}
