package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAppHealth_ReadOnlyIdleAndMissingTelemetry(t *testing.T) {
	for _, plan := range []api.Plan{api.PlanFree, api.PlanPro} {
		t.Run(string(plan), func(t *testing.T) {
			e := setup(t, plan)
			app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "health-app", Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Status: state.DeployLive, TrafficPercent: 100, TrafficPercentExplicit: true, RootfsPath: "/artifact"})
			if err != nil {
				t.Fatal(err)
			}
			rec := e.do(t, http.MethodGet, "/v1/apps/health-app/health", nil, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			var got api.AppHealthResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Status != "unknown" || got.Phase != "idle" || got.AppID != app.ID || len(got.ServingDeploymentIDs) != 1 || got.ServingDeploymentIDs[0] != dep.ID {
				t.Fatalf("%+v", got)
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("assessment must not be cached by intermediaries")
			}
			instances, err := e.store.ListInstancesForApp(t.Context(), app.ID)
			if err != nil || len(instances) != 0 {
				t.Fatal("health read must not wake an idle app")
			}
		})
	}
}

func TestAppHealth_FreshMetricsAndOwnership(t *testing.T) {
	e := setup(t, api.PlanPro)
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "health-app", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Status: state.DeployLive, TrafficPercent: 100, TrafficPercentExplicit: true, RootfsPath: "/artifact"})
	if err != nil {
		t.Fatal(err)
	}
	installPromFixture(t, &e, func(q string) string {
		v := "0"
		if strings.Contains(q, "count(count by") {
			v = "1"
		}
		if strings.Contains(q, "timestamp(") {
			v = fmt.Sprint(time.Now().Unix())
		}
		return `{"status":"success","data":{"resultType":"vector","result":[{"value":[0,"` + v + `"]}]}}`
	})
	rec := e.do(t, http.MethodGet, "/v1/apps/health-app/health", nil, nil)
	var got api.AppHealthResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Status != "healthy" || got.MetricsAsOf == "" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	// Seed the other owner into the same store, not a separate test database.
	other, err := e.store.CreateAccount(t.Context(), "other-health@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	mustSeedAppFor(t, e.store, other.ID, "private-health")
	for _, slug := range []string{"private-health", "missing-health"} {
		rec = e.do(t, http.MethodGet, "/v1/apps/"+slug+"/health", nil, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: %d %s", slug, rec.Code, rec.Body)
		}
	}
}

func TestAppHealth_ReadScopeRequired(t *testing.T) {
	e := setupWithScopes(t, []string{api.ScopeDeployWrite})
	mustSeedApp(t, e, "health-app")
	rec := e.do(t, http.MethodGet, "/v1/apps/health-app/health", nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestAppHealth_DefaultRequestEvidenceExcludesPreviewAndDarkReleases(t *testing.T) {
	e := setup(t, api.PlanPro)
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "scoped-health", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	serving, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "default", Status: state.DeployLive, TrafficPercent: 100, TrafficPercentExplicit: true, RootfsPath: "/artifact"})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "pr-42", Status: state.DeployLive, TrafficPercent: 100, TrafficPercentExplicit: true, RootfsPath: "/artifact"})
	if err != nil {
		t.Fatal(err)
	}
	dark, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "default", Status: state.DeployLive, TrafficPercent: 0, TrafficPercentExplicit: true, RootfsPath: "/artifact"})
	if err != nil {
		t.Fatal(err)
	}
	installPromFixture(t, &e, func(q string) string {
		v := "100"
		switch {
		case strings.Contains(q, "timestamp("):
			v = fmt.Sprint(time.Now().Unix())
		case strings.Contains(q, "count(count by"):
			v = "1"
		case strings.Contains(q, "|__other__"):
			v = "0"
		case strings.Contains(q, `class="5xx"`):
			// Preview failures would dominate an app-wide read.
			v = "100"
			if strings.Contains(q, serving.ID) && !strings.Contains(q, preview.ID) && !strings.Contains(q, dark.ID) {
				v = "0"
			}
		}
		return `{"status":"success","data":{"resultType":"vector","result":[{"value":[0,"` + v + `"]}]}}`
	})
	rec := e.do(t, http.MethodGet, "/v1/apps/scoped-health/health", nil, nil)
	var got api.AppHealthResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Status != "healthy" || got.Requests == nil || !got.Requests.Known || got.Requests.ServerErrors != 0 || len(got.Requests.DeploymentIDs) != 1 || got.Requests.DeploymentIDs[0] != serving.ID {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// A failed history read must not discard independent serving/readiness evidence.
type healthHistoryFailStore struct{ state.Store }

func (s healthHistoryFailStore) ListDeploymentsForApp(context.Context, string, int, int) ([]state.Deployment, error) {
	return nil, errors.New("private history backend error")
}
func (s healthHistoryFailStore) ListActiveInstancesForApp(ctx context.Context, appID string, limit int) ([]state.Instance, error) {
	return s.Store.(interface {
		ListActiveInstancesForApp(context.Context, string, int) ([]state.Instance, error)
	}).ListActiveInstancesForApp(ctx, appID, limit)
}

func TestAppHealth_HistoryFailureRetainsServingEvidence(t *testing.T) {
	e := setup(t, api.PlanPro)
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "partial-health", Status: state.AppActive, MinInstances: 1})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Status: state.DeployLive, TrafficPercent: 100, TrafficPercentExplicit: true, RootfsPath: "/artifact"})
	if err != nil {
		t.Fatal(err)
	}
	node, err := e.store.CreateComputeNode(t.Context(), state.ComputeNode{Name: "health-node", Lifecycle: state.NodeLifecycleActive})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.store.CreateInstance(t.Context(), app.ID, dep.ID, string(state.StateRunning), 128, node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	e.s.store = healthHistoryFailStore{Store: e.store}
	rec := e.do(t, http.MethodGet, "/v1/apps/partial-health/health", nil, nil)
	var got api.AppHealthResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if got.Status != "unknown" || !got.Capacity.Known || got.Capacity.Ready != 1 || len(got.ServingDeploymentIDs) != 1 {
		t.Fatalf("%+v", got)
	}
	if strings.Contains(rec.Body.String(), "private history") {
		t.Fatal("raw store error leaked")
	}
}

type healthTruncatedStore struct{ state.Store }

func (s healthTruncatedStore) ListActiveInstancesForApp(context.Context, string, int) ([]state.Instance, error) {
	return make([]state.Instance, api.AppHealthInstanceLimit+1), nil
}

func TestAppHealth_TruncatedInstancesAreUnconfirmed(t *testing.T) {
	e := setup(t, api.PlanFree)
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "bounded-health", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Status: state.DeployLive, TrafficPercent: 100, TrafficPercentExplicit: true, RootfsPath: "/artifact"})
	if err != nil {
		t.Fatal(err)
	}
	e.s.store = healthTruncatedStore{Store: e.store}
	rec := e.do(t, http.MethodGet, "/v1/apps/bounded-health/health", nil, nil)
	var got api.AppHealthResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Capacity.Known || got.Status != "unknown" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}
