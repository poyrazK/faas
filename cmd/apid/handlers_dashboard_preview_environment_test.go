package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDashboardPRPreviewEnvironment_CurrentHeadAndScope(t *testing.T) {
	h, session, store, _ := newAuthedDashboardServerFull(t)
	ctx := t.Context()
	account, err := store.AccountByEmail(ctx, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	root := seedPreviewForDashboard(t, store, account.ID, "pr-42-api", "api", 42)
	worker := seedPreviewForDashboard(t, store, account.ID, "pr-42-worker", "worker", 42)
	sha := strings.Repeat("a", 40)
	set := state.PRPreviewSet{InstallationID: 77, RepoFullName: "octo/api", PRNumber: 42,
		CommitSHA: sha, RootAppID: root.ID, MemberAppIDs: []string{root.ID, worker.ID}}
	if err := store.PutPRPreviewSet(ctx, set); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, row := range []struct {
		appID  string
		status state.DeploymentStatus
	}{
		{root.ID, state.DeployLive},
		{worker.ID, state.DeployBuilding},
	} {
		if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: row.appID,
			Kind: state.DeploymentKindPreview, ImageDigest: "sha256:" + strings.Repeat("b", 64),
			CommitSHA: sha, Status: row.status, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	get := func(slug string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/dashboard/apps/"+slug, nil)
		req.AddCookie(session)
		h.ServeHTTP(rec, req)
		return rec
	}
	assertPage := func(want ...string) {
		t.Helper()
		rec := get(root.Slug)
		if rec.Code != http.StatusOK {
			t.Fatalf("root detail status=%d: %s", rec.Code, rec.Body.String())
		}
		for _, fragment := range want {
			if !strings.Contains(rec.Body.String(), fragment) {
				t.Errorf("root detail missing %q", fragment)
			}
		}
	}
	assertPage(`id="pr-preview-environment"`, `https://github.com/octo/api/pull/42`,
		`1/2 current-head deployments live`, `pr-42-worker`, `building`)
	if sibling := get(worker.Slug); sibling.Code != http.StatusOK || strings.Contains(sibling.Body.String(), `id="pr-preview-environment"`) {
		t.Fatalf("sibling detail exposed root environment: status=%d", sibling.Code)
	}
	if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: worker.ID,
		Kind: state.DeploymentKindPreview, ImageDigest: "sha256:" + strings.Repeat("d", 64),
		CommitSHA: sha, Status: state.DeployFailed, CreatedAt: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	assertPage(`preview-env-failed`, `worker deployment failed`)
	set.CommitSHA = strings.Repeat("c", 40)
	if err := store.PutPRPreviewSet(ctx, set); err != nil {
		t.Fatal(err)
	}
	assertPage(`0/2 current-head deployments live`, `preview-env-building`, `missing`)
	if err := store.ClosePRPreviewSet(ctx, 77, "octo/api", 42); err != nil {
		t.Fatal(err)
	}
	assertPage(`preview-env-closed`, `Preview PR #42 is closed.`)

	legacy := seedPreviewForDashboard(t, store, account.ID, "pr-7-legacy", "legacy", 7)
	if rec := get(legacy.Slug); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `id="pr-preview-environment"`) {
		t.Fatalf("legacy preview gained an environment panel: status=%d", rec.Code)
	}
	foreign, err := store.CreateAccount(ctx, "foreign-dashboard-preview@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreignRoot := seedPreviewForDashboard(t, store, foreign.ID, "pr-9-foreign", "foreign", 9)
	if rec := get(foreignRoot.Slug); rec.Code != http.StatusNotFound {
		t.Fatalf("foreign preview detail status=%d, want 404", rec.Code)
	}
}
