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
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func TestDashboardScheduledJobPolicyAndOccurrenceHistory(t *testing.T) {
	h, sessionCookie, store, sessions := newAuthedDashboardServerFullFull(t, "hobby", "alice@example.com")
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	job, err := store.JobCreateScheduledIfUnderQuota(t.Context(), state.Job{
		AccountID: acct.ID, Name: "nightly-sync", Kind: "recurring", ImageRef: "oci://example/sync@sha256:abc",
		Command: []string{"/bin/sync"}, RAMMB: 256, TaskTimeoutS: 90, MaxParallelism: 4, RetryMax: 2,
		CronSchedule: "*/5 * * * *", CronTimezone: "UTC",
		SchedulePolicy: &workpolicy.SchedulePolicy{Version: workpolicy.Version, Overlap: "allow", MissedRuns: "skip"},
	}, 10)
	if err != nil {
		t.Fatalf("JobCreateScheduledIfUnderQuota: %v", err)
	}
	firedAt := time.Now().UTC()
	_, created, err := store.JobRunCreateScheduledOccurrence(t.Context(), job.ID, job.CronSchedule, job.CronTimezone, nil, firedAt,
		state.JobScheduledOccurrenceOptions{ScheduledFor: firedAt.Add(-5 * time.Minute), ScheduleRevision: job.ScheduleRevision, Disposition: "coalesced", Reason: "scheduler recovered after downtime"})
	if err != nil || created {
		t.Fatalf("record coalesced occurrence: created=%t err=%v", created, err)
	}

	page := dashboardGET(t, h, sessionCookie, "/dashboard/jobs")
	if page.Code != http.StatusOK {
		t.Fatalf("GET /dashboard/jobs = %d: %s", page.Code, page.Body.String())
	}
	for _, want := range []string{"nightly-sync", "coalesced", "scheduler recovered after downtime", "name=\"overlap\"", "failure_rules_json"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("jobs page missing %q", want)
		}
	}

	token, err := middleware.IssueForAuthenticatedNamed(sessions, dashboardJobSchedulePolicyAction, acct.ID, dashboardJobSchedulePolicyCookie)
	if err != nil {
		t.Fatalf("IssueForAuthenticatedNamed: %v", err)
	}
	form := map[string]string{
		middleware.FormFieldName: token, "overlap": "skip", "start_deadline_seconds": "120", "missed_runs": "coalesce_latest",
		"failure_rules_json": `{"version":1,"rules":[{"exit_codes":[65],"action":"fail_partition"}],"unmatched_failure":"retry","uncertain_outcome":"hold"}`,
	}
	response := dashboardPOST(t, h, sessionCookie, "/dashboard/jobs/nightly-sync/policy", form,
		&http.Cookie{Name: dashboardJobSchedulePolicyCookie, Value: token})
	if response.Code != http.StatusSeeOther || !strings.Contains(response.Header().Get("Location"), "scheduled_work=updated") {
		t.Fatalf("POST policy = %d, location %q, body=%s", response.Code, response.Header().Get("Location"), response.Body.String())
	}
	updated, err := store.JobGetByName(t.Context(), acct.ID, job.Name)
	if err != nil {
		t.Fatalf("JobGetByName after update: %v", err)
	}
	if updated.SchedulePolicy == nil || updated.SchedulePolicy.Overlap != "skip" || updated.SchedulePolicy.StartDeadlineSeconds != 120 || updated.SchedulePolicy.MissedRuns != "coalesce_latest" {
		t.Fatalf("updated schedule policy = %+v", updated.SchedulePolicy)
	}
	if updated.FailureRules == nil || len(updated.FailureRules.Rules) != 1 || updated.FailureRules.Rules[0].ExitCodes[0] != 65 {
		t.Fatalf("updated failure rules = %+v", updated.FailureRules)
	}

	failedJob, err := store.JobCreate(t.Context(), acct.ID, "replay-sync", "batch", "oci://example/sync@sha256:abc", []string{"/bin/sync"}, 256, 90, 1, 2, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("JobCreate replay job: %v", err)
	}
	if _, err := store.JobSetImageMaterialization(t.Context(), failedJob.ID, failedJob.ImageRef, "ready", "sha256:"+strings.Repeat("a", 64), "jobs/replay-sync.ext4", ""); err != nil {
		t.Fatalf("JobSetImageMaterialization: %v", err)
	}
	failedRun, _, err := store.JobRunCreate(t.Context(), failedJob.ID, acct.ID, "manual", nil, nil, nil, json.RawMessage(`{}`), 1)
	if err != nil {
		t.Fatalf("JobRunCreate: %v", err)
	}
	if err := store.JobTaskMarkTerminal(t.Context(), failedRun.ID, 0, "failed", 65, "exit_nonzero", "invalid partition", time.Now().UTC()); err != nil {
		t.Fatalf("JobTaskMarkTerminal: %v", err)
	}
	failedRun, err = store.JobRunRecompute(t.Context(), failedRun.ID)
	if err != nil || failedRun.AggregateStatus != "failed" {
		t.Fatalf("JobRunRecompute = %+v, err=%v", failedRun, err)
	}
	page = dashboardGET(t, h, sessionCookie, "/dashboard/jobs")
	if !strings.Contains(page.Body.String(), "Replay failed partitions") {
		t.Fatalf("jobs page did not expose the failed-partition replay action:\n%s", page.Body.String())
	}
	replayToken, err := middleware.IssueForAuthenticatedNamed(sessions, dashboardReplayFailedAction, acct.ID, dashboardReplayFailedCSRFCookie)
	if err != nil {
		t.Fatalf("IssueForAuthenticatedNamed replay: %v", err)
	}
	replay := dashboardPOST(t, h, sessionCookie, "/dashboard/jobs/replay-sync/runs/"+failedRun.ID+"/replay-failed",
		map[string]string{middleware.FormFieldName: replayToken}, &http.Cookie{Name: dashboardReplayFailedCSRFCookie, Value: replayToken})
	if replay.Code != http.StatusSeeOther || !strings.Contains(replay.Header().Get("Location"), "scheduled_work=replayed") {
		t.Fatalf("POST replay failed partitions = %d, location %q, body=%s", replay.Code, replay.Header().Get("Location"), replay.Body.String())
	}
	replayAgain := dashboardPOST(t, h, sessionCookie, "/dashboard/jobs/replay-sync/runs/"+failedRun.ID+"/replay-failed",
		map[string]string{middleware.FormFieldName: replayToken}, &http.Cookie{Name: dashboardReplayFailedCSRFCookie, Value: replayToken})
	if replayAgain.Code != http.StatusSeeOther || !strings.Contains(replayAgain.Header().Get("Location"), "scheduled_work=replayed") {
		t.Fatalf("duplicate replay = %d, location %q, body=%s", replayAgain.Code, replayAgain.Header().Get("Location"), replayAgain.Body.String())
	}
	runs, err := store.JobRunListByAccount(t.Context(), acct.ID, 20, 0)
	if err != nil {
		t.Fatalf("JobRunListByAccount after replay: %v", err)
	}
	linkedReplayCount := 0
	for _, run := range runs {
		if run.SourceRunID != nil && *run.SourceRunID == failedRun.ID && run.Tasks == 1 {
			linkedReplayCount++
		}
	}
	if linkedReplayCount != 1 {
		t.Fatalf("source run %s has %d linked replay runs, want exactly one: %+v", failedRun.ID, linkedReplayCount, runs)
	}

	foreign, err := store.CreateAccount(t.Context(), "bob@example.com", "hobby")
	if err != nil {
		t.Fatalf("create foreign account: %v", err)
	}
	foreignJob, err := store.JobCreateScheduledIfUnderQuota(t.Context(), state.Job{
		AccountID: foreign.ID, Name: "foreign-sync", Kind: "recurring", ImageRef: "oci://example/sync@sha256:abc",
		CronSchedule: "*/5 * * * *", CronTimezone: "UTC",
	}, 10)
	if err != nil {
		t.Fatalf("create foreign job: %v", err)
	}
	blocked := dashboardPOST(t, h, sessionCookie, "/dashboard/jobs/foreign-sync/policy", form,
		&http.Cookie{Name: dashboardJobSchedulePolicyCookie, Value: token})
	if blocked.Code != http.StatusSeeOther || !strings.Contains(blocked.Header().Get("Location"), "scheduled_work=error") {
		t.Fatalf("cross-account policy update = %d, location %q", blocked.Code, blocked.Header().Get("Location"))
	}
	unchanged, err := store.JobGetByName(t.Context(), foreign.ID, foreignJob.Name)
	if err != nil || unchanged.SchedulePolicy != nil {
		t.Fatalf("foreign job after account-scoped update = %+v, err=%v", unchanged.SchedulePolicy, err)
	}
}

func dashboardGET(t *testing.T, h http.Handler, sid *http.Cookie, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(sid)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

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
	finished := now.Add(time.Second)
	outcome := state.OutcomeDeadLetter
	asyncInvocation, err := store.EnqueueInvocation(t.Context(), state.Invocation{
		AccountID: acct.ID, AppID: app.ID, Source: state.InvocationAsyncInvoke,
		State: state.InvocationDeadLetter, Method: http.MethodPost, Path: "/reports",
		DueAt: now, Attempts: 4, CreatedAt: now, CompletedAt: &finished, Outcome: &outcome,
		Payload: json.RawMessage(`{"customer_secret":"dashboard-must-not-render-this"}`),
	})
	if err != nil {
		t.Fatalf("async dead-letter invocation: %v", err)
	}
	pendingAsyncInvocation, err := store.EnqueueInvocation(t.Context(), state.Invocation{
		AccountID: acct.ID, AppID: app.ID, Source: state.InvocationAsyncInvoke,
		State: state.InvocationPending, Method: http.MethodPost, Path: "/reports/next",
		DueAt: now.Add(time.Second), Attempts: 1, CreatedAt: now.Add(time.Second),
		Payload: json.RawMessage(`{"customer_secret":"another-dashboard-secret"}`),
	})
	if err != nil {
		t.Fatalf("pending async invocation: %v", err)
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
		"Recent async invocations", asyncInvocation.ID, "POST /reports", "dead_letter",
		"gregale invocations get",
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("body missing %q\n%s", want, rec.Body.String())
		}
	}
	for _, private := range []string{"dashboard-must-not-render-this", "another-dashboard-secret"} {
		if strings.Contains(rec.Body.String(), private) {
			t.Errorf("dashboard invocation history leaked request payload marker %q", private)
		}
	}
	olderPageRec := httptest.NewRecorder()
	olderPageReq := httptest.NewRequest(http.MethodGet, "/dashboard/jobs?async_before="+pendingAsyncInvocation.ID, nil)
	olderPageReq.AddCookie(sessionCookie)
	h.ServeHTTP(olderPageRec, olderPageReq)
	if olderPageRec.Code != http.StatusOK || !strings.Contains(olderPageRec.Body.String(), asyncInvocation.ID) || strings.Contains(olderPageRec.Body.String(), pendingAsyncInvocation.ID) {
		t.Fatalf("async history cursor page did not isolate older row %s: status=%d\n%s", asyncInvocation.ID, olderPageRec.Code, olderPageRec.Body.String())
	}

	// The per-app queue alias must not mix in the account-wide invocation
	// history when it is scoped to one application.
	appQueueRec := httptest.NewRecorder()
	appQueueReq := httptest.NewRequest(http.MethodGet, "/dashboard/apps/jobs-app/queues", nil)
	appQueueReq.AddCookie(sessionCookie)
	h.ServeHTTP(appQueueRec, appQueueReq)
	if appQueueRec.Code != http.StatusOK || strings.Contains(appQueueRec.Body.String(), "Recent async invocations") {
		t.Fatalf("per-app queue page unexpectedly rendered account history: status=%d\n%s", appQueueRec.Code, appQueueRec.Body.String())
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
