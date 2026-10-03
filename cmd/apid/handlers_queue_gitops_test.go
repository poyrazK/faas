package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestQueueEnvironmentHTTPGitOwnershipProblem(t *testing.T) {
	for _, mode := range []string{"report", "enforce"} {
		t.Run(mode, func(t *testing.T) {
			e := setup(t, api.PlanPro)
			project, err := e.store.CreateProject(t.Context(), state.Project{AccountID: e.acct.ID, Slug: "queue-project", RepoFullName: "example/queue", ProductionBranch: "main", InstallID: 42})
			if err != nil {
				t.Fatal(err)
			}
			app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "queue-worker", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
			if err != nil {
				t.Fatal(err)
			}
			binding := createScopedHTTPQueue(t, e, "production", "orders")
			source, err := e.store.CreateEnvironmentGitSource(t.Context(), e.acct.ID, project.ID, "production", state.EnvironmentGitSourceSpec{RepositoryID: 123, InstallationID: 42, Repository: "example/queue", Ref: "refs/heads/main", ManifestPath: "production.yaml", Mode: mode, ApprovalPolicy: "manual"})
			if err != nil {
				t.Fatal(err)
			}
			desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: project.Slug, Environment: "production", Workloads: map[string]api.EnvironmentWorkload{
				"worker": {App: app.Slug, QueueBindings: map[string]api.EnvironmentQueueBinding{"orders": {QueueName: "orders", Mode: "push", WorkloadClass: "worker"}}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			source, _, err = e.store.ApproveEnvironmentDesiredRevision(t.Context(), state.ApproveEnvironmentRevision{AccountID: e.acct.ID, SourceID: source.ID, ExpectedGeneration: source.Generation, Desired: desired, CommitSHA: strings.Repeat("a", 40), ApprovedBy: "owner"})
			if err != nil {
				t.Fatal(err)
			}
			preview, err := e.store.PreviewEnvironmentGitOpsAdoption(t.Context(), e.acct.ID, source.ID)
			if err != nil || !preview.CanApply() {
				t.Fatalf("adoption: %+v %v", preview, err)
			}
			if err := e.store.AdoptEnvironmentGitOps(t.Context(), e.acct.ID, source.ID, preview.Hash); err != nil {
				t.Fatal(err)
			}
			cap := 4
			rec := e.do(t, http.MethodPatch, "/v1/apps/queue-worker/queue-bindings/"+binding.ID, api.UpdateQueueBindingRequest{MaxConcurrency: &cap}, nil)
			if mode == "report" {
				if rec.Code != http.StatusOK {
					t.Fatalf("report edit: %d %s", rec.Code, rec.Body)
				}
				return
			}
			var problem api.Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || rec.Code != http.StatusConflict || problem.Code != "environment_field_git_managed" {
				t.Fatalf("owned edit: %d %s %v", rec.Code, rec.Body, err)
			}
			rec = e.do(t, http.MethodDelete, "/v1/apps/queue-worker/queue-bindings/"+binding.ID, nil, nil)
			if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || rec.Code != http.StatusConflict || problem.Code != "environment_field_git_managed" {
				t.Fatalf("owned retirement: %d %s %v", rec.Code, rec.Body, err)
			}
		})
	}
}
