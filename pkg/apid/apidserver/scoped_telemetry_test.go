package apidserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type scopedRoutesTestStore struct{ state.Store }

func (s scopedRoutesTestStore) ListScopedObservedRoutes(_ context.Context, _ string, _ string, scope string, _ time.Time, _ time.Time) ([]string, error) {
	return []string{"GET /" + scope}, nil
}

func TestScopedTelemetryHTTP(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := t.Context()
	project, err := e.store.CreateProject(ctx, state.Project{AccountID: e.acct.ID, Slug: "telemetry"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "telemetry-app", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	env, err := e.store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"routes", "operational-summary"} {
		for _, query := range []string{"?scope=default&environment=staging", "?scope=all", "?environment=staging&environment=production", "?environment=missing"} {
			r := e.do(t, http.MethodGet, "/v1/apps/telemetry-app/"+path+query, nil, nil)
			if r.Code == http.StatusOK {
				t.Fatal("invalid scope returned aggregate telemetry")
			}
		}
	}
	r := e.do(t, http.MethodGet, "/v1/apps/telemetry-app/routes?environment=staging", nil, nil)
	var routes api.AppRoutesResponse
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &routes) != nil || routes.Scope != "staging" || routes.EnvironmentID != env.ID || routes.Source != "unavailable" {
		t.Fatalf("scoped route identity=%d %s", r.Code, r.Body)
	}
	// A scoped reader contributes only the selected namespace; the aggregate
	// Prometheus/catalog paths are not used by an explicit selector.
	e.s.store = scopedRoutesTestStore{Store: e.store}
	r = e.do(t, http.MethodGet, "/v1/apps/telemetry-app/routes?scope=default", nil, nil)
	if r.Code != 200 || !strings.Contains(r.Body.String(), "GET /default") || strings.Contains(r.Body.String(), "GET /staging") {
		t.Fatal("default route snapshot mixed environments")
	}
	e.s.store = e.store
	r = e.do(t, http.MethodGet, "/v1/apps/telemetry-app/operational-summary?environment=staging", nil, nil)
	var summary api.AppOperationalSummary
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &summary) != nil || summary.EnvironmentID != env.ID || summary.Scope != "staging" || summary.Health == nil || summary.Health.Scope != "staging" || summary.Recovery.Coverage != "explicit_scope_only" {
		t.Fatalf("summary identity=%d %s", r.Code, r.Body)
	}
	if summary.Monitoring.Available || summary.Recovery.RestartsAvailable {
		t.Fatal("unattributable monitoring/restart source reported scoped availability")
	}
	if rows, err := e.store.ListInstancesForApp(ctx, app.ID); err != nil || len(rows) != 0 {
		t.Fatal("telemetry woke an app")
	}
}

func TestWorkerRolloutObservationHTTP(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := t.Context()
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, Slug: "worker-observation", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5, Status: state.AppActive, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeWorker}})
	if err != nil {
		t.Fatal(err)
	}
	base, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "default", ImageDigest: "sha256:base"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(ctx, base.ID); err != nil {
		t.Fatal(err)
	}
	old, err := e.store.CreateInstanceWithMode(ctx, app.ID, base.ID, "draining", 256, state.DefaultLocalNodeName, uuid.NewString(), "worker")
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "default", ImageDigest: "sha256:new"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(ctx, candidate.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateInstanceWithMode(ctx, app.ID, candidate.ID, "running", 256, state.DefaultLocalNodeName, uuid.NewString(), "worker"); err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/worker-observation/rollouts/" + candidate.ID + "/observation?scope=default"
	r := e.do(t, http.MethodGet, path, nil, nil)
	var out api.WorkerRolloutObservation
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &out) != nil || out.RetiredResidents != 1 || out.GenerationConverged || out.QueueProcessingConfirmed {
		t.Fatalf("drain observation=%d %s", r.Code, r.Body)
	}
	if err := e.store.UpdateInstanceState(ctx, old.ID, "stopped"); err != nil {
		t.Fatal(err)
	}
	r = e.do(t, http.MethodGet, path, nil, nil)
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &out) != nil || !out.GenerationConverged || out.QueueProcessingConfirmed {
		t.Fatal("generation convergence confused with queue processing")
	}
	if bad := e.do(t, http.MethodGet, strings.Split(path, "?")[0], nil, nil); bad.Code == 200 {
		t.Fatal("unscoped worker observation accepted")
	}
}
