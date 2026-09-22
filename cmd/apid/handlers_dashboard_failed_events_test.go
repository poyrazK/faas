package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

type dashboardFailedEventsTestEnv struct {
	h        http.Handler
	store    *state.MemStore
	sessions *session.Manager
	account  state.Account
	session  *http.Cookie
	ops      *wire.OpsMetrics
}

func newDashboardFailedEventsTestEnv(t *testing.T) dashboardFailedEventsTestEnv {
	t.Helper()
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "failed-events@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	sessions, err := session.NewEphemeralManager(sessionCookieLifetime)
	if err != nil {
		t.Fatalf("session manager: %v", err)
	}
	sessionToken, err := sessions.Issue(account.ID)
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	ops := wire.NewOpsMetrics("apid_failed_events_dashboard_test")
	srv := newServerWithDeps(
		store,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		"gregale.dev",
		noopNotifier{},
		"",
		noopMailer{},
		stubGithubdClient{},
		sessions,
		nil,
		15*60_000_000_000,
		"",
	).WithOpsMetrics(context.Background(), ops)
	return dashboardFailedEventsTestEnv{
		h: srv.handler(), store: store, sessions: sessions, account: account,
		session: &http.Cookie{Name: sessionCookie, Value: sessionToken}, ops: ops,
	}
}

func seedDashboardFailedInvocation(t *testing.T, store *state.MemStore, accountID, appID string) (state.Invocation, string) {
	t.Helper()
	inv, err := store.EnqueueInvocation(t.Context(), state.Invocation{
		AccountID: accountID,
		AppID:     appID,
		Source:    state.InvocationQueue,
		State:     state.InvocationDeadLetter,
		DueAt:     time.Now().UTC(),
		Attempts:  3,
		LastError: "worker failed",
		Payload:   json.RawMessage(`{"event":"failed"}`),
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation: %v", err)
	}
	return inv, dashboardDeadLetterEventID("invocation", inv.ID)
}

func dashboardDeadLetterEventID(source, sourceID string) string {
	return uuid.NewSHA1(uuid.Nil, []byte(source+":"+sourceID)).String()
}

func dashboardFailedEventsCSRF(t *testing.T, env dashboardFailedEventsTestEnv) *http.Cookie {
	t.Helper()
	token, err := middleware.IssueForAuthenticatedNamed(
		env.sessions,
		dashboardFailedEventsAction,
		env.account.ID,
		dashboardFailedEventsCSRFCookie,
	)
	if err != nil {
		t.Fatalf("IssueForAuthenticatedNamed: %v", err)
	}
	return &http.Cookie{Name: dashboardFailedEventsCSRFCookie, Value: token}
}

func dashboardFailedEventsAudit(t *testing.T, store *state.MemStore, accountID, kind, eventID string) map[string]any {
	t.Helper()
	rows, err := store.ListEvents(t.Context(), accountID, 100)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	for _, row := range rows {
		if row.Kind != kind {
			continue
		}
		var data map[string]any
		if err := json.Unmarshal(row.Data, &data); err != nil {
			t.Fatalf("decode %s data: %v", kind, err)
		}
		if got, _ := data["event_id"].(string); got == eventID {
			return data
		}
	}
	t.Fatalf("missing %s audit row for event %s", kind, eventID)
	return nil
}

func dashboardFailedEventsBatchAudit(t *testing.T, store *state.MemStore, accountID, kind string, wantCount int) map[string]any {
	t.Helper()
	rows, err := store.ListEvents(t.Context(), accountID, 100)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	for _, row := range rows {
		if row.Kind != kind {
			continue
		}
		var data map[string]any
		if err := json.Unmarshal(row.Data, &data); err != nil {
			t.Fatalf("decode %s data: %v", kind, err)
		}
		if data["operation"] != "batch" {
			continue
		}
		if got, _ := data["count"].(float64); int(got) == wantCount {
			return data
		}
	}
	t.Fatalf("missing %s batch audit row with count %d", kind, wantCount)
	return nil
}

func TestDashboardFailedEvents_ReplayIsCSRFProtectedScopedAndObserved(t *testing.T) {
	env := newDashboardFailedEventsTestEnv(t)
	app, err := env.store.CreateApp(t.Context(), state.App{
		AccountID: env.account.ID, Slug: "dashboard-replay", Type: state.AppTypeApp,
		Runtime: "node22", Status: state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if _, err := env.store.CreateApp(t.Context(), state.App{
		AccountID: env.account.ID, Slug: "dashboard-other", Type: state.AppTypeApp,
		Runtime: "node22", Status: state.AppActive,
	}); err != nil {
		t.Fatalf("CreateApp other: %v", err)
	}
	inv, eventID := seedDashboardFailedInvocation(t, env.store, env.account.ID, app.ID)

	missingCSRF := dashboardPOST(t, env.h, env.session,
		"/dashboard/failed-events/dashboard-replay/"+eventID+"/replay", nil)
	if missingCSRF.Code != http.StatusBadRequest {
		t.Fatalf("missing csrf status = %d, want 400", missingCSRF.Code)
	}

	csrf := dashboardFailedEventsCSRF(t, env)
	wrongApp := dashboardPOST(t, env.h, env.session,
		"/dashboard/failed-events/dashboard-other/"+eventID+"/replay",
		map[string]string{middleware.FormFieldName: csrf.Value}, csrf)
	if wrongApp.Code != http.StatusSeeOther || !strings.Contains(wrongApp.Header().Get("Location"), "action=error") {
		t.Fatalf("wrong app response = %d/%q, want 303 error redirect", wrongApp.Code, wrongApp.Header().Get("Location"))
	}
	stillDead, err := env.store.InvocationByID(t.Context(), inv.ID)
	if err != nil {
		t.Fatalf("InvocationByID after wrong app: %v", err)
	}
	if stillDead.State != state.InvocationDeadLetter {
		t.Fatalf("wrong-app replay changed state to %q", stillDead.State)
	}
	wrongAppMetrics := scrapeOpsMetrics(t, env.ops)
	if !strings.Contains(wrongAppMetrics, `faas_dlq_replayed_total{app="dashboard-other",status="not_found"} 1`) {
		t.Fatalf("missing scoped not-found replay metric:\n%s", wrongAppMetrics)
	}

	replayed := dashboardPOST(t, env.h, env.session,
		"/dashboard/failed-events/dashboard-replay/"+eventID+"/replay",
		map[string]string{middleware.FormFieldName: csrf.Value}, csrf)
	if replayed.Code != http.StatusSeeOther {
		t.Fatalf("replay status = %d, want 303; body=%s", replayed.Code, replayed.Body.String())
	}
	if loc := replayed.Header().Get("Location"); !strings.Contains(loc, "action=replayed") {
		t.Fatalf("Location = %q, want replay flash", loc)
	}
	reset, err := env.store.InvocationByID(t.Context(), inv.ID)
	if err != nil {
		t.Fatalf("InvocationByID after replay: %v", err)
	}
	if reset.State != state.InvocationPending || reset.Attempts != 0 {
		t.Fatalf("replayed invocation = state %q attempts %d, want pending/0", reset.State, reset.Attempts)
	}
	data := dashboardFailedEventsAudit(t, env.store, env.account.ID, "app.dlq.event_replayed", eventID)
	if data["surface"] != "dashboard" || data["source"] != "invocation" {
		t.Fatalf("replay audit data = %+v, want dashboard surface and invocation source", data)
	}
	metrics := scrapeOpsMetrics(t, env.ops)
	if !strings.Contains(metrics, `faas_dlq_replayed_total{app="dashboard-replay",status="success"} 1`) {
		t.Fatalf("missing dashboard replay metric:\n%s", metrics)
	}
}

func TestDashboardFailedEvents_DiscardIsAuditedAndObserved(t *testing.T) {
	env := newDashboardFailedEventsTestEnv(t)
	app, err := env.store.CreateApp(t.Context(), state.App{
		AccountID: env.account.ID, Slug: "dashboard-discard", Type: state.AppTypeApp,
		Runtime: "node22", Status: state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	inv, eventID := seedDashboardFailedInvocation(t, env.store, env.account.ID, app.ID)
	csrf := dashboardFailedEventsCSRF(t, env)

	discarded := dashboardPOST(t, env.h, env.session,
		"/dashboard/failed-events/dashboard-discard/"+eventID+"/discard",
		map[string]string{middleware.FormFieldName: csrf.Value}, csrf)
	if discarded.Code != http.StatusSeeOther {
		t.Fatalf("discard status = %d, want 303; body=%s", discarded.Code, discarded.Body.String())
	}
	if loc := discarded.Header().Get("Location"); !strings.Contains(loc, "action=discarded") {
		t.Fatalf("Location = %q, want discard flash", loc)
	}
	if _, err := env.store.DeadLetterEventByID(t.Context(), app.ID, eventID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("discarded event lookup error = %v, want ErrNotFound", err)
	}
	stillDead, err := env.store.InvocationByID(t.Context(), inv.ID)
	if err != nil {
		t.Fatalf("InvocationByID after discard: %v", err)
	}
	if stillDead.State != state.InvocationDeadLetter {
		t.Fatalf("discard changed source state to %q", stillDead.State)
	}
	dashboardFailedEventsAudit(t, env.store, env.account.ID, "app.dlq.purged", eventID)
	metrics := scrapeOpsMetrics(t, env.ops)
	if !strings.Contains(metrics, `faas_dlq_purged_total{app="dashboard-discard",status="success"} 1`) {
		t.Fatalf("missing dashboard purge metric:\n%s", metrics)
	}
}

func TestDashboardFailedEvents_AccountJobReplayIsAuditedAndObserved(t *testing.T) {
	env := newDashboardFailedEventsTestEnv(t)
	job, err := env.store.JobCreate(t.Context(), env.account.ID, "dashboard-job", "batch",
		"oci://example/jobs@sha256:abc", []string{"/bin/run"}, 256, 90, 1, 3, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("JobCreate: %v", err)
	}
	run, _, err := env.store.JobRunCreate(t.Context(), job.ID, env.account.ID, "manual", nil, nil, nil, nil, 1)
	if err != nil {
		t.Fatalf("JobRunCreate: %v", err)
	}
	if err := env.store.JobTaskMarkTerminal(t.Context(), run.ID, 0, "failed", 1, "worker", "boom", time.Now().UTC()); err != nil {
		t.Fatalf("JobTaskMarkTerminal: %v", err)
	}
	if err := env.store.JobRunIncrementDeadLetter(t.Context(), run.ID); err != nil {
		t.Fatalf("JobRunIncrementDeadLetter: %v", err)
	}
	run, err = env.store.JobRunRecompute(t.Context(), run.ID)
	if err != nil {
		t.Fatalf("JobRunRecompute: %v", err)
	}
	if run.AggregateStatus != "dead_letter" {
		t.Fatalf("job run status = %q, want dead_letter", run.AggregateStatus)
	}
	eventID := dashboardDeadLetterEventID("job_run", run.ID)
	csrf := dashboardFailedEventsCSRF(t, env)

	replayed := dashboardPOST(t, env.h, env.session,
		"/dashboard/failed-events/account/"+eventID+"/replay",
		map[string]string{middleware.FormFieldName: csrf.Value}, csrf)
	if replayed.Code != http.StatusSeeOther {
		t.Fatalf("account replay status = %d, want 303; body=%s", replayed.Code, replayed.Body.String())
	}
	if loc := replayed.Header().Get("Location"); !strings.Contains(loc, "action=replayed") {
		t.Fatalf("Location = %q, want replay flash", loc)
	}
	run, err = env.store.JobRunGetByID(t.Context(), run.ID)
	if err != nil {
		t.Fatalf("JobRunGetByID after replay: %v", err)
	}
	if run.AggregateStatus != "running" || run.DeadLetterCount != 0 {
		t.Fatalf("job run after replay = status %q dead_letters %d, want running/0", run.AggregateStatus, run.DeadLetterCount)
	}
	data := dashboardFailedEventsAudit(t, env.store, env.account.ID, "account.dlq.event_replayed", eventID)
	if data["surface"] != "dashboard" || data["account_scope"] != true || data["source"] != "job_run" {
		t.Fatalf("account replay audit data = %+v, want dashboard/account/job_run", data)
	}
	metrics := scrapeOpsMetrics(t, env.ops)
	if !strings.Contains(metrics, `faas_dlq_replayed_total{app="account",status="success"} 1`) {
		t.Fatalf("missing account replay metric:\n%s", metrics)
	}
}

func TestDashboardFailedEvents_BulkReplayIsScopedAndAudited(t *testing.T) {
	env := newDashboardFailedEventsTestEnv(t)
	app, err := env.store.CreateApp(t.Context(), state.App{
		AccountID: env.account.ID, Slug: "dashboard-bulk-replay", Type: state.AppTypeApp,
		Runtime: "node22", Status: state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	other, err := env.store.CreateApp(t.Context(), state.App{
		AccountID: env.account.ID, Slug: "dashboard-bulk-other", Type: state.AppTypeApp,
		Runtime: "node22", Status: state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp other: %v", err)
	}
	invocations := make([]state.Invocation, 0, 3)
	for i := 0; i < 2; i++ {
		inv, _ := seedDashboardFailedInvocation(t, env.store, env.account.ID, app.ID)
		invocations = append(invocations, inv)
	}
	otherInv, _ := seedDashboardFailedInvocation(t, env.store, env.account.ID, other.ID)
	invocations = append(invocations, otherInv)

	csrf := dashboardFailedEventsCSRF(t, env)
	replayed := dashboardPOST(t, env.h, env.session, "/dashboard/failed-events/replay-all", map[string]string{
		middleware.FormFieldName: csrf.Value,
		"app":                    app.Slug,
	}, csrf)
	if replayed.Code != http.StatusSeeOther {
		t.Fatalf("bulk replay status = %d, want 303; body=%s", replayed.Code, replayed.Body.String())
	}
	if loc := replayed.Header().Get("Location"); !strings.Contains(loc, "action=replayed") || !strings.Contains(loc, "count=2") {
		t.Fatalf("bulk replay Location = %q, want replay/count flash", loc)
	}
	for i, inv := range invocations {
		got, err := env.store.InvocationByID(t.Context(), inv.ID)
		if err != nil {
			t.Fatalf("InvocationByID[%d]: %v", i, err)
		}
		if i < 2 && (got.State != state.InvocationPending || got.Attempts != 0) {
			t.Fatalf("selected invocation[%d] = state %q attempts %d, want pending/0", i, got.State, got.Attempts)
		}
		if i == 2 && got.State != state.InvocationDeadLetter {
			t.Fatalf("other invocation changed state to %q", got.State)
		}
	}
	audit := dashboardFailedEventsBatchAudit(t, env.store, env.account.ID, "app.dlq.event_replayed", 2)
	if audit["surface"] != "dashboard" || audit["app_id"] != app.ID {
		t.Fatalf("bulk replay audit = %+v, want dashboard/app scope", audit)
	}
	metrics := scrapeOpsMetrics(t, env.ops)
	if !strings.Contains(metrics, `faas_dlq_replayed_total{app="dashboard-bulk-replay",status="success"} 2`) {
		t.Fatalf("missing bulk replay metric:\n%s", metrics)
	}
}

func TestDashboardFailedEvents_BulkDiscardLeavesSourcesDeadLettered(t *testing.T) {
	env := newDashboardFailedEventsTestEnv(t)
	app, err := env.store.CreateApp(t.Context(), state.App{
		AccountID: env.account.ID, Slug: "dashboard-bulk-discard", Type: state.AppTypeApp,
		Runtime: "node22", Status: state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	invocations := make([]state.Invocation, 0, 2)
	for i := 0; i < 2; i++ {
		inv, _ := seedDashboardFailedInvocation(t, env.store, env.account.ID, app.ID)
		invocations = append(invocations, inv)
	}
	csrf := dashboardFailedEventsCSRF(t, env)
	discarded := dashboardPOST(t, env.h, env.session, "/dashboard/failed-events/discard-all", map[string]string{
		middleware.FormFieldName: csrf.Value,
		"app":                    app.Slug,
	}, csrf)
	if discarded.Code != http.StatusSeeOther {
		t.Fatalf("bulk discard status = %d, want 303; body=%s", discarded.Code, discarded.Body.String())
	}
	if loc := discarded.Header().Get("Location"); !strings.Contains(loc, "action=discarded") || !strings.Contains(loc, "count=2") {
		t.Fatalf("bulk discard Location = %q, want discard/count flash", loc)
	}
	for i, inv := range invocations {
		got, err := env.store.InvocationByID(t.Context(), inv.ID)
		if err != nil {
			t.Fatalf("InvocationByID[%d]: %v", i, err)
		}
		if got.State != state.InvocationDeadLetter {
			t.Fatalf("discarded invocation[%d] state = %q, want dead_letter", i, got.State)
		}
	}
	if events, err := env.store.ListDeadLetterEvents(t.Context(), app.ID, 10, ""); err != nil || len(events) != 0 {
		t.Fatalf("remaining app dead-letter events = %d, err=%v; want 0", len(events), err)
	}
	audit := dashboardFailedEventsBatchAudit(t, env.store, env.account.ID, "app.dlq.purged", 2)
	if audit["surface"] != "dashboard" || audit["app_id"] != app.ID {
		t.Fatalf("bulk discard audit = %+v, want dashboard/app scope", audit)
	}
	metrics := scrapeOpsMetrics(t, env.ops)
	if !strings.Contains(metrics, `faas_dlq_purged_total{app="dashboard-bulk-discard",status="success"} 2`) {
		t.Fatalf("missing bulk discard metric:\n%s", metrics)
	}
}

func TestDashboardFailedEvents_PaginatesAtPageSize(t *testing.T) {
	env := newDashboardFailedEventsTestEnv(t)
	app, err := env.store.CreateApp(t.Context(), state.App{
		AccountID: env.account.ID, Slug: "dashboard-pagination", Type: state.AppTypeApp,
		Runtime: "node22", Status: state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	for i := 0; i < dashboardFailedEventsPageSize+1; i++ {
		seedDashboardFailedInvocation(t, env.store, env.account.ID, app.ID)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/failed-events?app="+app.Slug, nil)
	req.AddCookie(env.session)
	env.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET failed events status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Older events") || !strings.Contains(body, "before=") {
		t.Fatalf("paginated page missing older-events link:\n%s", body)
	}
	if rows := strings.Count(body, "<tr>"); rows != dashboardFailedEventsPageSize+1 {
		t.Fatalf("rendered table rows = %d, want %d including header", rows, dashboardFailedEventsPageSize+1)
	}
}
