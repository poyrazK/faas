package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
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

func TestDashboardDebugReplayRequiresCSRF(t *testing.T) {
	h, cookie, store, _ := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	if _, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "debug-replay", Status: state.AppActive}); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/dashboard/apps/debug-replay/debug/requests/00000000-0000-0000-0000-000000000001/replay", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing csrf status = %d, want 400\nbody = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Invalid CSRF token") {
		t.Fatalf("missing csrf response = %s", rec.Body.String())
	}
}

func TestDashboardDebugReplayStatusProjectionIsScoped(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "debug@example.com", "hobby")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "debug-status", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	requestID := "00000000-0000-0000-0000-000000000002"
	metadata, _ := json.Marshal(map[string]string{api.DebugReplayRequestIDHeader: requestID})
	inv, err := store.EnqueueInvocation(context.Background(), state.Invocation{
		AccountID: acct.ID, AppID: app.ID, Source: state.InvocationReplay,
		Headers: metadata, DueAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation: %v", err)
	}
	if _, err := store.ClaimInvocation(context.Background(), inv.ID, "instance-1", 60); err != nil {
		t.Fatalf("ClaimInvocation: %v", err)
	}
	result := json.RawMessage(`{"source_status_code":500,"mirror_status_code":200,"source_latency_ms":90,"mirror_latency_ms":12,"status_diff":true,"crashed":false}`)
	if err := store.CompleteInvocation(context.Background(), inv.ID, result); err != nil {
		t.Fatalf("CompleteInvocation: %v", err)
	}

	s := &server{store: store}
	data := &dashboard.DebugPageData{}
	if err := s.populateDashboardDebugReplay(context.Background(), app, acct, inv.ID, requestID, data); err != nil {
		t.Fatalf("populateDashboardDebugReplay: %v", err)
	}
	if data.Replay == nil || !data.Replay.HasResult || data.Replay.MirrorStatusCode != 200 || !data.Replay.StatusDiff {
		t.Fatalf("replay projection = %#v, want completed comparison", data.Replay)
	}
	if err := s.populateDashboardDebugReplay(context.Background(), app, acct, inv.ID, "different-request", data); err == nil {
		t.Fatal("foreign request id unexpectedly exposed replay")
	}
}

func TestDashboardDebugReplayActionFlash(t *testing.T) {
	for _, tt := range []struct {
		action string
		err    string
		want   string
	}{
		{action: "replay_queued", want: "Replay queued"},
		{action: "replay_error", err: api.CodeDebugReplayUnsupported, want: "enable a mirror rule"},
		{action: "replay_error", err: api.CodeNotFound, want: "aged out"},
	} {
		t.Run(tt.action+tt.err, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/dashboard/apps/debug-status/debug?action="+tt.action+"&error="+tt.err, nil)
			if got := dashboardDebugReplayActionFlash(r); !strings.Contains(got, tt.want) {
				t.Fatalf("flash = %q, want substring %q", got, tt.want)
			}
		})
	}

}
