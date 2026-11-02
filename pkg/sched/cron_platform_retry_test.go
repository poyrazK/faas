package sched_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261026201726 (amendment 2026-10-29)
//
// A 5xx from gatewayd-internal's dispatch endpoint means the gateway refused
// the dispatch before the app saw it; an app's own 5xx arrives inside a 200
// envelope and a 4xx refusal stays permanent (ADR-20261026201726 amendment
// 2026-10-29).
func TestGatewayDispatchRefusalIsMarked(t *testing.T) {
	cases := []struct {
		name                         string
		status                       int
		body                         string
		refused, permanent, answered bool
	}{
		{"policy_unavailable", http.StatusServiceUnavailable,
			`{"code":"traffic_policy_unavailable","detail":"Retry shortly.","status":503}`, true, false, false},
		{"dispatch_failed", http.StatusBadGateway, `{"code":"capacity_unavailable","status":502}`, true, false, false},
		{"refused_permanently", http.StatusConflict, `{"code":"conflict","status":409}`, false, true, false},
		{"app_answered_503", http.StatusOK, `{"state":"completed","status_code":503,"result":{}}`, false, false, true},
		{"app_answered_404", http.StatusOK, `{"state":"failed","status_code":404,"result":{"error":"not_found"}}`, false, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.status == http.StatusOK {
					w.Header().Set("Content-Type", "application/json")
				} else {
					w.Header().Set("Content-Type", "application/problem+json")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			_, err := sched.XHTTPGatewaySynth(srv.Client(), srv.URL).InvokeWithWake(t.Context(), "app-1",
				state.Invocation{ID: "inv-1", AppID: "app-1", Source: state.InvocationCron, Method: http.MethodPost, Path: "/tick"},
				sched.WakeResult{InstanceID: "instance-1", NodeID: "node-1"})
			if err == nil {
				t.Fatal("dispatch reported no error")
			}
			if got := errors.Is(err, sched.ErrDispatchRefused); got != tc.refused {
				t.Fatalf("errors.Is(%v, ErrDispatchRefused) = %v, want %v", err, got, tc.refused)
			}
			if got := errors.Is(err, sched.ErrPermanentInvoke); got != tc.permanent {
				t.Fatalf("errors.Is(%v, ErrPermanentInvoke) = %v, want %v", err, got, tc.permanent)
			}
			if got := errors.Is(err, sched.ErrAppAnswered); got != tc.answered {
				t.Fatalf("errors.Is(%v, ErrAppAnswered) = %v, want %v", err, got, tc.answered)
			}
		})
	}
}

// A plain HTTP cron run the gateway refused before delivery is tried again
// after 5 s and 10 s and fails only after api.CronPlatformAttempts attempts;
// a run that failed for any other reason keeps its single attempt.
func TestDrain_RetriesPlainCronRunRefusedBeforeDelivery(t *testing.T) {
	ctx := context.Background()
	d, store, _, _, synth := sched.NewDrainHarness(t, api.PlanHobby, true)
	now := time.Now().UTC()
	*d.Now() = func() time.Time { return now }
	apps, err := store.ListAllApps(ctx)
	if err != nil || len(apps) == 0 {
		t.Fatalf("ListAllApps: %v / %d apps", err, len(apps))
	}
	enqueue := func() string {
		t.Helper()
		scheduledAt := now
		inv, err := store.EnqueueInvocation(ctx, state.Invocation{
			AppID: apps[0].ID, AccountID: apps[0].AccountID,
			Source: state.InvocationCron, Method: http.MethodPost, Path: "/tick",
			DueAt: scheduledAt, ScheduledAt: &scheduledAt,
			RetryPolicyJSON: json.RawMessage(`{"max_attempts":1}`),
		})
		if err != nil {
			t.Fatalf("EnqueueInvocation: %v", err)
		}
		return inv.ID
	}
	read := func(id string) state.Invocation {
		t.Helper()
		inv, err := store.InvocationByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return inv
	}

	refused := enqueue()
	synth.Refused().Store(true)
	for attempt := 1; attempt <= api.CronPlatformAttempts; attempt++ {
		d.Tick(ctx)
		inv := read(refused)
		if inv.Attempts != attempt {
			t.Fatalf("attempt %d: row has %d attempts", attempt, inv.Attempts)
		}
		if attempt < api.CronPlatformAttempts {
			if inv.State != state.InvocationPending {
				t.Fatalf("attempt %d: state %s, want pending for a retry", attempt, inv.State)
			}
			wait := time.Duration(attempt) * api.CronPlatformRetryDelay
			now = now.Add(wait + time.Second)
			continue
		}
		if inv.State != state.InvocationFailed {
			t.Fatalf("after %d refusals: state %s, want failed (not dead-lettered)", attempt, inv.State)
		}
	}
	synth.Refused().Store(false)

	answered := enqueue()
	synth.Transient().Store(true)
	d.Tick(ctx)
	synth.Transient().Store(false)
	if inv := read(answered); inv.State != state.InvocationFailed || inv.Attempts != 1 {
		t.Fatalf("a run the platform delivered: state %s after %d attempts, want failed after 1", inv.State, inv.Attempts)
	}
}
