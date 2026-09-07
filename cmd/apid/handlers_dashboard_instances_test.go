package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDashboardHandler_AppInstances(t *testing.T) {
	h, cookie, store, _ := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{
		AccountID: acct.ID, Slug: "instances-app", Type: state.AppTypeApp,
		Runtime: "node22", Status: state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := store.CreateDeployment(t.Context(), state.Deployment{
		ID: "dep-instances", AppID: app.ID, Status: state.DeployLive,
		LivenessRestartCount: 3, ParkedReason: "liveness_exhausted",
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	instance, err := store.CreateInstance(t.Context(), app.ID, dep.ID, "RUNNING", 256, "node-1", "wake-1")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if err := store.SetInstanceRuntime(t.Context(), instance.ID, "netns-1", "10.0.0.7", 1000); err != nil {
		t.Fatalf("SetInstanceRuntime: %v", err)
	}
	if err := store.AppendEvent(t.Context(), "system:schedd", "wake.boot_started", nil,
		[]byte(`{"wake_id":"wake-1","method":"restore","tier":"warm"}`)); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/apps/instances-app/instances", nil)
	req.AddCookie(cookie)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{
		"Instances: <code>instances-app</code>", instance.ID, "node-1", "10.0.0.7",
		"restore", "tier warm", "Liveness restarts", "liveness_exhausted",
		"Crash-loop signal", "/dashboard/apps/instances-app/logs", `name="csrf_token"`,
		"Park app", "Restart app",
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("body missing %q\n%s", want, rec.Body.String())
		}
	}
	var hasActionCookie bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == dashboardInstanceCSRFCookie {
			hasActionCookie = true
			break
		}
	}
	if !hasActionCookie {
		t.Fatalf("GET instances: missing %s cookie", dashboardInstanceCSRFCookie)
	}
}

func TestParseAppInstancesPath(t *testing.T) {
	for _, tc := range []struct {
		path string
		want string
		ok   bool
	}{
		{path: "instances-app/instances", want: "instances-app", ok: true},
		{path: "instances-app/instances/", want: "instances-app", ok: true},
		{path: "instances-app", ok: false},
		{path: "instances-app/instances/foo", ok: false},
		{path: "/instances", ok: false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			got, ok := parseAppInstancesPath(tc.path)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("parseAppInstancesPath(%q) = (%q, %v), want (%q, %v)", tc.path, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestDashboardInstanceAction_ParkRequiresNamedCSRF(t *testing.T) {
	h, cookie, store, sessions := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	_, err = store.CreateApp(t.Context(), state.App{
		AccountID: acct.ID, Slug: "park-action", Type: state.AppTypeApp,
		Runtime: "node22", Status: state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	bad := dashboardPOST(t, h, cookie, "/dashboard/apps/park-action/instances/park", nil)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("missing csrf status = %d, want 400\nbody = %s", bad.Code, bad.Body.String())
	}

	token, err := middleware.IssueForAuthenticatedNamed(sessions, dashboardInstanceAction, acct.ID, dashboardInstanceCSRFCookie)
	if err != nil {
		t.Fatalf("IssueForAuthenticatedNamed: %v", err)
	}
	good := dashboardPOST(t, h, cookie, "/dashboard/apps/park-action/instances/park",
		map[string]string{middleware.FormFieldName: token},
		&http.Cookie{Name: dashboardInstanceCSRFCookie, Value: token})
	if good.Code != http.StatusSeeOther {
		t.Fatalf("valid csrf status = %d, want 303\nbody = %s", good.Code, good.Body.String())
	}
	if loc := good.Header().Get("Location"); !strings.Contains(loc, "action=parked") {
		t.Fatalf("Location = %q, want action=parked", loc)
	}
	updated, err := store.AppBySlug(t.Context(), "park-action")
	if err != nil {
		t.Fatalf("AppBySlug: %v", err)
	}
	if updated.Status != state.AppEvictedCold {
		t.Fatalf("app status = %q, want %q", updated.Status, state.AppEvictedCold)
	}
}
