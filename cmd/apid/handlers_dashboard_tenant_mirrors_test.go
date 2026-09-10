package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestParseAppTenantAndMirrorPaths(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		want string
		ok   bool
	}{
		{name: "tenant", path: "demo/tenant-surfaces", want: "demo", ok: true},
		{name: "tenant trailing slash", path: "demo/tenant-surfaces/", want: "demo", ok: true},
		{name: "mirror", path: "demo/mirrors", want: "demo", ok: true},
		{name: "nested", path: "demo/other/mirrors", ok: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			var ok bool
			if strings.Contains(tc.path, "tenant-surfaces") {
				got, ok = parseAppTenantSurfacesPath(tc.path)
			} else {
				got, ok = parseAppMirrorsPath(tc.path)
			}
			if got != tc.want || ok != tc.ok {
				t.Fatalf("path %q = (%q, %v), want (%q, %v)", tc.path, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestDashboardHandler_TenantSurfacesFixture(t *testing.T) {
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "true")
	h, cookie, store, _ := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	if err := store.UpdateAccountPlan(t.Context(), acct.ID, api.PlanPro); err != nil {
		t.Fatalf("UpdateAccountPlan: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "surface-app", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	limits := api.MustLimitsFor(api.PlanPro)
	surface, err := store.CreateTenantSurfaceIfUnderQuota(t.Context(), state.CreateTenantSurfaceParams{AccountID: acct.ID, AppID: app.ID, Name: "customer-hosts", CertKind: state.CertKindPerHostSAN}, limits)
	if err != nil {
		t.Fatalf("CreateTenantSurface: %v", err)
	}
	if _, err := store.CreateTenantHostnameIfUnderQuota(t.Context(), state.CreateTenantHostnameParams{SurfaceID: surface.ID, Hostname: "api.example.com", ChallengeToken: "test-token"}, limits); err != nil {
		t.Fatalf("CreateTenantHostname: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/apps/surface-app/tenant-surfaces", nil)
	req.AddCookie(cookie)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"Tenant surfaces for", "customer-hosts", "api.example.com", "pending", "_faas-verify.api.example.com", "Create a tenant surface"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("body missing %q\n%s", want, rec.Body.String())
		}
	}
	if findDashboardCookie(rec.Result().Cookies(), dashboardTenantSurfacesCSRFCookie) == nil {
		t.Fatalf("GET tenant surfaces: missing %s cookie", dashboardTenantSurfacesCSRFCookie)
	}
}

func TestDashboardHandler_MirrorsFixture(t *testing.T) {
	h, cookie, store, _ := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	if err := store.UpdateAccountPlan(t.Context(), acct.ID, api.PlanPro); err != nil {
		t.Fatalf("UpdateAccountPlan: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "mirror-app", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	first, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:first", Status: state.DeployPending})
	if err != nil {
		t.Fatalf("CreateDeployment first: %v", err)
	}
	second, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:second", Status: state.DeployPending})
	if err != nil {
		t.Fatalf("CreateDeployment second: %v", err)
	}
	if err := store.MarkDeploymentLive(t.Context(), first.ID); err != nil {
		t.Fatalf("MarkDeploymentLive first: %v", err)
	}
	if err := store.MarkDeploymentLive(t.Context(), second.ID); err != nil {
		t.Fatalf("MarkDeploymentLive second: %v", err)
	}
	if _, err := store.CreateMirrorRuleIfUnderQuota(t.Context(), state.CreateMirrorRuleParams{AccountID: acct.ID, AppID: app.ID, SourceDeploymentID: first.ID, MirrorDeploymentID: second.ID, Percent: 25, IncludeBody: true}, api.MustLimitsFor(api.PlanPro)); err != nil {
		t.Fatalf("CreateMirrorRule: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/apps/mirror-app/mirrors", nil)
	req.AddCookie(cookie)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"Traffic mirrors for", first.ID, second.ID, "25% traffic", "Invocations", "Status diffs", "last 1h", "Create a mirror rule"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("body missing %q\n%s", want, rec.Body.String())
		}
	}
	if findDashboardCookie(rec.Result().Cookies(), dashboardMirrorsCSRFCookie) == nil {
		t.Fatalf("GET mirrors: missing %s cookie", dashboardMirrorsCSRFCookie)
	}
}
