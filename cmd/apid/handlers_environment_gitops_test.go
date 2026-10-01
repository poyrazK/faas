package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

type environmentGitOpsClient struct {
	stubGithubdClient
	repositories []Repo
	archive      []byte
	resolvedSHA  string
	truncated    bool
	fetches      int
}

func (c *environmentGitOpsClient) ListInstallableRepos(context.Context, string, int64) ([]Repo, error) {
	return c.repositories, nil
}
func (c *environmentGitOpsClient) StreamSourceRef(_ context.Context, _ string, _ int64, _ string, _ string, _ int64) (*StreamSourceRefResult, error) {
	c.fetches++
	return &StreamSourceRefResult{Body: io.NopCloser(bytes.NewReader(c.archive)), Stats: &StreamSourceRefStats{ResolvedCommitSHA: c.resolvedSHA, Truncated: c.truncated}}, nil
}

func environmentGitOpsArchive(t *testing.T) []byte {
	t.Helper()
	return environmentGitOpsArchiveDefinition(t, "api_version: gregale.dev/environment/v1\nproject: shop\nenvironment: production\nworkloads:\n  api:\n    app: shop-api\n    variables:\n      MODE: production\n")
}

func environmentGitOpsArchiveDefinition(t *testing.T, definition string) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	archive := tar.NewWriter(gz)
	if err := archive.WriteHeader(&tar.Header{Name: "shop-commit/environments/production.yaml", Typeflag: tar.TypeReg, Size: int64(len(definition))}); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write([]byte(definition)); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func gitOpsHandlerRequest(t *testing.T, srv *server, account state.Account, method, suffix string, body any, handler func(http.ResponseWriter, *http.Request, state.Account)) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, rec := projectRequest(method, "/v1/projects/shop/environments/production/gitops/"+suffix, "shop", raw)
	req.SetPathValue("environment", "production")
	handler(rec, req, account)
	return rec
}

func TestEnvironmentGitOpsHandlersApproveReviewedGitBytesAndAdopt(t *testing.T) {
	srv, store, account, project, app := newProjectLifecycleFixture(t)
	if _, err := store.UpdateProjectBinding(t.Context(), account.ID, project.ID, "example/shop", "main", 42); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, "production", "MODE", "console"); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 40)
	client := &environmentGitOpsClient{repositories: []Repo{{ID: 123, FullName: "example/shop"}}, archive: environmentGitOpsArchive(t), resolvedSHA: sha}
	srv.githubd = client
	rec := gitOpsHandlerRequest(t, srv, account, http.MethodPost, "source", map[string]any{"manifest_path": "environments/production.yaml", "mode": "enforce"}, srv.createEnvironmentGitSource)
	if rec.Code != http.StatusCreated {
		t.Fatalf("source: %d %s", rec.Code, rec.Body.String())
	}
	var source state.EnvironmentGitSource
	if err := json.Unmarshal(rec.Body.Bytes(), &source); err != nil || source.Spec.RepositoryID != 123 || source.Spec.InstallationID != 42 {
		t.Fatalf("identity: %+v %v", source, err)
	}
	rec = gitOpsHandlerRequest(t, srv, account, http.MethodPost, "preview", map[string]string{"commit_sha": sha}, srv.previewEnvironmentGitRevision)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", rec.Code, rec.Body.String())
	}
	var preview struct {
		DefinitionDigest string `json:"definition_digest"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	rec = gitOpsHandlerRequest(t, srv, account, http.MethodPost, "approve", map[string]any{"commit_sha": sha, "definition_digest": strings.Repeat("0", 64), "expected_generation": 0}, srv.approveEnvironmentGitRevision)
	if rec.Code != http.StatusConflict {
		t.Fatalf("unreviewed digest: %d %s", rec.Code, rec.Body.String())
	}
	rec = gitOpsHandlerRequest(t, srv, account, http.MethodPost, "approve", map[string]any{"commit_sha": sha, "definition_digest": preview.DefinitionDigest, "expected_generation": 0}, srv.approveEnvironmentGitRevision)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("approval: %d %s", rec.Code, rec.Body.String())
	}
	rec = gitOpsHandlerRequest(t, srv, account, http.MethodGet, "adoption-preview", nil, srv.previewEnvironmentGitOpsAdoption)
	if rec.Code != http.StatusOK {
		t.Fatalf("adoption preview: %d %s", rec.Code, rec.Body.String())
	}
	var plan environmentsync.Plan
	if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	rec = gitOpsHandlerRequest(t, srv, account, http.MethodPost, "adopt", map[string]string{"plan_hash": plan.Hash}, srv.adoptEnvironmentGitOps)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("adopt: %d %s", rec.Code, rec.Body.String())
	}
	worker := environmentgitops.Worker{Store: store, Backend: environmentgitops.IntentBackend{Store: store}, LeaseDuration: time.Minute, CheckInterval: time.Minute, RetryInterval: time.Second}
	if _, err := worker.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	variables, err := store.ListAppEnvInScope(t.Context(), account.ID, app.ID, "production")
	if err != nil || len(variables) != 1 || variables[0].Value != "production" {
		t.Fatalf("intent: %+v %v", variables, err)
	}
	rec = gitOpsHandlerRequest(t, srv, account, http.MethodGet, "status", nil, srv.getEnvironmentGitOps)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"converged"`) {
		t.Fatalf("status: %d %s", rec.Code, rec.Body.String())
	}
	other, err := store.CreateAccount(t.Context(), "other-gitops@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	rec = gitOpsHandlerRequest(t, srv, other, http.MethodGet, "status", nil, srv.getEnvironmentGitOps)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account status: %d", rec.Code)
	}
}

func TestEnvironmentGitOpsHandlersRejectForgedIdentityAndSource(t *testing.T) {
	srv, store, account, project, _ := newProjectLifecycleFixture(t)
	if _, err := store.UpdateProjectBinding(t.Context(), account.ID, project.ID, "example/shop", "main", 42); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 40)
	client := &environmentGitOpsClient{repositories: []Repo{{ID: 123, FullName: "example/shop"}}, archive: environmentGitOpsArchive(t), resolvedSHA: sha}
	srv.githubd = client
	rec := gitOpsHandlerRequest(t, srv, account, http.MethodPost, "source", map[string]any{"manifest_path": "environments/production.yaml", "repository_id": 999}, srv.createEnvironmentGitSource)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("caller-supplied identity: %d", rec.Code)
	}
	rec = gitOpsHandlerRequest(t, srv, account, http.MethodPost, "source", map[string]string{"manifest_path": "environments/production.yaml"}, srv.createEnvironmentGitSource)
	if rec.Code != http.StatusCreated {
		t.Fatalf("source: %d %s", rec.Code, rec.Body.String())
	}
	for _, failure := range []string{"renamed repository", "wrong commit", "truncated stream", "mutable ref"} {
		t.Run(failure, func(t *testing.T) {
			client.repositories = []Repo{{ID: 123, FullName: "example/shop"}}
			client.resolvedSHA = sha
			client.truncated = false
			commit := sha
			switch failure {
			case "renamed repository":
				client.repositories[0].ID = 999
			case "wrong commit":
				client.resolvedSHA = strings.Repeat("b", 40)
			case "truncated stream":
				client.truncated = true
			case "mutable ref":
				commit = "main"
			}
			rec := gitOpsHandlerRequest(t, srv, account, http.MethodPost, "preview", map[string]string{"commit_sha": commit}, srv.previewEnvironmentGitRevision)
			if rec.Code == http.StatusOK {
				t.Fatalf("unverified source accepted: %s", failure)
			}
		})
	}
}

func TestEnvironmentGitOpsHTTPScopedConfigWorkflow(t *testing.T) {
	srv, store, account, project, app := newProjectLifecycleFixture(t)
	if _, err := store.UpdateProjectBinding(t.Context(), account.ID, project.ID, "example/shop", "main", 42); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, "production", "MODE", "console"); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 40)
	srv.githubd = &environmentGitOpsClient{repositories: []Repo{{ID: 123, FullName: "example/shop"}}, archive: environmentGitOpsArchive(t), resolvedSHA: sha}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(t.Context(), account.ID, hash, "gitops test", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(srv.handler())
	defer httpServer.Close()
	client := api.NewClient(httpServer.URL, key)
	if _, err := client.CreateEnvironmentGitSource(t.Context(), "shop", "production", api.CreateEnvironmentGitSourceRequest{ManifestPath: "environments/production.yaml", Mode: "enforce"}); err != nil {
		t.Fatal(err)
	}
	review, err := client.PreviewEnvironmentGitRevision(t.Context(), "shop", "production", api.PreviewEnvironmentGitRevisionRequest{CommitSHA: sha})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ApproveEnvironmentGitRevision(t.Context(), "shop", "production", api.ApproveEnvironmentGitRevisionRequest{CommitSHA: review.CommitSHA, DefinitionDigest: review.DefinitionDigest, ExpectedGeneration: review.Generation}); err != nil {
		t.Fatal(err)
	}
	plan, err := client.PreviewEnvironmentGitOpsAdoption(t.Context(), "shop", "production")
	if err != nil || !plan.CanApply() {
		t.Fatalf("adoption: %+v %v", plan, err)
	}
	if _, err := client.AdoptEnvironmentGitOps(t.Context(), "shop", "production", api.AdoptEnvironmentGitOpsRequest{PlanHash: plan.Hash}); err != nil {
		t.Fatal(err)
	}
	worker := environmentgitops.Worker{Store: store, Backend: environmentgitops.IntentBackend{Store: store}, LeaseDuration: time.Minute, CheckInterval: time.Minute, RetryInterval: time.Second}
	if _, err := worker.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	status, err := client.GetEnvironmentGitOps(t.Context(), "shop", "production")
	if err != nil || len(status.Runs) != 1 || status.Runs[0].Status != "converged" || status.Source.AppliedRevisionID == "" {
		t.Fatalf("convergence: %+v %v", status, err)
	}
	// Existing console/API mutations use the same ownership contract.
	invocation, err := store.EnqueueInvocation(t.Context(), state.Invocation{AppID: app.ID, AccountID: account.ID, Source: state.InvocationAsyncInvoke,
		Method: "POST", Path: "/work", Payload: json.RawMessage(`{}`), DueAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	request, rec := projectRequest(http.MethodDelete, "/v1/apps/shop-api", "shop-api", nil)
	srv.deleteApp(rec, request, account)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "environment_field_git_managed") {
		t.Fatalf("managed app delete: %d %s", rec.Code, rec.Body.String())
	}
	unchanged, err := store.InvocationByID(t.Context(), invocation.ID)
	if err != nil || unchanged.State != state.InvocationPending {
		t.Fatalf("rejected deletion changed pending work: %+v %v", unchanged, err)
	}
	expiry := time.Now().UTC().Add(time.Hour)
	if _, err := client.CreateEnvironmentGitOpsOverride(t.Context(), "shop", "production", api.EnvironmentGitOpsOverrideRequest{Resource: "workload/api", Path: "variables/MODE", Reason: "incident mitigation", ExpiresAt: expiry}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, "production", "MODE", "temporary"); err != nil {
		t.Fatal(err)
	}
	if err := client.RemoveEnvironmentGitOpsOverride(t.Context(), "shop", "production", api.RemoveEnvironmentGitOpsOverrideRequest{Resource: "workload/api", Path: "variables/MODE"}); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	variables, err := store.ListAppEnvInScope(t.Context(), account.ID, app.ID, "production")
	if err != nil || len(variables) != 1 || variables[0].Value != "production" {
		t.Fatalf("override restoration: %+v %v", variables, err)
	}
	status, err = client.GetEnvironmentGitOps(t.Context(), "shop", "production")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.UpdateEnvironmentGitSource(t.Context(), "shop", "production", api.EnvironmentGitSourceUpdate{ExpectedGeneration: status.Source.Generation, Mode: "report"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, "production", "MODE", "manual"); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	status, err = client.GetEnvironmentGitOps(t.Context(), "shop", "production")
	if err != nil || status.Runs[0].Status != "drifted" {
		t.Fatalf("report drift: %+v %v", status, err)
	}

	readKey, readHash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(t.Context(), account.ID, readHash, "read only", []string{api.ScopeProjectEnvironmentRead}); err != nil {
		t.Fatal(err)
	}
	readClient := api.NewClient(httpServer.URL, readKey)
	if _, err := readClient.GetEnvironmentGitOps(t.Context(), "shop", "production"); err != nil {
		t.Fatal(err)
	}
	if _, err := readClient.UpdateEnvironmentGitSource(t.Context(), "shop", "production", api.EnvironmentGitSourceUpdate{ExpectedGeneration: status.Source.Generation, Mode: "enforce"}); err == nil {
		t.Fatal("read-only key mutated source controls")
	}
	if _, err := api.NewClient(httpServer.URL, "").GetEnvironmentGitOps(t.Context(), "shop", "production"); err == nil {
		t.Fatal("unauthenticated source read succeeded")
	}
}
