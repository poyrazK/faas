//go:build promotionacceptance && metal

package e2e_test

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func promotionServiceGraphNativeFiles(binary []byte, version string) fstest.MapFS {
	files := projectServiceGraphNativeFiles(binary, version)
	files["docker-compose.yml"].Data = []byte(strings.Replace(string(files["docker-compose.yml"].Data), "  backend:\n", "  backend:\n    x-gregale-allow-callers: [frontend]\n    x-gregale-allow-call-scopes:\n      frontend:\n        methods: [GET]\n        path_prefixes: [/promotion-protocol]\n", 1))
	for _, role := range []string{"frontend", "backend"} {
		file := files["Dockerfile."+role]
		copy := *file
		copy.Data = []byte(strings.Replace(string(file.Data), "EXPOSE", "ENV PROMOTION_SERVICE_GRAPH=1 GRAPH_ROLE="+role+"\nEXPOSE", 1))
		files["Dockerfile."+role] = &copy
	}
	return files
}

func TestProjectEnvironmentPromotionServiceGraphFixtureMeta(t *testing.T) {
	scan, err := reposcan.Scan(promotionServiceGraphNativeFiles([]byte("fixture"), "tested"))
	mustProjectWorkerNative(t, err)
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "promotion-service-graph@example.test", api.PlanScale)
	mustProjectWorkerNative(t, err)
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "promotion-service-graph"})
	mustProjectWorkerNative(t, err)
	plan, err := reconcile.NewService(store, nil, nil).Plan(t.Context(), project, scan, "", "", nil)
	mustProjectWorkerNative(t, err)
	apps, err := reconcile.ProjectSafeSourceApps(plan.Added, scan)
	mustProjectWorkerNative(t, err)
	if len(apps) != 2 {
		t.Fatal("service graph fixture must contain two HTTP workloads")
	}
	for _, app := range apps {
		if app.WorkloadName == "frontend" && (len(app.Manifest.ServiceBindings) != 1 || app.Manifest.ServiceBindings[0].Service != "backend") {
			t.Fatal("frontend lost declared dependency")
		}
		if app.WorkloadName == "backend" && (app.Manifest.AllowedServiceCallers == nil || app.Manifest.AllowedServiceCallScopes == nil) {
			t.Fatal("API lost target-owned caller grants")
		}
		if app.WorkloadClass == state.WorkloadClassWorker {
			t.Fatal("service graph fixture admitted a worker")
		}
	}
}

func newPromotionServiceGraphNative(t *testing.T) *environmentPromotionNative {
	t.Helper()
	setup := func(f *projectWorkerNativeFixture) { configurePromotionGraphDestination(t, f) }
	stage := func(f *projectWorkerNativeFixture) { configurePromotionGraphSource(t, f) }
	p := newEnvironmentPromotionNativeWithOptions(t, true, nil, promotionNativeOptions{serviceGraph: true, files: promotionServiceGraphNativeFiles, beforeDeploy: setup, beforeStaging: stage, environment: []string{"FAAS_PROMOTION_SERVICE_GRAPH_ENABLED=1"}})
	for _, app := range p.apps {
		gate, err := p.owner.GetRouteHealthGate(t.Context(), app.Slug)
		mustProjectWorkerNative(t, err)
		_, err = p.owner.SetRouteHealthGate(t.Context(), app.Slug, api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &gate.Revision, Routes: []api.RouteHealthRoute{{Method: "GET", Path: "/critical", Probe: &api.RouteHealthProbe{Path: "/critical"}}}})
		mustProjectWorkerNative(t, err)
	}
	return p
}

func (p *environmentPromotionNative) graphInstance(t *testing.T, appID, deployment string) state.Instance {
	t.Helper()
	var found state.Instance
	waitProjectWorkerNative(t, time.Minute, "exact graph guest instance", func() bool {
		instances, err := p.store.ListInstancesForApp(t.Context(), appID)
		mustProjectWorkerNative(t, err)
		for _, instance := range instances {
			if instance.DeploymentID == deployment && instance.State == string(state.StateRunning) && instance.HostIP != "" {
				found = instance
				return true
			}
		}
		return false
	})
	return found
}

func graphGuestRequest(t *testing.T, instance state.Instance, path, method string) ([]byte, int) {
	t.Helper()
	return projectWorkerNativeRequest(t, "http://"+net.JoinHostPort(instance.HostIP, "8080")+path, "", method)
}

func (p *environmentPromotionNative) graphReport(t *testing.T, frontend state.Instance, backend, graph, generation, transport string) projectWorkerNativeReport {
	t.Helper()
	body, status := graphGuestRequest(t, frontend, "/", http.MethodGet)
	var report projectWorkerNativeReport
	mustProjectWorkerNative(t, json.Unmarshal(body, &report))
	if status != http.StatusOK || report.Generation != generation || report.Mode != generation+"-config" || report.DeploymentID != frontend.DeploymentID || report.InstanceID != frontend.ID || report.BackendDeploymentID != backend || report.ReleaseID != graph || report.BackendReleaseID != graph || report.BackendGeneration != generation || report.BackendMode != generation+"-config" || !report.DestinationSecretMatches || !report.BackendDestinationSecretMatches || report.BindingTransport != transport {
		t.Fatalf("mixed service graph runtime: status=%d report=%+v", status, report)
	}
	return report
}

func graphAttack(t *testing.T, base, host, target string, headers map[string]string) int {
	t.Helper()
	query := url.Values{}
	if target != "" {
		query.Set("target", target)
	}
	for key, value := range headers {
		query.Set(key, value)
	}
	body, status := projectWorkerNativeRequest(t, base+"/service-attack?"+query.Encode(), host, http.MethodGet)
	var result struct {
		Status int `json:"upstream_status"`
	}
	if status != http.StatusOK || json.Unmarshal(body, &result) != nil {
		t.Fatal("service isolation request did not reach the real proxy")
	}
	return result.Status
}

// The foreign guest has a real source-built image and its own account. It can
// attempt the known target URL, but cannot acquire the owner's network identity.
func newPromotionGraphForeignCaller(t *testing.T, p *environmentPromotionNative) *projectWorkerNativeFixture {
	t.Helper()
	files := func(binary []byte, version string) fstest.MapFS {
		result := promotionServiceGraphNativeFiles(binary, version)
		delete(result, "Dockerfile.backend")
		result["Dockerfile.frontend"].Data = []byte(strings.Replace(string(result["Dockerfile.frontend"].Data), "GRAPH_ROLE=frontend", "GRAPH_ROLE=attack", 1))
		result["docker-compose.yml"].Data = []byte("services:\n  frontend:\n    build: { context: ., dockerfile: Dockerfile.frontend }\n    ports: [\"8080:8080\"]\n")
		return result
	}
	return newProjectReleaseNativeFixtureOnHarness(t, 3600, nil, true, false, files, p.h, api.PlanPro)
}

func decodeGraphReport(t *testing.T, body []byte) projectWorkerNativeReport {
	t.Helper()
	var report projectWorkerNativeReport
	mustProjectWorkerNative(t, json.Unmarshal(body, &report))
	return report
}

func configurePromotionGraphDestination(t *testing.T, f *projectWorkerNativeFixture) {
	for name, app := range f.apps {
		if name == "processor" {
			continue
		}
		manifest := app.Manifest
		manifest.ServiceBindingPolicy = api.ServiceBindingPolicyDeclared
		manifest.ServiceBindingTransport = api.ServiceBindingTransportHTTPS
		if name == "backend" {
			manifest.AllowedServiceCallers = new([]string{"frontend"})
			manifest.AllowedServiceCallScopes = new(api.ServiceCallerScopes{"frontend": {Methods: []string{"GET"}, PathPrefixes: []string{"/promotion-protocol"}}})
		}
		updated, err := f.store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &manifest, MaxConcurrency: new(3)})
		mustProjectWorkerNative(t, err)
		f.apps[name] = updated
		if name == "frontend" {
			for key, value := range api.ServiceBindingEnvForTransport(nil, manifest.ServiceBindings, manifest.ServiceBindingTransport) {
				mustProjectWorkerNative(t, f.store.UpsertAppEnvInScope(t.Context(), f.accountID, app.ID, "production", key, value))
			}
		}
	}
}

func configurePromotionGraphSource(t *testing.T, f *projectWorkerNativeFixture) {
	for name, app := range f.apps {
		if name == "processor" {
			continue
		}
		settings, err := state.WorkloadSettingsFromApp(app)
		mustProjectWorkerNative(t, err)
		settings.Manifest.ServiceBindingTransport = api.ServiceBindingTransportHTTP
		spec, err := f.store.ProjectEnvironmentWorkloadSpec(t.Context(), f.accountID, f.project.ID, "staging", app.ID)
		if !errors.Is(err, state.ErrNotFound) {
			mustProjectWorkerNative(t, err)
		}
		_, err = f.store.PutProjectEnvironmentWorkloadSpec(t.Context(), f.accountID, f.project.ID, "staging", app.ID, spec.Revision, settings)
		mustProjectWorkerNative(t, err)
		if name == "frontend" {
			for key, value := range api.ServiceBindingEnvForTransport(nil, settings.Manifest.ServiceBindings, settings.Manifest.ServiceBindingTransport) {
				mustProjectWorkerNative(t, f.store.UpsertAppEnvInScope(t.Context(), f.accountID, app.ID, "staging", key, value))
			}
		}
	}
}
