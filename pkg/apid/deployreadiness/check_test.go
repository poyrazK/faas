package deployreadiness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261019120041
func TestReadinessCollectsBlockersAndTracksLogicalChanges(t *testing.T) {
	store := state.NewMemStore()
	account, project, app := seedReadiness(t, store)
	request := readinessRequest()
	request.Workloads[0].Workload.EnvKeys = []string{"TOKEN", "REGION"}
	first := Check(t.Context(), store, account, project.Slug, request, Config{})
	if first.Status != "blocked" || countCode(first, "runtime_key_missing") != 2 {
		t.Fatalf("missing keys were not collected: %+v", first)
	}
	must(t, store.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, "production", "REGION", "private-value"))
	request.Workloads[0].ProvidedKeys = []string{"TOKEN"}
	ready := Check(t.Context(), store, account, project.Slug, request, Config{})
	if ready.Status != "ready" || len(ready.StateHash) != 64 || ready.RuntimeChecked {
		t.Fatalf("configured inputs did not pass: %+v", ready)
	}
	raw, _ := json.Marshal(ready)
	if bytes.Contains(raw, []byte("private-value")) {
		t.Fatal("configuration value entered report")
	}
	request.ExpectedStateHash = ready.StateHash
	if same := Check(t.Context(), store, account, project.Slug, request, Config{}); same.Status != "ready" || same.StateHash != ready.StateHash {
		t.Fatalf("unchanged inputs drifted: %+v", same)
	}
	must(t, store.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, "production", "REGION", "new-private-value"))
	changed := Check(t.Context(), store, account, project.Slug, request, Config{})
	if changed.Status != "blocked" || countCode(changed, "deployment_inputs_changed") != 1 {
		t.Fatalf("changed configuration was not detected: %+v", changed)
	}
}

// adr: 20261019120041
func TestReadinessUnknownAndHTTPStrictness(t *testing.T) {
	store := state.NewMemStore()
	account, project, _ := seedReadiness(t, store)
	unknown := Check(t.Context(), unavailableUsage{Reader: store}, account, project.Slug, readinessRequest(), Config{})
	if unknown.Status != "unknown" || unknown.StateHash != "" {
		t.Fatalf("unavailable usage passed: %+v", unknown)
	}
	raw, _ := json.Marshal(unknown)
	if bytes.Contains(raw, []byte("private-store-error")) {
		t.Fatal("store error leaked")
	}
	for _, body := range []string{`{"environment":"production","unexpected":true}`, `{} {}`, `{"workloads":[]}`, `{"environment":"production","workloads":[{"workload":{"name":"api","root_dir":"../escape"}}]}`} {
		req := httptest.NewRequest(http.MethodPost, "/readiness", strings.NewReader(body))
		req.SetPathValue("slug", project.Slug)
		rec := httptest.NewRecorder()
		Handle(rec, req, account, store, Config{})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("malformed request accepted: %d %s", rec.Code, rec.Body)
		}
	}
	before, err := store.AppsForProject(t.Context(), account.ID, project.ID)
	must(t, err)
	request := readinessRequest()
	data, _ := json.Marshal(request)
	req := httptest.NewRequest(http.MethodPost, "/readiness", bytes.NewReader(data))
	req.SetPathValue("slug", project.Slug)
	rec := httptest.NewRecorder()
	Handle(rec, req, account, store, Config{})
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("read-only check failed: %d %s", rec.Code, rec.Body)
	}
	after, err := store.AppsForProject(t.Context(), account.ID, project.ID)
	must(t, err)
	if len(before) != len(after) || before[0].ID != after[0].ID {
		t.Fatal("readiness changed project membership")
	}
}

// adr: 20261019120041
func TestReadinessReportsIndependentReleaseAndDependencyFailures(t *testing.T) {
	store := state.NewMemStore()
	account, project, _ := seedReadiness(t, store)
	request := readinessRequest()
	request.Atomic = true
	request.Verify = true
	request.Workers = true
	request.Progressive = true
	request.Workloads[0].Workload.DependsOn = []string{"missing"}
	result := Check(t.Context(), store, account, project.Slug, request, Config{})
	for _, code := range []string{"dependency_invalid", "worker_release_unqualified", "progressive_release_unqualified", "release_mode_invalid", "baseline_required"} {
		if countCode(result, code) == 0 {
			t.Fatalf("missing %s: %+v", code, result)
		}
	}
	other, err := store.CreateAccount(t.Context(), "other-readiness@example.test", api.PlanPro)
	must(t, err)
	request = readinessRequest()
	request.Workloads[0].Workload.EnvKeys = []string{"TENANT_TOKEN"}
	if got := Check(t.Context(), store, other, project.Slug, request, Config{}); got.Status != "blocked" || countCode(got, "runtime_key_missing") != 1 {
		t.Fatal("another account's workload was used", got)
	}
}

// adr: 20261019120041
func TestReadinessMetadataBoundsAndManagedBindings(t *testing.T) {
	store := state.NewMemStore()
	account, project, _ := seedReadiness(t, store)
	request := readinessRequest()
	request.Managed = []string{"db"}
	request.Workloads[0].Workload.DependsOn = []string{"db"}
	request.Workloads[0].Workload.EnvKeys = []string{api.ServiceBindingEnvKey("db")}
	if !ValidateRequest(project.Slug, request) {
		t.Fatal("valid short dependency name was refused")
	}
	missing := Check(t.Context(), store, account, project.Slug, request, Config{})
	if countCode(missing, "runtime_key_missing") != 1 {
		t.Fatal("external managed dependency invented a local service URL", missing)
	}
	request.Managed = nil
	request.Workloads = append(request.Workloads, api.ProjectDeploymentCheckWorkload{Workload: api.PlanWorkload{Name: "db", Class: "http"}})
	if ready := Check(t.Context(), store, account, project.Slug, request, Config{}); ready.Status != "ready" {
		t.Fatal("declared workload binding did not pass", ready)
	}
	request = api.ProjectDeploymentCheckRequest{Environment: "production"}
	for i := 0; i < 6; i++ {
		w := api.PlanWorkload{Name: fmt.Sprintf("api-%d", i), Class: "http"}
		for k := 0; k < api.ProjectDeploymentCheckMaxKeys; k++ {
			w.EnvKeys = append(w.EnvKeys, fmt.Sprintf("KEY_%03d", k))
		}
		request.Workloads = append(request.Workloads, api.ProjectDeploymentCheckWorkload{Workload: w})
	}
	bounded := Check(t.Context(), store, account, "fresh", request, Config{})
	if len(bounded.Checks) > api.ProjectDeploymentCheckMaxChecks || countCode(bounded, "readiness_report_truncated") != 1 || bounded.StateHash != "" || bounded.Status == "ready" {
		t.Fatal("truncated report manufactured readiness", bounded)
	}
}

type unavailableUsage struct{ Reader }

func (unavailableUsage) CountDeployedApps(context.Context, string) (int, error) {
	return 0, errors.New("private-store-error")
}

func readinessRequest() api.ProjectDeploymentCheckRequest {
	return api.ProjectDeploymentCheckRequest{Environment: "production", Workloads: []api.ProjectDeploymentCheckWorkload{{Workload: api.PlanWorkload{Name: "api", Class: "http", Tier: "compose", Ports: []int{}, Command: []string{}}}}}
}
func countCode(r api.ProjectDeploymentReadiness, code string) int {
	n := 0
	for _, c := range r.Checks {
		if c.Code == code {
			n++
		}
	}
	return n
}
func seedReadiness(t *testing.T, store state.Store) (state.Account, state.Project, state.App) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "readiness@example.test", api.PlanPro)
	must(t, err)
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "shop"})
	must(t, err)
	_, err = store.UpdateProjectEnvironmentProtection(ctx, account.ID, project.ID, "production", false)
	must(t, err)
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "shop-api", WorkloadName: "api", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassHTTP, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 5})
	must(t, err)
	return account, project, app
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
