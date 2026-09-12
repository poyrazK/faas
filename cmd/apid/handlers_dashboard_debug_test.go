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
	if !strings.Contains(body, "Why is this app running?") || !strings.Contains(body, "No scheduler observation was retained") {
		t.Fatalf("running debugger empty state missing from response: %s", body)
	}
}

func TestDashboardHandler_DebugRunningPanelProjectsObservation(t *testing.T) {
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
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "debug-running-panel", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	at := time.Now().UTC().Add(-time.Minute)
	payload, err := json.Marshal(debugRunningEvent{
		SchemaVersion:          1,
		AppID:                  app.ID,
		ObservedAt:             at.Format(time.RFC3339Nano),
		RunningInstances:       1,
		ConfiguredMinInstances: 0,
		EffectiveMinInstances:  0,
		IdleTimeoutSeconds:     60,
		Causes: []api.DebugRunningCause{{
			Code:            api.DebugRunningReasonOpenConnection,
			Summary:         "1 active TCP connection(s) keep the instance warm; protocol is not identified.",
			InstanceCount:   1,
			OpenConnections: 1,
		}},
	})
	if err != nil {
		t.Fatalf("marshal running event: %v", err)
	}
	subject := app.ID
	if err := store.AppendEventAt(t.Context(), "schedd", debugRunningEventKind, &subject, payload, at); err != nil {
		t.Fatalf("AppendEventAt: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/apps/debug-running-panel/debug?since=3h", nil)
	req.AddCookie(cookie)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Why is this app running?",
		"Current observed causes",
		"Open connection",
		"1 active TCP connection(s) keep the instance warm",
		"Configuration context",
		"Recent observations",
		"gregale debug running --since 3h debug-running-panel",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("running panel missing %q\nbody = %s", want, body)
		}
	}
}

func TestDashboardDebugRunningCauseLabels(t *testing.T) {
	for _, tt := range []struct {
		code string
		want string
	}{
		{api.DebugRunningReasonRequestActivity, "Request activity"},
		{api.DebugRunningReasonOpenConnection, "Open connection"},
		{api.DebugRunningReasonTailTasks, "Background tasks"},
		{api.DebugRunningReasonMinInstances, "Configured minimum"},
		{api.DebugRunningReasonPrewarmFloor, "Prewarm floor"},
		{api.DebugRunningReasonScaleInCooldown, "Scale-in cooldown"},
		{api.DebugRunningReasonWorkloadMode, "Workload mode"},
		{api.DebugRunningReasonStartupGrace, "Startup grace"},
		{api.DebugRunningReasonUnknownActivity, "Activity signal unavailable"},
		{api.DebugRunningReasonNoBlockerObserved, "No blocker observed"},
		{"future_reason", "Observed cause"},
	} {
		t.Run(tt.code, func(t *testing.T) {
			if got := dashboardDebugRunningCauseLabel(tt.code); got != tt.want {
				t.Fatalf("dashboardDebugRunningCauseLabel(%q) = %q, want %q", tt.code, got, tt.want)
			}
		})
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

func TestDashboardDebugReplayPollIsBounded(t *testing.T) {
	for _, tt := range []struct {
		raw  string
		want int
	}{
		{raw: "", want: 0},
		{raw: "3", want: 3},
		{raw: "-1", want: 0},
		{raw: "garbage", want: 0},
		{raw: "999", want: dashboardDebugReplayPollLimit},
	} {
		if got := parseDashboardDebugReplayPoll(tt.raw); got != tt.want {
			t.Errorf("parseDashboardDebugReplayPoll(%q) = %d, want %d", tt.raw, got, tt.want)
		}
	}
}

func TestDashboardDebugRegressionCompareURLUsesRetainedPeer(t *testing.T) {
	deployments := []dashboard.DebugDeploymentView{
		{ID: "11111111-1111-1111-1111-111111111111"},
		{ID: "22222222-2222-2222-2222-222222222222"},
	}
	got := dashboardDebugRegressionCompareURL("debug-app", "24h", deployments[0].ID, "/api/items", deployments)
	if !strings.Contains(got, "/dashboard/apps/debug-app/debug?") ||
		!strings.Contains(got, "compare_source="+deployments[0].ID) ||
		!strings.Contains(got, "compare_mirror="+deployments[1].ID) ||
		!strings.Contains(got, "compare=1") ||
		!strings.Contains(got, "compare_route=%2Fapi%2Fitems") {
		t.Fatalf("compare URL = %q, missing retained source/mirror selection", got)
	}
	if got := dashboardDebugRegressionCompareURL("debug-app", "24h", "missing", "/api/items", deployments); got != "" {
		t.Fatalf("compare URL for an unknown source = %q, want empty", got)
	}
}
