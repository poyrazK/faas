package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDashboardPreAuthRendersScopedObservations(t *testing.T) {
	e := setup(t, api.PlanHobby)
	config := &api.PreAuthRateLimitConfig{
		Mode: api.PreAuthRateLimitObserve, RequestsPerSecond: 20, Burst: 40,
		Routes: []api.PreAuthRouteLimit{{Method: "POST", Path: "/login", RequestsPerSecond: 2, Burst: 4,
			Coordination: api.PreAuthCoordinationCentral, ObserveTargets: true,
			FailedResponses: &api.PreAuthFailedResponseLimit{FailuresPerMinute: 5, Burst: 3, Coordination: api.PreAuthCoordinationCentral}}},
	}
	if rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "protected-app", PreAuthRateLimit: config}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", rec.Code, rec.Body.String())
	}
	installPromFixture(t, &e, func(query string) string {
		if !strings.Contains(query, `[15m]`) || !strings.Contains(query, `gateway_pre_auth_policy_shadow_total`) {
			t.Errorf("unexpected query: %s", query)
		}
		return `{"status":"success","data":{"resultType":"vector","result":[
			{"metric":{"policy":"route_0","outcome":"would_block"},"value":[0,"3"]},
			{"metric":{"policy":"route_0","outcome":"result_2xx"},"value":[0,"1"]},
			{"metric":{"policy":"targets_0","outcome":"target_failure"},"value":[0,"7"]},
			{"metric":{"policy":"targets_0","outcome":"target_threshold"},"value":[0,"2"]},
			{"metric":{"policy":"targets_0","outcome":"target_fallback"},"value":[0,"1"]},
			{"metric":{"policy":"foreign","outcome":"would_block"},"value":[0,"999"]}
		]}}`
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/apps/protected-app/pre-auth?range=15m", nil)
	e.s.renderAppPreAuth(rec, req, slog.New(slog.NewTextHandler(io.Discard, nil)), e.acct, "protected-app")
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Pre-auth protection", "Mode: <strong>observe</strong>", "POST /login", "Selected failures",
		"Local fallback", "<option value=\"15m\" selected>", ">7</td>", ">2</td>",
		"/dashboard/apps/protected-app#alert-presets",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
	// Match the rendered count cell, not a bare "999": the page embeds random
	// hex ids and nonces that contain it by chance.
	if strings.Contains(body, ">999</td>") || strings.Contains(body, "target_digest") {
		t.Fatalf("dashboard exposed foreign observation or digest")
	}
}

func TestDashboardPreAuthOffDegradedAndTenantIsolation(t *testing.T) {
	h, cookie, store, _ := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "off-app"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "observe-app", Manifest: state.AppManifest{
		PreAuthRateLimit: &api.PreAuthRateLimitConfig{Mode: api.PreAuthRateLimitObserve, RequestsPerSecond: 1, Burst: 1},
	}}); err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateAccount(t.Context(), "bob@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(t.Context(), state.App{AccountID: other.ID, Slug: "other-app"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, want string
		status     int
	}{
		{"/dashboard/apps/off-app/pre-auth", "Pre-auth protection is off", http.StatusOK},
		{"/dashboard/apps/observe-app/pre-auth?range=bogus", "Observation data is unavailable", http.StatusOK},
		{"/dashboard/apps/other-app/pre-auth", "", http.StatusNotFound},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.status || (tc.want != "" && !strings.Contains(rec.Body.String(), tc.want)) {
			t.Errorf("%s: status=%d body=%q", tc.path, rec.Code, rec.Body.String())
		}
		if tc.path == "/dashboard/apps/observe-app/pre-auth?range=bogus" &&
			(!strings.Contains(rec.Body.String(), "<option value=\"5m\" selected>") || strings.Contains(rec.Body.String(), "<option value=\"bogus\"")) {
			t.Error("invalid range did not fall back to 5m")
		}
	}
}

func TestParseAppPreAuthPath(t *testing.T) {
	for _, tc := range []struct {
		input, slug string
		ok          bool
	}{
		{"my-app/pre-auth", "my-app", true},
		{"my-app/pre-auth/extra", "", false},
		{"my-app/other/pre-auth", "", false},
		{"/pre-auth", "", false},
	} {
		slug, ok := parseAppPreAuthPath(tc.input)
		if slug != tc.slug || ok != tc.ok {
			t.Errorf("parseAppPreAuthPath(%q) = %q, %v", tc.input, slug, ok)
		}
	}
}
