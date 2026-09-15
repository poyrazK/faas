package main

import (
	"bytes"
	"context"
	"encoding/json"
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
