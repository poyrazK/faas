package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestQueueEnvironmentHTTPGitOwnershipProblem(t *testing.T) {
	for _, mode := range []string{"report", "enforce"} {
		t.Run(mode, func(t *testing.T) {
			e := apidserver.Setup(t, api.PlanPro)
			project, err := (*e.Store()).CreateProject(t.Context(), state.Project{AccountID: (*e.Acct()).ID, Slug: "queue-project", RepoFullName: "example/queue", ProductionBranch: "main", InstallID: 42})
			if err != nil {
				t.Fatal(err)
			}
			app, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, ProjectID: project.ID, Slug: "queue-worker", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
			if err != nil {
				t.Fatal(err)
			}
			binding := createScopedHTTPQueue(t, e, "production", "orders")
			source, err := (*e.Store()).CreateEnvironmentGitSource(t.Context(), (*e.Acct()).ID, project.ID, "production", state.EnvironmentGitSourceSpec{RepositoryID: 123, InstallationID: 42, Repository: "example/queue", Ref: "refs/heads/main", ManifestPath: "production.yaml", Mode: mode, ApprovalPolicy: "manual"})
			if err != nil {
				t.Fatal(err)
			}
			desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: project.Slug, Environment: "production", Workloads: map[string]api.EnvironmentWorkload{
				"worker": {App: app.Slug, QueueBindings: map[string]api.EnvironmentQueueBinding{"orders": {QueueName: "orders", Mode: "push", WorkloadClass: "worker"}},
					QueueSmoke: map[string]api.EnvironmentQueueSmoke{"orders": {Payload: json.RawMessage(`{"id":"qualification"}`)}}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			source, _, err = (*e.Store()).ApproveEnvironmentDesiredRevision(t.Context(), state.ApproveEnvironmentRevision{AccountID: (*e.Acct()).ID, SourceID: source.ID, ExpectedGeneration: source.Generation, Desired: desired, CommitSHA: strings.Repeat("a", 40), ApprovedBy: "owner"})
			if err != nil {
				t.Fatal(err)
			}
			preview, err := (*e.Store()).PreviewEnvironmentGitOpsAdoption(t.Context(), (*e.Acct()).ID, source.ID)
			if err != nil || !preview.CanApply() {
				t.Fatalf("adoption: %+v %v", preview, err)
			}
			if err := (*e.Store()).AdoptEnvironmentGitOps(t.Context(), (*e.Acct()).ID, source.ID, preview.Hash); err != nil {
				t.Fatal(err)
			}
			cap := 4
			rec := e.Do(t, http.MethodPatch, "/v1/apps/queue-worker/queue-bindings/"+binding.ID, api.UpdateQueueBindingRequest{MaxConcurrency: &cap}, nil)
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
			rec = e.Do(t, http.MethodDelete, "/v1/apps/queue-worker/queue-bindings/"+binding.ID, nil, nil)
			if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || rec.Code != http.StatusConflict || problem.Code != "environment_field_git_managed" {
				t.Fatalf("owned retirement: %d %s %v", rec.Code, rec.Body, err)
			}
		})
	}
}
