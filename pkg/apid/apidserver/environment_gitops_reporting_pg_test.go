//go:build !no_pg

// adr: 568 — the production reporter observes a complete persisted graph without applying it.
package apidserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPGEnvironmentGitDriftReportingWholeGraphOutageAndRestart(t *testing.T) {
	env := setupPGHandler(t, api.PlanPro)
	store := state.NewPgStore(env.pool)
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: env.acct.ID, Slug: "shop", RepoFullName: "example/shop", ProductionBranch: "main", InstallID: 42})
	if err != nil {
		t.Fatal(err)
	}
	apiApp, err := store.CreateApp(t.Context(), state.App{AccountID: env.acct.ID, ProjectID: project.ID, Slug: "shop-api", WorkloadName: "api", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	workerApp, err := store.CreateApp(t.Context(), state.App{AccountID: env.acct.ID, ProjectID: project.ID, Slug: "shop-worker", WorkloadName: "worker", Type: state.AppTypeApp,
		Status: state.AppActive, WorkloadClass: state.WorkloadClassWorker, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeWorker}})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := store.CreateQueueBindingWithConsumer(t.Context(), state.QueueBinding{AccountID: env.acct.ID, AppID: workerApp.ID,
		DeploymentScope: "production", Name: "orders", QueueName: "orders", Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppEnvInScope(t.Context(), env.acct.ID, apiApp.ID, "production", "MODE", "console"); err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateEnvironmentGitSource(t.Context(), env.acct.ID, project.ID, "production", state.EnvironmentGitSourceSpec{
		RepositoryID: 123, InstallationID: 42, Repository: "example/shop", Ref: "refs/heads/main", ManifestPath: "environments/production.yaml",
		Mode: "report", ApprovalPolicy: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: "shop", Environment: "production",
		Workloads: map[string]api.EnvironmentWorkload{
			"api": {App: apiApp.Slug, Variables: map[string]string{"MODE": "approved"},
				Routes:   &api.EnvironmentRouteContract{OnlyAllowDeclaredRoutes: true, DeclaredRoutes: []api.DeclaredRoute{{Path: "/health", Methods: []string{"GET"}}}},
				Policies: &[]api.EnvironmentPolicy{{Name: "headers", Kind: "headers", Action: json.RawMessage(`{"response_headers":[{"name":"X-Test","action":"set","value":"approved"}]}`)}}},
			"worker": {App: workerApp.Slug, QueueBindings: map[string]api.EnvironmentQueueBinding{
				"orders": {QueueName: "orders-v2", Mode: "push", WorkloadClass: "worker", Enabled: &enabled, MaxConcurrency: 3}},
				QueueSmoke: map[string]api.EnvironmentQueueSmoke{"orders": {Payload: json.RawMessage(`{"id":"qualification-smoke"}`)}},
			},
		}})
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), state.ApproveEnvironmentRevision{AccountID: env.acct.ID,
		SourceID: source.ID, CommitSHA: strings.Repeat("a", 40), Desired: desired, ApprovedBy: "test-owner"})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), env.acct.ID, source.ID)
	if err != nil || !preview.CanApply() {
		t.Fatalf("adoption preview: %+v %v", preview, err)
	}
	if err := store.AdoptEnvironmentGitOps(t.Context(), env.acct.ID, source.ID, preview.Hash); err != nil {
		t.Fatal(err)
	}
	env.s.githubd = &environmentGitSourcePollingClient{
		repositories: []Repo{{ID: 123, FullName: "example/shop"}},
		err:          errors.New("Git transport unavailable"),
	}
	poller := &environmentgitops.SourcePoller{Store: store, Reader: &environmentGitSourceReader{server: env.s},
		LeaseDuration: time.Minute, ReadTimeout: 10 * time.Second, CheckInterval: time.Minute, RetryInterval: time.Second,
		Now: func() time.Time { return time.Now().Add(time.Second) }}
	if worked, err := poller.RunOnce(t.Context()); err != nil || !worked {
		t.Fatalf("source-outage poll: worked=%t err=%v", worked, err)
	}
	stop := env.s.startEnvironmentGitDriftReporting(t.Context(), func(string) string { return "true" })
	t.Cleanup(stop)
	first := waitGitOpsReport(t, store, source, "")
	stop()
	if first.Status != "drifted" || string(first.Steps) != "[]" || first.RevisionID != source.ApprovedRevisionID {
		t.Fatalf("first PostgreSQL report: %+v", first)
	}
	var plan environmentsync.Plan
	if err := json.Unmarshal(first.Plan, &plan); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"workload/api#variables/MODE": false, "workload/api#routes": false,
		"workload/api#policies": false, "workload/worker#queue_bindings/orders": false}
	for _, change := range plan.Changes {
		key := change.Resource + "#" + change.Path
		if _, ok := want[key]; ok && change.Action != "keep" && change.Action != "retain_unmanaged" {
			want[key] = true
		}
	}
	for key, found := range want {
		if !found {
			t.Fatalf("full-graph report omitted %s: %+v", key, plan.Changes)
		}
	}
	if err := store.UpsertAppEnvInScope(t.Context(), env.acct.ID, apiApp.ID, "production", "MODE", "second-console-edit"); err != nil {
		t.Fatal(err)
	}
	fresh := newServer(state.NewPgStore(env.pool), env.s.log, "gregale.dev", noopNotifier{})
	stopFresh := fresh.startEnvironmentGitDriftReporting(t.Context(), func(string) string { return "true" })
	t.Cleanup(stopFresh)
	second := waitGitOpsReport(t, store, source, first.ID)
	stopFresh()
	if second.Status != "drifted" || second.RevisionID != first.RevisionID || string(second.Steps) != "[]" {
		t.Fatalf("restart lost report or approved revision: %+v", second)
	}
	response := gitOpsHandlerRequest(t, fresh, env.acct, http.MethodGet, "", nil, fresh.getEnvironmentGitOps)
	var status api.EnvironmentGitOpsStatusResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &status) != nil || len(status.Runs) < 2 ||
		status.Source.SourceErrorCode != "environment_git_source_unavailable" || status.Source.AppliedRevisionID != "" {
		t.Fatalf("report/source outage status: %d %s", response.Code, response.Body.String())
	}
	values, err := store.ListAppEnvInScope(t.Context(), env.acct.ID, apiApp.ID, "production")
	if err != nil || len(values) != 1 || values[0].Value != "second-console-edit" {
		t.Fatalf("report restored console value: %+v %v", values, err)
	}
	actual, err := store.QueueBindingByID(t.Context(), env.acct.ID, workerApp.ID, binding.Binding.ID)
	if err != nil || actual.QueueName != "orders" || actual.MaxConcurrency != 1 {
		t.Fatalf("report changed queue contract: %+v %v", actual, err)
	}
}
