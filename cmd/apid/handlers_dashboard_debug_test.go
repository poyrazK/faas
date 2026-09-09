package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestParseAppDebugPath(t *testing.T) {
	// adr: 127 — §7 pins the customer-facing dashboard route and its
	// deliberate single-level app/debug path shape.
	tests := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{name: "bare", in: "my-app/debug", want: "my-app", ok: true},
		{name: "trailing slash", in: "my-app/debug/", want: "my-app", ok: true},
		{name: "nested", in: "my-app/debug/request/1", ok: false},
		{name: "empty", in: "/debug", ok: false},
		{name: "invalid slug", in: "bad%2fapp/debug", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseAppDebugPath(tt.in)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("parseAppDebugPath(%q) = (%q, %v), want (%q, %v)", tt.in, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestDashboardHandler_DebugPagePlanGate(t *testing.T) {
	// adr: 127 — §7: debugger access is plan-gated like the API surface.
	h, cookie, store, _ := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	if _, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "debug-free", Status: state.AppActive}); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/apps/debug-free/debug", nil)
	req.AddCookie(cookie)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Production debugging is available on Hobby") {
		t.Fatalf("plan-gate copy missing from response: %s", rec.Body.String())
	}
}

func TestDashboardHandler_DebugPageDegradesWhenTelemetryUnavailable(t *testing.T) {
	// adr: 127 — §7: the dashboard is a read-only projection of the API
	// contract and must render a useful degraded state when telemetry
	// storage is unavailable.
	h, cookie, store, _ := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	if err := store.UpdateAccountPlan(t.Context(), acct.ID, api.PlanHobby); err != nil {
		t.Fatalf("UpdateAccountPlan: %v", err)
	}
	acct, err = store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail after plan update: %v", err)
	}
	if _, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "debug-hobby", Status: state.AppActive}); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/apps/debug-hobby/debug?since=24h", nil)
	req.AddCookie(cookie)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Production debugger:") || !strings.Contains(body, "debug-hobby") {
		t.Fatalf("debugger heading missing from response: %s", body)
	}
	if !strings.Contains(body, "Debugger telemetry is temporarily unavailable") {
		t.Fatalf("degraded telemetry copy missing from response: %s", body)
	}
}
