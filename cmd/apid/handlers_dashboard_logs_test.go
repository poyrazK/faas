package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestDashboardHandler_AppLogs(t *testing.T) {
	h, cookie, store, _ := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{
		AccountID: acct.ID,
		Slug:      "logs-app",
		Type:      state.AppTypeApp,
		Runtime:   "node22",
		Status:    state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := store.CreateDeployment(t.Context(), state.Deployment{
		AppID:     app.ID,
		Kind:      state.DeploymentKindImage,
		Status:    state.DeployLive,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	_, err = store.CreateInstance(t.Context(), app.ID, dep.ID, "running", 256, "node-1", "wake-1")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/apps/logs-app/logs?level=warn&grep=GET&since=2026-09-01T00%3A00%3A00Z&deployment="+dep.ID, nil)
	r.AddCookie(cookie)
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Logs: <code>logs-app</code>",
		"name=\"level\"",
		"name=\"grep\" value=\"GET\"",
		"name=\"deployment\"",
		"follow=1",
		"level=warn",
		"grep=GET",
		dep.ID,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n%s", want, body)
		}
	}
	if strings.Contains(body, "Archived logs") {
		t.Errorf("free-plan page unexpectedly rendered archive controls")
	}
}

func TestDashboardHandler_AppLogsArchiveSelector(t *testing.T) {
	h, cookie, store, _ := newAuthedDashboardServerFullFull(t, "hobby", "logs@example.com")
	acct, err := store.AccountByEmail(t.Context(), "logs@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "archived-app", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	instance, err := store.CreateInstance(t.Context(), app.ID, dep.ID, "running", 256, "node-1", "wake-1")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/apps/archived-app/logs", nil)
	r.AddCookie(cookie)
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Archived logs") || !strings.Contains(body, instance.ID) || !strings.Contains(body, "archive=1") {
		t.Fatalf("archive selector missing from page\n%s", body)
	}
}

func TestParseAppLogsPath(t *testing.T) {
	for _, tc := range []struct {
		path string
		want string
		ok   bool
	}{
		{path: "logs-app/logs", want: "logs-app", ok: true},
		{path: "logs-app/logs/", want: "logs-app", ok: true},
		{path: "logs-app", ok: false},
		{path: "logs-app/deployments/dep-1", ok: false},
	} {
		got, ok := parseAppLogsPath(tc.path)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseAppLogsPath(%q) = (%q, %v), want (%q, %v)", tc.path, got, ok, tc.want, tc.ok)
		}
	}
}
