package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDashboardHandler_JobsQueues(t *testing.T) {
	h, sessionCookie, store, _ := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{
		AccountID: acct.ID, Slug: "jobs-app", Type: state.AppTypeApp,
		Runtime: "node22", Status: state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	job, err := store.JobCreate(t.Context(), acct.ID, "nightly", "app", "oci://example/jobs@sha256:abc", []string{"/bin/run"}, 256, 90, 2, 3, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("JobCreate: %v", err)
	}
	if _, _, err := store.JobRunCreate(t.Context(), job.ID, acct.ID, "manual", nil, nil, nil, json.RawMessage(`{}`), 2); err != nil {
		t.Fatalf("JobRunCreate: %v", err)
	}
	now := time.Now().UTC()
	if _, err := store.EnqueueInvocation(t.Context(), state.Invocation{
		AccountID: acct.ID, AppID: app.ID, Source: state.InvocationQueue,
		State: state.InvocationPending, DueAt: now, Payload: json.RawMessage(`{"event":"pending"}`),
	}); err != nil {
		t.Fatalf("pending invocation: %v", err)
	}
	if _, err := store.EnqueueInvocation(t.Context(), state.Invocation{
		AccountID: acct.ID, AppID: app.ID, Source: state.InvocationQueue,
		State: state.InvocationDeadLetter, DueAt: now, Attempts: 3,
		LastError: "worker failed", Payload: json.RawMessage(`{"event":"dead"}`),
	}); err != nil {
		t.Fatalf("dead-letter invocation: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/jobs", nil)
	req.AddCookie(sessionCookie)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{
		"Jobs &amp; queues", "nightly", "manual", "jobs-app", "pending", "dead_letter",
		"worker failed", "/dashboard/apps/jobs-app/queues", `name="csrf_token"`,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("body missing %q\n%s", want, rec.Body.String())
		}
	}
	if cookie := findDashboardCookie(rec.Result().Cookies(), dashboardJobsCSRFCookie); cookie == nil || cookie.Value == "" {
		t.Fatalf("GET jobs: missing %s cookie", dashboardJobsCSRFCookie)
	}
}

func TestDashboardQueueDeadLetterReplay_AppScopedAndCSRFProtected(t *testing.T) {
	h, sessionCookie, store, sessions := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "replay-app", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	_, err = store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "other-app", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp other: %v", err)
	}
	inv, err := store.EnqueueInvocation(t.Context(), state.Invocation{
		AccountID: acct.ID, AppID: app.ID, Source: state.InvocationQueue,
		State: state.InvocationDeadLetter, DueAt: time.Now(), Payload: json.RawMessage(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("dead-letter invocation: %v", err)
	}

	missing := dashboardPOST(t, h, sessionCookie, "/dashboard/apps/replay-app/queues/dead_letter/"+inv.ID+"/replay", nil)
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing csrf status = %d, want 400\nbody = %s", missing.Code, missing.Body.String())
	}

	token, err := middleware.IssueForAuthenticatedNamed(sessions, dashboardJobsAction, acct.ID, dashboardJobsCSRFCookie)
	if err != nil {
		t.Fatalf("IssueForAuthenticatedNamed: %v", err)
	}
	wrongApp := dashboardPOST(t, h, sessionCookie, "/dashboard/apps/other-app/queues/dead_letter/"+inv.ID+"/replay",
		map[string]string{middleware.FormFieldName: token}, &http.Cookie{Name: dashboardJobsCSRFCookie, Value: token})
	if wrongApp.Code != http.StatusNotFound {
		t.Fatalf("wrong app status = %d, want 404\nbody = %s", wrongApp.Code, wrongApp.Body.String())
	}
	stillDead, err := store.InvocationByID(t.Context(), inv.ID)
	if err != nil {
		t.Fatalf("InvocationByID after wrong app: %v", err)
	}
	if stillDead.State != state.InvocationDeadLetter {
		t.Fatalf("wrong-app replay changed state to %q", stillDead.State)
	}

	good := dashboardPOST(t, h, sessionCookie, "/dashboard/apps/replay-app/queues/dead_letter/"+inv.ID+"/replay",
		map[string]string{middleware.FormFieldName: token}, &http.Cookie{Name: dashboardJobsCSRFCookie, Value: token})
	if good.Code != http.StatusSeeOther {
		t.Fatalf("valid replay status = %d, want 303\nbody = %s", good.Code, good.Body.String())
	}
	if loc := good.Header().Get("Location"); !strings.Contains(loc, "action=replayed") || !strings.Contains(loc, "app=replay-app") {
		t.Fatalf("Location = %q, want replay flash and app filter", loc)
	}
	replayed, err := store.InvocationByID(t.Context(), inv.ID)
	if err != nil {
		t.Fatalf("InvocationByID after replay: %v", err)
	}
	if replayed.State != state.InvocationPending || replayed.Attempts != 0 {
		t.Fatalf("replayed invocation = state %q attempts %d, want pending/0", replayed.State, replayed.Attempts)
	}
}

func TestDashboardAppQueues_ForeignAppIsNotFound(t *testing.T) {
	h, sessionCookie, store, _ := newAuthedDashboardServerFull(t)
	other, err := store.CreateAccount(t.Context(), "bob@example.com", "hobby")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if _, err := store.CreateApp(t.Context(), state.App{AccountID: other.ID, Slug: "foreign-queue", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive}); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/apps/foreign-queue/queues", nil)
	req.AddCookie(sessionCookie)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign app status = %d, want 404", rec.Code)
	}
}

func TestParseAppJobsPath(t *testing.T) {
	got, ok := parseAppJobsPath("queue-app/jobs/")
	if !ok || got != "queue-app" {
		t.Fatalf("parseAppJobsPath = (%q, %v), want (queue-app, true)", got, ok)
	}
}
