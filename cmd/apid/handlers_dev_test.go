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

func TestDevSessionCanUseConsumerAuthenticationWithoutPlanDefaultGates(t *testing.T) {
	e := setup(t, api.PlanPro)
	created := e.do(t, http.MethodPut, "/v1/dev/sessions/scenario-auth", api.UpsertDevSessionRequest{}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", created.Code, created.Body.String())
	}
	var session api.DevSessionResponse
	if err := json.Unmarshal(created.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	before, err := e.store.AppBySlug(t.Context(), session.App.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if !before.RequireAuthn || before.PublicAuthMode != api.AppPublicAuthModeBearer {
		t.Fatalf("expected Pro default auth gates, got require_authn=%t public_auth=%q", before.RequireAuthn, before.PublicAuthMode)
	}
	closed := false
	mode := api.ConsumerAuthModeRequired
	updated := e.do(t, http.MethodPatch, "/v1/apps/"+session.App.Slug, api.UpdateAppRequest{
		RequireAuthn:     &closed,
		PublicAuth:       &api.PublicAuthBlock{Mode: api.AppPublicAuthModeOpen},
		ConsumerAuthMode: &mode,
	}, nil)
	if updated.Code != http.StatusOK {
		t.Fatalf("test access update status = %d: %s", updated.Code, updated.Body.String())
	}
	after, err := e.store.AppBySlug(t.Context(), session.App.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if after.RequireAuthn || after.PublicAuthMode != api.AppPublicAuthModeOpen || after.ConsumerAuthMode != api.ConsumerAuthModeRequired {
		t.Fatalf("test access gates = require_authn=%t public_auth=%q consumer_auth=%q", after.RequireAuthn, after.PublicAuthMode, after.ConsumerAuthMode)
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

func TestDevSessionLeaseBoundedByPlan(t *testing.T) {
	cases := []struct {
		name         string
		plan         api.Plan
		leaseSeconds int64
		wantStatus   int
		wantLease    time.Duration
		wantCode     string
		wantLimit    int64
		wantObserved int64
	}{
		{name: "omitted keeps default", plan: api.PlanHobby, wantStatus: http.StatusCreated, wantLease: 24 * time.Hour},
		{name: "minimum accepted", plan: api.PlanFree, leaseSeconds: 3600, wantStatus: http.StatusCreated, wantLease: time.Hour},
		{name: "plan ceiling accepted", plan: api.PlanHobby, leaseSeconds: 72 * 3600, wantStatus: http.StatusCreated, wantLease: 72 * time.Hour},
		{name: "below minimum rejected", plan: api.PlanPro, leaseSeconds: 60, wantStatus: http.StatusBadRequest, wantCode: api.CodeValidation},
		{name: "negative rejected", plan: api.PlanPro, leaseSeconds: -3600, wantStatus: http.StatusBadRequest, wantCode: api.CodeValidation},
		{name: "free over ceiling", plan: api.PlanFree, leaseSeconds: 25 * 3600, wantStatus: http.StatusForbidden, wantCode: api.CodePlanLimitDeveloperLease, wantLimit: 24, wantObserved: 25},
		{name: "partial hour rounds up", plan: api.PlanHobby, leaseSeconds: 72*3600 + 1, wantStatus: http.StatusForbidden, wantCode: api.CodePlanLimitDeveloperLease, wantLimit: 72, wantObserved: 73},
		{name: "overflow-sized value", plan: api.PlanScale, leaseSeconds: 1 << 62, wantStatus: http.StatusForbidden, wantCode: api.CodePlanLimitDeveloperLease, wantLimit: 336, wantObserved: (1<<62)/3600 + 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := setup(t, tc.plan)
			rec := e.do(t, http.MethodPut, "/v1/dev/sessions/lease-app", api.UpsertDevSessionRequest{LeaseSeconds: tc.leaseSeconds}, nil)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantCode == "" {
				var session api.DevSessionResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
					t.Fatal(err)
				}
				if until := time.Until(session.ExpiresAt); until > tc.wantLease || until < tc.wantLease-time.Minute {
					t.Fatalf("lease = %s, want about %s", until, tc.wantLease)
				}
				return
			}
			var problem api.Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if problem.Code != tc.wantCode {
				t.Fatalf("problem code = %q, want %q", problem.Code, tc.wantCode)
			}
			if tc.wantLimit != 0 && (problem.Limit == nil || *problem.Limit != tc.wantLimit ||
				problem.Observed == nil || *problem.Observed != tc.wantObserved || problem.DocsURL == "") {
				t.Fatalf("lease limit problem = %+v", problem)
			}
			// A rejected lease never creates the environment.
			if _, err := e.store.AppBySlug(t.Context(), devSessionSlug(e.acct.ID, "lease-app", "")); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("rejected lease created an app: err=%v", err)
			}
		})
	}
}

func TestDevSessionRefreshAppliesRequestedLease(t *testing.T) {
	e := setup(t, api.PlanPro)
	first := e.do(t, http.MethodPut, "/v1/dev/sessions/lease-app", api.UpsertDevSessionRequest{LeaseSeconds: 3600}, nil)
	if first.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", first.Code, first.Body.String())
	}
	refreshed := e.do(t, http.MethodPut, "/v1/dev/sessions/lease-app", api.UpsertDevSessionRequest{LeaseSeconds: 7 * 24 * 3600}, nil)
	if refreshed.Code != http.StatusOK {
		t.Fatalf("refresh status = %d: %s", refreshed.Code, refreshed.Body.String())
	}
	var session api.DevSessionResponse
	if err := json.Unmarshal(refreshed.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	row, err := e.store.AppBySlug(t.Context(), session.App.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if row.PreviewExpiresAt == nil || time.Until(*row.PreviewExpiresAt) < 7*24*time.Hour-time.Minute {
		t.Fatalf("stored lease = %v, want about 7 days", row.PreviewExpiresAt)
	}
}
