package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGitOpsRollbackHTTPRequiresReadScopeAndScopesRetainedRelease(t *testing.T) {
	server, store, account, project, _ := apidserver.NewProjectLifecycleFixture(t)
	if _, err := store.UpdateProjectBinding(t.Context(), account.ID, project.ID, "example/shop", "main", 42); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	spec := state.EnvironmentGitSourceSpec{Provider: "github", Repository: "example/shop", RepositoryID: 123, InstallationID: 42, Ref: "refs/heads/main", ManifestPath: "environments/production.yaml", Mode: "report", ApprovalPolicy: "manual"}
	if _, err := store.CreateEnvironmentGitSource(t.Context(), account.ID, project.ID, "production", spec); err != nil {
		t.Fatal(err)
	}
	spec.ManifestPath = "environments/staging.yaml"
	from, err := store.CreateEnvironmentGitSource(t.Context(), account.ID, project.ID, "staging", spec)
	if err != nil {
		t.Fatal(err)
	}
	definition := api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: "shop", Environment: "staging", Workloads: map[string]api.EnvironmentWorkload{"api": {App: "shop-api", Source: &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example/api@sha256:" + strings.Repeat("a", 64)}}}}
	desired, err := environmentsync.Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ApproveEnvironmentDesiredRevision(t.Context(), state.ApproveEnvironmentRevision{AccountID: account.ID, SourceID: from.ID, ExpectedGeneration: from.Generation, CommitSHA: strings.Repeat("b", 40), Desired: desired, ApprovedBy: "owner"}); err != nil {
		t.Fatal(err)
	}
	definition.Environment = "production"
	raw, _ := json.Marshal(definition)
	sha := strings.Repeat("c", 40)
	server.GitOpsPreviewArchiveForTest(t, string(raw), sha)
	key := func(accountID string, scopes []string) string {
		t.Helper()
		token, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateAPIKey(t.Context(), accountID, hash, "promotion-preview", scopes); err != nil {
			t.Fatal(err)
		}
		return token
	}
	reader := key(account.ID, []string{api.ScopeProjectEnvironmentRead})
	other, err := store.CreateAccount(t.Context(), "foreign-promotion@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	request := func(token, hash string) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(api.PreviewEnvironmentGitRollbackRequest{ReleaseSetID: "2ba0aa40-2362-4c0e-aeb1-6381666bb514", TargetCommitSHA: sha, ExpectedPlanHash: hash})
		r := httptest.NewRequest(http.MethodPost, "/v1/projects/shop/environments/production/gitops/rollback-plan", strings.NewReader(string(body)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "read-only-repeat")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		name, token string
		status      int
	}{{"anonymous", "", 401}, {"wrong scope", key(account.ID, []string{api.ScopeUsageRead}), 403}, {"foreign account", key(other.ID, []string{api.ScopeProjectEnvironmentRead}), 404}, {"reader with missing release", reader, 404}} {
		t.Run(tc.name, func(t *testing.T) {
			w := request(tc.token, "")
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
	first := request(reader, "")
	if !strings.Contains(first.Body.String(), "environment_gitops_rollback_release_not_found") {
		t.Fatalf("reader did not reach scoped rollback storage: %s", first.Body.String())
	}
	invalid := request(reader, "invalid-review-hash")
	if invalid.Code != 400 {
		t.Fatalf("invalid review hash accepted: %d %s", invalid.Code, invalid.Body.String())
	}
}
