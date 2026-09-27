package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestDashboardAsyncInvocationDetail_ShowsLifecycleWithoutRequestOrResultBodies(t *testing.T) {
	h, sessionCookie, store, _ := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{
		AccountID: acct.ID, Slug: "async-detail-app", Type: state.AppTypeApp,
		Runtime: "node22", Status: state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	accepted := time.Date(2026, time.September, 26, 10, 0, 0, 0, time.UTC)
	started := accepted.Add(time.Second)
	finished := accepted.Add(5 * time.Second)
	deadline := accepted.Add(time.Hour)
	retention := finished.Add(24 * time.Hour)
	outcome := state.OutcomeDeadLetter
	inv, err := store.EnqueueInvocation(t.Context(), state.Invocation{
		AccountID: acct.ID, AppID: app.ID, Source: state.InvocationAsyncInvoke,
		State: state.InvocationDeadLetter, Method: http.MethodPost, Path: "/reports",
		DueAt: accepted, CreatedAt: accepted, ReceivedAt: &started, CompletedAt: &finished,
		DeadlineAt: &deadline, ResultRetentionUntil: &retention,
		Attempts: 4, Outcome: &outcome, LastError: "upstream timed out",
		RetryPolicyJSON:        json.RawMessage(`{"max_attempts":4,"base_seconds":1,"max_seconds":30,"jitter_seconds":0.2}`),
		OnSuccessDestinationID: "success-webhook", OnFailureDestinationID: "failure-webhook",
		Payload: json.RawMessage(`{"secret":"payload-should-not-render"}`),
		Headers: json.RawMessage(`{"authorization":"header-should-not-render"}`),
		Result:  json.RawMessage(`{"secret":"result-should-not-render"}`),
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/invocations/"+inv.ID, nil)
	req.AddCookie(sessionCookie)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Async invocation", inv.ID, "async-detail-app", "POST /reports", "dead_letter", "dead_letter",
		"upstream timed out", "up to 4 attempts", "base delay 1s", "delay cap 30s", "±20% jitter",
		accepted.Format(time.RFC3339), started.Format(time.RFC3339), finished.Format(time.RFC3339),
		deadline.Format(time.RFC3339), retention.Format(time.RFC3339),
		"/dashboard/apps/async-detail-app/webhooks#webhook-success-webhook",
		"/dashboard/apps/async-detail-app/webhooks#webhook-failure-webhook",
		"gregale invocations get " + inv.ID,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n%s", want, body)
		}
	}
	for _, private := range []string{"payload-should-not-render", "header-should-not-render", "result-should-not-render"} {
		if strings.Contains(body, private) {
			t.Errorf("dashboard invocation detail leaked private marker %q", private)
		}
	}
}

func TestDashboardAsyncInvocationDetail_IsAccountScopedAndAsyncOnly(t *testing.T) {
	h, sessionCookie, store, _ := newAuthedDashboardServerFull(t)
	alice, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail alice: %v", err)
	}
	bob, err := store.CreateAccount(t.Context(), "bob@example.com", "free")
	if err != nil {
		t.Fatalf("CreateAccount bob: %v", err)
	}
	bobApp, err := store.CreateApp(t.Context(), state.App{
		AccountID: bob.ID, Slug: "bob-async-app", Type: state.AppTypeApp,
		Runtime: "node22", Status: state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp bob: %v", err)
	}
	aliceApp, err := store.CreateApp(t.Context(), state.App{
		AccountID: alice.ID, Slug: "alice-queue-app", Type: state.AppTypeApp,
		Runtime: "node22", Status: state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp alice: %v", err)
	}
	foreign, err := store.EnqueueInvocation(t.Context(), state.Invocation{
		AccountID: bob.ID, AppID: bobApp.ID, Source: state.InvocationAsyncInvoke,
		State: state.InvocationPending, Method: http.MethodPost, Path: "/private",
		DueAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation foreign: %v", err)
	}
	queueInvocation, err := store.EnqueueInvocation(t.Context(), state.Invocation{
		AccountID: alice.ID, AppID: aliceApp.ID, Source: state.InvocationQueue,
		State: state.InvocationPending, DueAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation queue: %v", err)
	}

	for _, id := range []string{foreign.ID, queueInvocation.ID, "missing-invocation"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/dashboard/invocations/"+id, nil)
		req.AddCookie(sessionCookie)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("invocation %q status = %d, want 404\nbody = %s", id, rec.Code, rec.Body.String())
		}
	}
}
