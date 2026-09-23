package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func newProjectLifecycleFixture(t *testing.T) (*server, *state.MemStore, state.Account, state.Project, state.App) {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "project-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "shop", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "shop-api", WorkloadName: "api", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
	return srv, store, acct, project, app
}

func projectRequest(method, path, slug string, body []byte) (*http.Request, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.SetPathValue("slug", slug)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, httptest.NewRecorder()
}

func TestProjectLifecycleInspectUpdateAndDelete(t *testing.T) {
	srv, store, acct, project, app := newProjectLifecycleFixture(t)

	req, rec := projectRequest(http.MethodGet, "/v1/projects/shop", "shop", nil)
	srv.getProject(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got api.ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Slug != project.Slug || len(got.Workloads) != 1 || got.Workloads[0].Slug != app.Slug {
		t.Fatalf("project response = %+v", got)
	}

	req, rec = projectRequest(http.MethodPatch, "/v1/projects/shop", "shop", []byte(`{"production_branch":"release"}`))
	srv.updateProject(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", rec.Code, rec.Body.String())
	}
	updated, err := store.ProjectBySlug(context.Background(), acct.ID, "shop")
	if err != nil || updated.ProductionBranch != "release" {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}

	req, rec = projectRequest(http.MethodGet, "/v1/projects/shop/delete-preview", "shop", nil)
	srv.previewDeleteProject(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", rec.Code, rec.Body.String())
	}
	var preview api.ProjectDeletePreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil || len(preview.Workloads) != 1 {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}

	req, rec = projectRequest(http.MethodDelete, "/v1/projects/shop", "shop", nil)
	srv.deleteProject(rec, req, acct)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", rec.Code, rec.Body.String())
	}
	remaining, err := store.AppBySlug(context.Background(), app.Slug)
	if err != nil || remaining.ProjectID != "" {
		t.Fatalf("live workload was not detached: %+v err=%v", remaining, err)
	}
}

func TestProjectLifecycleHidesOtherAccounts(t *testing.T) {
	srv, store, _, _, _ := newProjectLifecycleFixture(t)
	other, err := store.CreateAccount(context.Background(), "other-project@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	req, rec := projectRequest(http.MethodGet, "/v1/projects/shop", "shop", nil)
	srv.getProject(rec, req, other)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestProjectEnvironmentRegistryLifecycleAndOwnership(t *testing.T) {
	srv, store, acct, project, _ := newProjectLifecycleFixture(t)
	ctx := context.Background()

	environments, err := store.ListProjectEnvironments(ctx, acct.ID, project.ID)
	if err != nil || len(environments) != 1 || environments[0].Slug != "production" || !environments[0].Protected {
		t.Fatalf("production environment = %+v err=%v", environments, err)
	}

	req, rec := projectRequest(http.MethodPost, "/v1/projects/shop/environments", "shop", []byte(`{"slug":"staging","protected":true}`))
	req.SetPathValue("environment", "staging")
	srv.createProjectEnvironment(rec, req, acct)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}

	req, rec = projectRequest(http.MethodPatch, "/v1/projects/shop/environments/staging", "shop", []byte(`{"protected":false}`))
	req.SetPathValue("environment", "staging")
	srv.updateProjectEnvironment(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", rec.Code, rec.Body.String())
	}
	var updated api.ProjectEnvironmentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil || updated.Slug != "staging" || updated.Protected {
		t.Fatalf("updated environment=%+v err=%v", updated, err)
	}

	req, rec = projectRequest(http.MethodGet, "/v1/projects/shop/environments", "shop", nil)
	srv.listProjectEnvironments(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	var listed []api.ProjectEnvironmentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil || len(listed) != 2 {
		t.Fatalf("listed environments=%+v err=%v", listed, err)
	}

	req, rec = projectRequest(http.MethodDelete, "/v1/projects/shop/environments/staging", "shop", nil)
	req.SetPathValue("environment", "staging")
	srv.deleteProjectEnvironment(rec, req, acct)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, "staging"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted staging lookup err=%v, want ErrNotFound", err)
	}

	req, rec = projectRequest(http.MethodDelete, "/v1/projects/shop/environments/production", "shop", nil)
	req.SetPathValue("environment", "production")
	srv.deleteProjectEnvironment(rec, req, acct)
	if rec.Code != http.StatusConflict {
		t.Fatalf("production delete status=%d body=%s", rec.Code, rec.Body.String())
	}

	other, err := store.CreateAccount(ctx, "other-environment-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	req, rec = projectRequest(http.MethodGet, "/v1/projects/shop/environments", "shop", nil)
	srv.listProjectEnvironments(rec, req, other)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account list status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestProjectEnvironmentCloneCopiesScopedStateAtomically(t *testing.T) {
	srv, store, acct, project, app := newProjectLifecycleFixture(t)
	ctx := context.Background()
	createProjectEnvironmentConfigFixture(t, store, acct.ID, project.ID, "production", `{"region":"us"}`)
	if err := store.UpsertAppEnvInScope(ctx, acct.ID, app.ID, "production", "MODE", "production"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppSecretWithKidAndValueHashInScope(ctx, acct.ID, app.ID, "production", "STRIPE_KEY", "age1-source", "1111111111111111", []byte("sealed-source")); err != nil {
		t.Fatal(err)
	}

	req, rec := projectRequest(http.MethodPost, "/v1/projects/shop/environments", "shop", []byte(`{"slug":"staging","from_environment":"production"}`))
	srv.createProjectEnvironment(rec, req, acct)
	if rec.Code != http.StatusCreated {
		t.Fatalf("clone status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response api.ProjectEnvironmentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ClonedFrom != "production" || response.Clone == nil || !response.Clone.ConfigurationCopied || response.Clone.VariablesCopied != 1 || response.Clone.SecretsCopied != 1 {
		t.Fatalf("clone response=%+v", response)
	}
	if strings.Contains(rec.Body.String(), "sealed-source") || strings.Contains(rec.Body.String(), "age1-source") {
		t.Fatalf("clone response leaked secret material: %s", rec.Body.String())
	}
	config, err := store.ProjectEnvironmentConfigLatest(ctx, acct.ID, project.ID, "staging")
	if err != nil || config.Version != 1 || string(config.Values) != `{"region":"us"}` {
		t.Fatalf("cloned config=%+v err=%v", config, err)
	}
	envs, err := store.ListAppEnvInScope(ctx, acct.ID, app.ID, "staging")
	if err != nil || len(envs) != 1 || envs[0].Value != "production" {
		t.Fatalf("cloned env=%+v err=%v", envs, err)
	}
	secrets, err := store.ListAppSecretsInScope(ctx, acct.ID, app.ID, "staging")
	if err != nil || len(secrets) != 1 || string(secrets[0].Ciphertext) != "sealed-source" || secrets[0].ValueHash != "1111111111111111" {
		t.Fatalf("cloned secrets=%+v err=%v", secrets, err)
	}
}

// adr: 211
func TestProjectEnvironmentCloneFailsClosedWhenManagedResourceIsolationUnavailable(t *testing.T) {
	srv, store, acct, project, app := newProjectLifecycleFixture(t)
	ctx := context.Background()
	if err := store.PutManagedPostgresSecret(ctx, state.AppSecret{
		AccountID: acct.ID, AppID: app.ID, Scope: "production", Key: "DATABASE_URL",
		Ciphertext: []byte("managed-sealed"), ManagedPostgresBindingID: "binding-production",
		ManagedCredentialRef: "credential-production", ManagedCredentialGeneration: 3,
	}); err != nil {
		t.Fatal(err)
	}
	req, rec := projectRequest(http.MethodPost, "/v1/projects/shop/environments", "shop", []byte(`{"slug":"staging","from_environment":"production"}`))
	srv.createProjectEnvironment(rec, req, acct)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("clone status=%d body=%s, want managed-resource isolation unavailable", rec.Code, rec.Body.String())
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, "staging"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("blocked clone created target: %v", err)
	}
}

func TestProjectEnvironmentConfigVersionAndDiff(t *testing.T) {
	srv, store, acct, project, _ := newProjectLifecycleFixture(t)
	ctx := context.Background()
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{
		AccountID: acct.ID, ProjectID: project.ID, Slug: "staging",
	}); err != nil {
		t.Fatal(err)
	}

	req, rec := projectRequest(http.MethodGet, "/v1/projects/shop/environments/staging/config", "shop", nil)
	req.SetPathValue("environment", "staging")
	srv.getProjectEnvironmentConfig(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty config status=%d body=%s", rec.Code, rec.Body.String())
	}
	var empty api.ProjectEnvironmentConfigResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &empty); err != nil {
		t.Fatal(err)
	}
	if empty.Version != 0 || string(empty.Values) != `{}` || empty.ConfigHash != api.EmptyProjectEnvironmentConfigHash() {
		t.Fatalf("empty config=%+v", empty)
	}

	req, rec = projectRequest(http.MethodPut, "/v1/projects/shop/environments/staging/config", "shop", []byte(`{"values":{"region":"eu","replicas":2}}`))
	req.SetPathValue("environment", "staging")
	srv.updateProjectEnvironmentConfig(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("update config status=%d body=%s", rec.Code, rec.Body.String())
	}
	var updated api.ProjectEnvironmentConfigResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Version != 1 || updated.ConfigHash == "" || string(updated.Values) != `{"region":"eu","replicas":2}` {
		t.Fatalf("updated config=%+v", updated)
	}

	req, rec = projectRequest(http.MethodGet, "/v1/projects/shop/environments/staging/config/diff?from=production", "shop", nil)
	req.SetPathValue("environment", "staging")
	srv.diffProjectEnvironmentConfig(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("diff status=%d body=%s", rec.Code, rec.Body.String())
	}
	var diff api.ProjectEnvironmentConfigDiffResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &diff); err != nil {
		t.Fatal(err)
	}
	if diff.FromEnvironment != "production" || diff.ToEnvironment != "staging" || len(diff.Changes) != 2 {
		t.Fatalf("diff=%+v", diff)
	}
	if diff.Changes[0].Key != "region" || diff.Changes[0].Kind != "added" {
		t.Fatalf("diff ordering/content=%+v", diff.Changes)
	}

	req, rec = projectRequest(http.MethodPut, "/v1/projects/shop/environments/staging/config", "shop", []byte(`{"values":{"api_token":"nope"}}`))
	req.SetPathValue("environment", "staging")
	srv.updateProjectEnvironmentConfig(rec, req, acct)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("secret-shaped config status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func projectDashboardPost(t *testing.T, srv *server, acct state.Account, path, slug string, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	token, err := middleware.IssueForAuthenticated(srv.sessions, dashboardProjectManageAction, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	values.Set(middleware.FormFieldName, token)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: token})
	req.SetPathValue("slug", slug)
	req = req.WithContext(WithAccount(req.Context(), acct))
	rec := httptest.NewRecorder()
	if strings.HasSuffix(path, "/update") {
		srv.dashboardUpdateProject(rec, req)
	} else {
		srv.dashboardDeleteProject(rec, req)
	}
	return rec
}

func TestDashboardProjectLifecycleInspectUpdateAndConfirmedDelete(t *testing.T) {
	srv, store, acct, _, app := newProjectLifecycleFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/dashboard/projects/shop", nil)
	req = req.WithContext(WithAccount(req.Context(), acct))
	rec := httptest.NewRecorder()
	srv.renderProjectDetail(rec, req, srv.log, acct, "shop")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "shop-api") || !strings.Contains(rec.Body.String(), "Delete project") {
		t.Fatalf("detail status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = projectDashboardPost(t, srv, acct, "/dashboard/projects/shop/update", "shop", url.Values{
		"repo_full_name": {""}, "production_branch": {"release"},
	})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/dashboard/projects/shop?project=updated" {
		t.Fatalf("update status=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}
	updated, err := store.ProjectBySlug(context.Background(), acct.ID, "shop")
	if err != nil || updated.ProductionBranch != "release" {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}

	rec = projectDashboardPost(t, srv, acct, "/dashboard/projects/shop/delete", "shop", url.Values{"confirm_slug": {"wrong"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/dashboard/projects/shop?project=confirm" {
		t.Fatalf("unconfirmed delete status=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}
	if _, err := store.ProjectBySlug(context.Background(), acct.ID, "shop"); err != nil {
		t.Fatalf("unconfirmed delete removed project: %v", err)
	}

	rec = projectDashboardPost(t, srv, acct, "/dashboard/projects/shop/delete", "shop", url.Values{"confirm_slug": {"shop"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/dashboard/projects?project=deleted" {
		t.Fatalf("delete status=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}
	remaining, err := store.AppBySlug(context.Background(), app.Slug)
	if err != nil || remaining.ProjectID != "" {
		t.Fatalf("dashboard delete did not preserve/detach app: %+v err=%v", remaining, err)
	}
}
