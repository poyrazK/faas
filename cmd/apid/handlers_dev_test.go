package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDevSessionCreateRefreshAndDestroy(t *testing.T) {
	e := setup(t, api.PlanHobby)
	project := "gregale-api"
	wantSlug := devSessionSlug(e.acct.ID, project, "")

	created := e.do(t, "PUT", "/v1/dev/sessions/"+project, api.UpsertDevSessionRequest{}, nil)
	if created.Code != 201 {
		t.Fatalf("create status = %d, want 201: %s", created.Code, created.Body.String())
	}
	var first api.DevSessionResponse
	if err := json.Unmarshal(created.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if first.App.Slug != wantSlug || first.App.URL != "https://"+wantSlug+".gregale.dev" {
		t.Fatalf("unexpected developer app: %+v", first.App)
	}
	if until := time.Until(first.ExpiresAt); until < 23*time.Hour || until > 25*time.Hour {
		t.Fatalf("lease duration = %s, want about 24h", until)
	}
	row, err := e.store.AppBySlug(t.Context(), wantSlug)
	if err != nil {
		t.Fatalf("load developer app: %v", err)
	}
	if row.PreviewOfSlug != project || row.PreviewPrNumber != 0 || row.PreviewPrState != state.PreviewPrStateOpen {
		t.Fatalf("developer preview metadata = %+v", row)
	}

	refreshed := e.do(t, "PUT", "/v1/dev/sessions/"+project, api.UpsertDevSessionRequest{}, nil)
	if refreshed.Code != 200 {
		t.Fatalf("refresh status = %d, want 200: %s", refreshed.Code, refreshed.Body.String())
	}
	var second api.DevSessionResponse
	if err := json.Unmarshal(refreshed.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode refresh response: %v", err)
	}
	if second.App.ID != first.App.ID {
		t.Fatalf("refresh changed app id: first=%s second=%s", first.App.ID, second.App.ID)
	}

	destroyed := e.do(t, "DELETE", "/v1/dev/sessions/"+project, nil, nil)
	if destroyed.Code != 204 {
		t.Fatalf("destroy status = %d, want 204: %s", destroyed.Code, destroyed.Body.String())
	}
	if _, err := e.store.AppBySlug(t.Context(), wantSlug); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("developer app after destroy: err=%v, want ErrNotFound", err)
	}
}

func TestDevSessionRejectsFunctionWithoutRuntime(t *testing.T) {
	e := setup(t, api.PlanHobby)
	rec := e.do(t, "PUT", "/v1/dev/sessions/my-function", api.UpsertDevSessionRequest{Type: "function"}, nil)
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestDevSessionUsesSeparateQuota(t *testing.T) {
	e := setup(t, api.PlanFree)

	first := e.do(t, "PUT", "/v1/dev/sessions/first-app", api.UpsertDevSessionRequest{}, nil)
	if first.Code != http.StatusCreated {
		t.Fatalf("first developer session status = %d, want 201: %s", first.Code, first.Body.String())
	}
	// A Free account can still create its normal deployed-app slot while the
	// developer environment is live: the two budgets are intentionally split.
	production := state.App{AccountID: e.acct.ID, Slug: "production-app", Status: state.AppActive}
	if _, err := e.store.CreateAppIfUnderQuota(t.Context(), production, api.MustLimitsFor(api.PlanFree)); err != nil {
		t.Fatalf("production app was blocked by developer session: %v", err)
	}

	second := e.do(t, "PUT", "/v1/dev/sessions/second-app", api.UpsertDevSessionRequest{}, nil)
	if second.Code != http.StatusForbidden {
		t.Fatalf("second developer session status = %d, want 403: %s", second.Code, second.Body.String())
	}
	var problem api.Problem
	if err := json.Unmarshal(second.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode developer quota problem: %v", err)
	}
	if problem.Code != api.CodePlanLimitDeveloperApps || problem.Limit == nil || *problem.Limit != 1 || problem.Observed == nil || *problem.Observed != 1 {
		t.Fatalf("developer quota problem = %+v", problem)
	}
}

func TestDevSessionSlugStableAndBounded(t *testing.T) {
	a := devSessionSlug("account-a", "a-very-long-project-name-that-needs-truncation", "")
	b := devSessionSlug("account-a", "a-very-long-project-name-that-needs-truncation", "")
	c := devSessionSlug("account-b", "a-very-long-project-name-that-needs-truncation", "")
	if a != b {
		t.Fatalf("slug is not stable: %q != %q", a, b)
	}
	if a == c {
		t.Fatalf("account-scoped slugs collided: %q", a)
	}
	if len(a) > 40 {
		t.Fatalf("slug length = %d, want <= 40: %q", len(a), a)
	}
	if legacy := devSessionSlug("account-a", "gregale-api", ""); legacy != "dev-gregale-api-ef301a416b46" {
		t.Fatalf("legacy slug changed: %q", legacy)
	}
}

func TestDevSessionsAreIsolatedByWorkspace(t *testing.T) {
	e := setup(t, api.PlanHobby)
	const (
		project    = "gregale-api"
		workspaceA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		workspaceB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)

	create := func(workspaceID string) api.DevSessionResponse {
		t.Helper()
		rec := e.do(t, "PUT", "/v1/dev/sessions/"+project, api.UpsertDevSessionRequest{WorkspaceID: workspaceID}, nil)
		if rec.Code != 201 {
			t.Fatalf("create workspace %s status = %d, want 201: %s", workspaceID, rec.Code, rec.Body.String())
		}
		var response api.DevSessionResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode workspace %s: %v", workspaceID, err)
		}
		return response
	}

	first := create(workspaceA)
	second := create(workspaceB)
	if first.App.ID == second.App.ID || first.App.Slug == second.App.Slug {
		t.Fatalf("workspace sessions collided: first=%+v second=%+v", first.App, second.App)
	}
	if first.App.Slug != devSessionSlug(e.acct.ID, project, workspaceA) || second.App.Slug != devSessionSlug(e.acct.ID, project, workspaceB) {
		t.Fatalf("unexpected workspace slugs: first=%q second=%q", first.App.Slug, second.App.Slug)
	}

	destroyed := e.do(t, "DELETE", "/v1/dev/sessions/"+project+"?workspace_id="+workspaceA, nil, nil)
	if destroyed.Code != 204 {
		t.Fatalf("destroy workspace A status = %d, want 204: %s", destroyed.Code, destroyed.Body.String())
	}
	if _, err := e.store.AppBySlug(t.Context(), first.App.Slug); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("workspace A after destroy: err=%v, want ErrNotFound", err)
	}
	if _, err := e.store.AppBySlug(t.Context(), second.App.Slug); err != nil {
		t.Fatalf("workspace B was removed with workspace A: %v", err)
	}
}

func TestDevSessionRejectsInvalidWorkspaceID(t *testing.T) {
	e := setup(t, api.PlanHobby)
	rec := e.do(t, "PUT", "/v1/dev/sessions/gregale-api", api.UpsertDevSessionRequest{WorkspaceID: "not-a-workspace"}, nil)
	if rec.Code != 400 {
		t.Fatalf("PUT status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, "DELETE", "/v1/dev/sessions/gregale-api?workspace_id=ABCDEF0123456789ABCDEF0123456789", nil, nil)
	if rec.Code != 400 {
		t.Fatalf("DELETE status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}
