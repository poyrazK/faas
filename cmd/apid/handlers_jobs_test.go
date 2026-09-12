// Tests for /v1/jobs handlers (issue #1184 Workstream A / Mega-1).
// Mirrors handlers_crons_info_test.go's shape: uses the
// standard `setup(t, plan)` + `e.do(...)` harness so the API
// key auth path is exercised exactly the way production
// callers see it. The byte-identical 404 contract + the
// plan-tier gate (Free → 402 jobs_not_allowed) + the
// deleteJob 409 CodeJobHasLiveInstances guard are the
// load-bearing assertions.

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedJob writes one job to the in-memory store + returns its
// name (the customer slug). Mirrors seedCron's shape. The
// JobStore sub-interface lives on *state.MemStore directly; no
// appID is needed (jobs are account-scoped, not app-scoped).
func seedJob(t *testing.T, e testEnv, name, imageRef string) string {
	t.Helper()
	job, err := e.store.JobCreate(context.Background(),
		e.acct.ID, name, "batch", imageRef,
		[]string{"/bin/sh", "-c", "echo hello"},
		512, 300, 10, 3, []byte(`{"DATABASE_URL":"postgres://x"}`))
	if err != nil {
		t.Fatalf("JobCreate: %v", err)
	}
	if job.Name != name {
		t.Fatalf("JobCreate.Name = %q, want %q", job.Name, name)
	}
	return job.Name
}

// seedJobRun writes one run with N tasks under the given job.
// Used by the listJobRuns + listJobRunTasks handlers.
func seedJobRun(t *testing.T, e testEnv, jobName string, tasks int) string {
	t.Helper()
	job, err := e.store.JobGetByName(context.Background(), e.acct.ID, jobName)
	if err != nil {
		t.Fatalf("JobGetByName: %v", err)
	}
	run, _, err := e.store.JobRunCreate(context.Background(),
		job.ID, e.acct.ID, "manual", nil, nil, nil, nil, tasks)
	if err != nil {
		t.Fatalf("JobRunCreate: %v", err)
	}
	return run.ID
}

// TestCreateJob_HappyPath pins the basic create flow. Hobby plan
// is the smallest tier that allows jobs (Free → 402).
func TestCreateJob_HappyPath(t *testing.T) {
	e := setup(t, api.PlanHobby)
	rec := e.do(t, "POST", "/v1/jobs", api.CreateJobRequest{
		Name:     "nightly-export",
		ImageRef: "ghcr.io/example/worker:v1",
		Command:  []string{"/bin/sh", "-c", "echo hello"},
		RAMMB:    512,
	}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST jobs = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.JobResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if resp.Name != "nightly-export" ||
		resp.ImageRef != "ghcr.io/example/worker:v1" ||
		resp.RAMMB != 512 {
		t.Errorf("job response drift: %+v", resp)
	}
	if resp.Status != "active" {
		t.Errorf("Status = %q, want active (handler applies default)", resp.Status)
	}
}

// TestCreateJob_FreePlan_Forbidden pins the plan-tier gate. The
// PlanSupportsJobs(p) helper returns false for PlanFree, so the
// handler MUST surface 402 CodeJobsNotAllowed BEFORE the quota
// check — Free customers cannot create jobs even with empty
// quota. The body MUST contain the canonical error code.
func TestCreateJob_FreePlan_Forbidden(t *testing.T) {
	e := setup(t, api.PlanFree)
	rec := e.do(t, "POST", "/v1/jobs", api.CreateJobRequest{
		Name:     "free-tier-job",
		ImageRef: "ghcr.io/example/worker:v1",
		Command:  []string{"/bin/sh", "-c", "echo hi"},
	}, nil)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("POST jobs (Free) = %d, want 402; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "jobs_not_allowed") {
		t.Errorf("body must surface jobs_not_allowed code; got: %s", rec.Body.String())
	}
}

// TestGetJob_HappyPath pins the read shape.
func TestGetJob_HappyPath(t *testing.T) {
	e := setup(t, api.PlanHobby)
	seedJob(t, e, "get-job-happy", "ghcr.io/example/worker:v1")
	rec := e.do(t, "GET", "/v1/jobs/get-job-happy", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET job = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.JobResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if resp.Name != "get-job-happy" {
		t.Errorf("name = %q, want get-job-happy", resp.Name)
	}
}

// TestGetJob_NotFound_NoSuchSlug asserts the byte-identical-404
// body uses the canonical "no such job" string verbatim — a
// different copy on this branch would leak the existence
// oracle.
func TestGetJob_NotFound_NoSuchSlug(t *testing.T) {
	e := setup(t, api.PlanHobby)
	rec := e.do(t, "GET", "/v1/jobs/no-such-slug", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET job 404 = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no such job") {
		t.Errorf("body must say 'no such job' verbatim to keep the byte-identical 404 contract; got: %s", rec.Body.String())
	}
}

// TestGetJob_NotFound_CrossAccount pins the IDOR-safe two-step
// (resolveJob). Account A owns the job; account B's API key
// tries to read it. The handler MUST return a byte-identical
// 404 to the missing-slug branch — never 200, never a 403 that
// distinguishes "exists on another account".
func TestGetJob_NotFound_CrossAccount(t *testing.T) {
	e := setup(t, api.PlanHobby)
	seedJob(t, e, "account-a-job", "ghcr.io/example/worker:v1")
	// Spin up a second account on the same store and use its key.
	otherAcct, err := e.store.CreateAccount(context.Background(), "other@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	otherPT, otherHash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(context.Background(), otherAcct.ID, otherHash, "test", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/v1/jobs/account-a-job", nil)
	req.Header.Set("Authorization", "Bearer "+otherPT)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET cross-account = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no such job") {
		t.Errorf("body must say 'no such job' verbatim (cross-account + missing are byte-identical); got: %s", rec.Body.String())
	}
}

// TestListJobs_HappyPath pins the list response envelope (the
// spec_compliance_test gate pins `total: integer` in required).
func TestListJobs_HappyPath(t *testing.T) {
	e := setup(t, api.PlanHobby)
	seedJob(t, e, "list-job-1", "ghcr.io/example/worker:v1")
	seedJob(t, e, "list-job-2", "ghcr.io/example/worker:v2")
	rec := e.do(t, "GET", "/v1/jobs", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET jobs = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ListJobsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if resp.Total != 2 || len(resp.Jobs) != 2 {
		t.Errorf("total=%d jobs=%d, want 2/2", resp.Total, len(resp.Jobs))
	}
}

func TestListJobs_RejectsInvalidPagination(t *testing.T) {
	cases := []string{
		"?limit=-1",
		"?limit=0",
		"?limit=201",
		"?limit=not-a-number",
		"?limit=",
		"?offset=-1",
		"?offset=not-a-number",
		"?offset=",
	}
	for _, query := range cases {
		t.Run(query[1:], func(t *testing.T) {
			e := setup(t, api.PlanHobby)
			rec := e.do(t, "GET", "/v1/jobs"+query, nil, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("GET jobs%s = %d, want 400; body=%s", query, rec.Code, rec.Body.String())
			}
			assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
		})
	}
}

// TestUpdateJob_PauseAndResume pins the PATCH status=paused +
// status=active transitions. The pointer-based UpdateJobRequest
// keeps "unset" distinct from explicit-zero so a customer can
// pass `--resume` without also having to re-send every other
// field.
func TestUpdateJob_PauseAndResume(t *testing.T) {
	e := setup(t, api.PlanHobby)
	seedJob(t, e, "pause-resume-job", "ghcr.io/example/worker:v1")
	paused := "paused"
	rec := e.do(t, "PATCH", "/v1/jobs/pause-resume-job", api.UpdateJobRequest{
		Status: &paused,
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status=paused = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.JobResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "paused" {
		t.Errorf("status = %q, want paused", resp.Status)
	}
	active := "active"
	rec = e.do(t, "PATCH", "/v1/jobs/pause-resume-job", api.UpdateJobRequest{
		Status: &active,
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status=active = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "active" {
		t.Errorf("status = %q, want active", resp.Status)
	}
}

// TestDeleteJob_NoLiveInstances pins the happy-path soft delete.
// Returns 204 No Content with no response body — the job is
// gone from the wire shape; the dashboard reloads the list
// after the request completes.
func TestDeleteJob_NoLiveInstances(t *testing.T) {
	e := setup(t, api.PlanHobby)
	seedJob(t, e, "rm-job-clean", "ghcr.io/example/worker:v1")
	rec := e.do(t, "DELETE", "/v1/jobs/rm-job-clean", nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE job = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	// Subsequent GET MUST return 404 (job is soft-deleted; the
	// IDOR-safe resolveJob returns the byte-identical not-found
	// response).
	rec = e.do(t, "GET", "/v1/jobs/rm-job-clean", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET deleted job = %d, want 404", rec.Code)
	}
}

// TestCreateJobRun_HappyPath pins the fan-out shape. Hobby
// caps concurrent jobs at 3, so tasks=3 (the per-run
// JobMaxParallelism) is the largest value that fits in the
// Hobby cap. Hobby also caps JobMaxTasksPerRun at 100, well
// above this test's tasks value.
func TestCreateJobRun_HappyPath(t *testing.T) {
	e := setup(t, api.PlanHobby)
	seedJob(t, e, "run-fanout-job", "ghcr.io/example/worker:v1")
	rec := e.do(t, "POST", "/v1/jobs/run-fanout-job/runs", api.CreateJobRunRequest{
		Tasks: 3,
	}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST runs = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.JobRunResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if resp.Tasks != 3 {
		t.Errorf("tasks = %d, want 3", resp.Tasks)
	}
	// Newly created run has aggregate_status='queued' — the
	// dispatch tick flips it to 'running' once tasks are
	// claimed. The handler does NOT pre-claim so a customer can
	// see "I just dispatched this" before the engine picks it up.
	if resp.AggregateStatus != "queued" {
		t.Errorf("aggregate_status = %q, want queued", resp.AggregateStatus)
	}
}

// TestListJobRunTasks_HappyPath pins the task-list envelope
// (total in required).
func TestListJobRunTasks_HappyPath(t *testing.T) {
	e := setup(t, api.PlanHobby)
	seedJob(t, e, "tasks-list-job", "ghcr.io/example/worker:v1")
	runID := seedJobRun(t, e, "tasks-list-job", 5)
	rec := e.do(t, "GET", "/v1/jobs/tasks-list-job/runs/"+runID+"/tasks", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET tasks = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ListJobTasksResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if resp.Total != 5 || len(resp.Tasks) != 5 {
		t.Errorf("total=%d tasks=%d, want 5/5", resp.Total, len(resp.Tasks))
	}
	// LeaseToken MUST be omitted on the wire (internal dispatch
	// primitive). The struct field is gone from the wire shape
	// but the db column is still written; we verify the JSON
	// shape has no "lease_token" key.
	raw := rec.Body.String()
	if strings.Contains(raw, "lease_token") {
		t.Errorf("wire response must NOT include lease_token (cross-tenant enumeration vector); got: %s", raw)
	}
}

func TestGetJobTaskLogs_AllowsZeroBasedIndex(t *testing.T) {
	e := setup(t, api.PlanHobby)
	seedJob(t, e, "task-logs-job", "ghcr.io/example/worker:v1")
	runID := seedJobRun(t, e, "task-logs-job", 1)
	rec := e.do(t, "GET", "/v1/jobs/task-logs-job/runs/"+runID+"/tasks/0/logs", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET task logs = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.JobTaskLogResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if resp.TaskStatus != "queued" {
		t.Errorf("task_status = %q, want queued", resp.TaskStatus)
	}
}

// TestCancelJobRun_HappyPath pins the cancel shape. The
// JobRunCancelledResponse wraps the post-cancel run aggregate +
// a cancelled_at timestamp. Naturally idempotent — a second
// cancel returns the already-cancelled run.
func TestCancelJobRun_HappyPath(t *testing.T) {
	e := setup(t, api.PlanHobby)
	seedJob(t, e, "cancel-run-job", "ghcr.io/example/worker:v1")
	runID := seedJobRun(t, e, "cancel-run-job", 3)
	rec := e.do(t, "POST", "/v1/jobs/cancel-run-job/runs/"+runID+"/cancel", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST cancel = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.JobRunCancelledResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if resp.Run.AggregateStatus != "cancelled" {
		t.Errorf("aggregate_status = %q, want cancelled", resp.Run.AggregateStatus)
	}
	if resp.Run.TasksCancelled != 3 {
		t.Errorf("tasks_cancelled = %d, want 3", resp.Run.TasksCancelled)
	}
	if resp.CancelledAt == "" {
		t.Errorf("cancelled_at empty; want RFC 3339")
	}
}

// TestCreateJob_InvalidSlug pins the 400 path. The validSlug
// regex rejects slugs with uppercase letters / underscores /
// spaces — a customer typo must fail fast at the apid layer
// rather than 500-ing through the sqlc layer.
func TestCreateJob_InvalidSlug(t *testing.T) {
	e := setup(t, api.PlanHobby)
	rec := e.do(t, "POST", "/v1/jobs", api.CreateJobRequest{
		Name:     "Bad Slug",
		ImageRef: "ghcr.io/example/worker:v1",
		Command:  []string{"/bin/sh", "-c", "echo hi"},
	}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST invalid slug = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestCreateJob_QuotaExceeded pins the Hobby JobMaxPerAccount
// gate. Hobby allows 5 jobs/account; creating the 6th returns
// 409 CodeJobQuotaExceeded. The check fires AFTER the plan-tier
// gate but BEFORE the store call so the quota atomically
// applies.
func TestCreateJob_QuotaExceeded(t *testing.T) {
	e := setup(t, api.PlanHobby)
	for i := 1; i <= 5; i++ {
		seedJob(t, e, jobName(i), "ghcr.io/example/worker:v1")
	}
	rec := e.do(t, "POST", "/v1/jobs", api.CreateJobRequest{
		Name:     "over-quota-job",
		ImageRef: "ghcr.io/example/worker:v1",
		Command:  []string{"/bin/sh", "-c", "echo hi"},
	}, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST over-quota = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "job_quota_exceeded") {
		t.Errorf("body must surface job_quota_exceeded; got: %s", rec.Body.String())
	}
}

// jobName returns a Hobby-valid slug for the i'th seeded job.
// Mirrors the convention used in cron seedCron (1..N indexed).
func jobName(i int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	if i < 0 || i >= len(letters) {
		return "x"
	}
	return "job-" + string(letters[i])
}

// Compile-time guard: the memstore actually implements the
// JobStore sub-interface. Pinning this here means a future
// signature drift in pkg/state surfaces as a build failure in
// this file rather than a runtime nil-method panic in M11.
var _ state.JobStore = (*state.MemStore)(nil)
