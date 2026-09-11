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

func TestDashboardHandler_AppLogDrains(t *testing.T) {
	h, cookie, store, _ := newAuthedDashboardServerFullFull(t, "pro", "log-drain-dashboard@example.com")
	acct, err := store.AccountByEmail(t.Context(), "log-drain-dashboard@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "dashboard-health-app", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	drain, err := store.CreateAppLogDrain(t.Context(), state.AppLogDrain{
		AppID: app.ID, AccountID: acct.ID, Kind: state.AppLogDrainKindOTLP,
		TargetURL: "https://logs.example/dashboard", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateAppLogDrain: %v", err)
	}
	if err := store.UpsertAppLogDrainHealth(t.Context(), state.AppLogDrainHealth{
		DrainID: drain.ID, Status: api.AppLogDrainHealthStatusDegraded, Active: true,
		QueueDepth: 5, QueueCapacity: 20, DeliveredTotal: 12, FailedTotal: 2,
		DroppedTotal: 1, RetriesTotal: 4, StreamReconnectsTotal: 1, GapsTotal: 1,
		LastError: "source log gap observed", UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertAppLogDrainHealth: %v", err)
	}

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/apps/dashboard-health-app/log-drains", nil)
	r.AddCookie(cookie)
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"Log-drain health for", "otlp", "degraded", "5 / 20", "delivered 12", "retries 4", "reconnects 1", "source log gap observed"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard body missing %q\n%s", want, body)
		}
	}
	if strings.Contains(body, "authorization") || strings.Contains(body, "secret") {
		t.Fatalf("dashboard body contains credential-adjacent data: %s", body)
	}
}

func TestParseAppLogDrainsPath(t *testing.T) {
	for _, tc := range []struct {
		path string
		want string
		ok   bool
	}{
		{path: "logs-app/log-drains", want: "logs-app", ok: true},
		{path: "logs-app/log-drains/", want: "logs-app", ok: true},
		{path: "logs-app", ok: false},
		{path: "logs-app/log-drains/health", ok: false},
		{path: "logs-app/other-log-drains", ok: false},
	} {
		got, ok := parseAppLogDrainsPath(tc.path)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseAppLogDrainsPath(%q) = (%q, %v), want (%q, %v)", tc.path, got, ok, tc.want, tc.ok)
		}
	}
}

func TestDashboardLogDrainPageItemDefaultsAndMasksState(t *testing.T) {
	lastFailure := time.Now().UTC().Add(-time.Minute)
	item := dashboardLogDrainPageItem(
		state.AppLogDrain{ID: "drain-1", Kind: state.AppLogDrainKindHTTPJSON, TargetURL: "https://logs.example/ingest", Enabled: false},
		state.AppLogDrainHealth{
			DrainID: "drain-1", Status: api.AppLogDrainHealthStatusDegraded, Active: true,
			QueueDepth: 3, QueueCapacity: 10, FailedTotal: 2, LastFailureAt: lastFailure,
			LastError: "delivery queue dropped records", UpdatedAt: lastFailure,
		},
	)
	if item.Status != api.AppLogDrainHealthStatusInactive || item.Active {
		t.Fatalf("disabled drain presentation = status %q active %v, want inactive/false", item.Status, item.Active)
	}
	if item.QueueDepth != 3 || item.QueueCapacity != 10 || item.FailedTotal != 2 || item.LastError == "" {
		t.Fatalf("health counters were not projected: %+v", item)
	}

	unknown := dashboardLogDrainPageItem(state.AppLogDrain{ID: "drain-2", Enabled: true}, state.AppLogDrainHealth{})
	if unknown.Status != api.AppLogDrainHealthStatusUnknown || unknown.Active {
		t.Fatalf("missing health presentation = status %q active %v, want unknown/false", unknown.Status, unknown.Active)
	}
}
