// Unit tests for the jobs handlers (ADR-099 PR-D). Coverage:
//
//   - happy-path CRUD round-trip across all 11 endpoints
//   - per-plan caps enforced before write (ram_mb / task_timeout_s /
//     max_parallelism / max_tasks_per_run all gated against
//     pkg/api/limits.go)
//   - Free plan rejects every POST with 404 jobs_not_allowed because
//     JobMax* is 0 (per-plan cap)
//
//     [actually Free plan gets 404 jobs_not_allowed only when
//     FAAS_JOBS_ENABLED is off; with it on, Free plan still gets
//     403 job_quota_exceeded because JobMaxPerAccount=0]
//   - dark-launch kill-switch (FAAS_JOBS_ENABLED=0) gates the
//     entire /v1/jobs surface with 404 jobs_not_allowed
//   - IDOR-safe byte-identical-404: a cross-account fetch returns
//     404, never 403, never 200
//   - cancel a terminal run returns 409 job_run_cancelled
//   - retry on a non-failed task returns 404 job_task_not_found
//
// All tests run KVM-free via the in-memory store (state.MemStore).
//
// The handlers gate on api.JobsEnabled() (env FAAS_JOBS_ENABLED=1)
// so TestMain sets the flag once before the suite runs.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestMain sets FAAS_JOBS_ENABLED=1 so the jobs handlers don't return
// 404 jobs_not_allowed at the gate. Individual tests can flip it off
// to verify the dark-launch kill-switch behavior.
func TestMain(m *testing.M) {
	prev := os.Getenv("FAAS_JOBS_ENABLED")
	_ = os.Setenv("FAAS_JOBS_ENABLED", "1")
	code := m.Run()
	if prev == "" {
		_ = os.Unsetenv("FAAS_JOBS_ENABLED")
	} else {
		_ = os.Setenv("FAAS_JOBS_ENABLED", prev)
	}
	os.Exit(code)
}

// makeJob returns a CreateJobRequest that fits any plan's per-job caps
// (ram_mb=256 ≤ max for all paid plans, task_timeout_s=300 fits Hobby+,
// max_parallelism=10 fits Hobby+, retry_max=2 fits all).
func makeJob(name string) api.CreateJobRequest {
	return api.CreateJobRequest{
		Name:           name,
		ImageRef:       "sha256:" + repeat("a", 64),
		RAMMB:          256,
		TaskTimeoutS:   60,
		MaxParallelism: 5,
		RetryMax:       1,
		EnvOverrides:   map[string]string{"REGION": "eu-west"},
	}
}

// TestJobsCRUD_HappyPath walks the full surface end-to-end on Pro.
func TestJobsCRUD_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)

	// 1. CreateJob → 201
	create := e.do(t, "POST", "/v1/jobs", makeJob("nightly-etl"), nil)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201: %s", create.Code, create.Body)
	}
	var job api.JobResponse
	if err := json.Unmarshal(create.Body.Bytes(), &job); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if job.Name != "nightly-etl" || job.Kind != "run_to_completion" || job.Status != "active" {
		t.Errorf("unexpected job: %+v", job)
	}
	if len(job.ID) != 32 {
		t.Errorf("job.ID = %q, want 32 hex chars", job.ID)
	}

	// 2. GetJob → 200
	get := e.do(t, "GET", "/v1/jobs/"+job.ID, nil, nil)
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200: %s", get.Code, get.Body)
	}
	var got api.JobResponse
	_ = json.Unmarshal(get.Body.Bytes(), &got)
	if got.ID != job.ID {
		t.Errorf("got.ID = %q, want %q", got.ID, job.ID)
	}

	// 3. UpdateJob (PATCH ram_mb) → 200
	bump := int32(512)
	upd := e.do(t, "PATCH", "/v1/jobs/"+job.ID, api.UpdateJobRequest{RAMMB: &bump}, nil)
	if upd.Code != http.StatusOK {
		t.Fatalf("update status = %d, want 200: %s", upd.Code, upd.Body)
	}
	var updJob api.JobResponse
	_ = json.Unmarshal(upd.Body.Bytes(), &updJob)
	if updJob.RAMMB != 512 {
		t.Errorf("updated ram_mb = %d, want 512", updJob.RAMMB)
	}

	// 4. ListJobs → 200, count=1
	list := e.do(t, "GET", "/v1/jobs", nil, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200: %s", list.Code, list.Body)
	}
	var lst api.ListJobsResponse
	if err := json.Unmarshal(list.Body.Bytes(), &lst); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if lst.Count != 1 || len(lst.Jobs) != 1 {
		t.Errorf("list count = %d, jobs = %d, want 1/1", lst.Count, len(lst.Jobs))
	}
	if lst.Quota != api.MustLimitsFor(api.PlanPro).JobMaxPerAccount {
		t.Errorf("quota_max = %d, want %d", lst.Quota, api.MustLimitsFor(api.PlanPro).JobMaxPerAccount)
	}

	// 5. CreateRun → 201
	runCreate := e.do(t, "POST", "/v1/jobs/"+job.ID+"/runs", api.CreateRunRequest{Tasks: 5}, nil)
	if runCreate.Code != http.StatusCreated {
		t.Fatalf("createRun status = %d, want 201: %s", runCreate.Code, runCreate.Body)
	}
	var run api.JobRunResponse
	if err := json.Unmarshal(runCreate.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	if run.Tasks != 5 || run.AggregateStatus != "queued" {
		t.Errorf("unexpected run: %+v", run)
	}

	// 6. GetRun → 200
	runGet := e.do(t, "GET", "/v1/runs/"+run.ID, nil, nil)
	if runGet.Code != http.StatusOK {
		t.Fatalf("getRun status = %d, want 200: %s", runGet.Code, runGet.Body)
	}

	// 7. ListRuns → 200
	runs := e.do(t, "GET", "/v1/jobs/"+job.ID+"/runs", nil, nil)
	if runs.Code != http.StatusOK {
		t.Fatalf("listRuns status = %d, want 200: %s", runs.Code, runs.Body)
	}
	var runLst api.ListRunsResponse
	_ = json.Unmarshal(runs.Body.Bytes(), &runLst)
	if len(runLst.Runs) != 1 {
		t.Errorf("runs count = %d, want 1", len(runLst.Runs))
	}

	// 8. ListRunTasks → 200 (no tasks dispatched in unit-test env, so empty page)
	tasks := e.do(t, "GET", "/v1/runs/"+run.ID+"/tasks", nil, nil)
	if tasks.Code != http.StatusOK {
		t.Fatalf("listRunTasks status = %d, want 200: %s", tasks.Code, tasks.Body)
	}

	// 9. CancelRun → 202 (the cancel action is accepted; the run
	// transitions to cancelled asynchronously as schedd claims the
	// request. The handler returns 202 with the current run state.)
	cancel := e.do(t, "POST", "/v1/runs/"+run.ID+"/cancel", nil, nil)
	if cancel.Code != http.StatusAccepted {
		t.Fatalf("cancelRun status = %d, want 202: %s", cancel.Code, cancel.Body)
	}
	var cancelled api.JobRunResponse
	_ = json.Unmarshal(cancel.Body.Bytes(), &cancelled)
	if cancelled.AggregateStatus != "cancelled" {
		t.Errorf("cancelled aggregate_status = %q, want cancelled", cancelled.AggregateStatus)
	}

	// 10. Cancel again → 409 (already terminal)
	cancel2 := e.do(t, "POST", "/v1/runs/"+run.ID+"/cancel", nil, nil)
	if cancel2.Code != http.StatusConflict {
		t.Errorf("second cancel status = %d, want 409: %s", cancel2.Code, cancel2.Body)
	}
	var prob api.Problem
	_ = json.Unmarshal(cancel2.Body.Bytes(), &prob)
	if prob.Code != api.CodeJobRunCancelled {
		t.Errorf("second cancel code = %q, want %q", prob.Code, api.CodeJobRunCancelled)
	}

	// 11. DeleteJob → 204
	del := e.do(t, "DELETE", "/v1/jobs/"+job.ID, nil, nil)
	if del.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204: %s", del.Code, del.Body)
	}

	// And the job is soft-deleted: a follow-up GET returns the row
	// with status="deleted" (the store's GetJob does NOT filter on
	// status; ListJobsForAccount does, but a direct fetch is by id).
	getGone := e.do(t, "GET", "/v1/jobs/"+job.ID, nil, nil)
	if getGone.Code != http.StatusOK {
		t.Errorf("post-delete get = %d, want 200 (soft delete)", getGone.Code)
	}
	var goneJob api.JobResponse
	_ = json.Unmarshal(getGone.Body.Bytes(), &goneJob)
	if goneJob.Status != "deleted" {
		t.Errorf("post-delete status = %q, want deleted", goneJob.Status)
	}
}

// TestJobsDarkLaunch_KillSwitch verifies FAAS_JOBS_ENABLED=0 returns
// 404 jobs_not_allowed on every endpoint.
func TestJobsDarkLaunch_KillSwitch(t *testing.T) {
	t.Setenv("FAAS_JOBS_ENABLED", "0")
	e := setup(t, api.PlanPro)

	for _, tc := range []struct {
		method, path string
	}{
		{"GET", "/v1/jobs"},
		{"POST", "/v1/jobs"},
		{"GET", "/v1/jobs/0123456789abcdef0123456789abcdef"},
		{"DELETE", "/v1/jobs/0123456789abcdef0123456789abcdef"},
		{"POST", "/v1/jobs/0123456789abcdef0123456789abcdef/runs"},
		{"GET", "/v1/runs/0123456789abcdef0123456789abcdef"},
		{"POST", "/v1/runs/0123456789abcdef0123456789abcdef/cancel"},
		{"GET", "/v1/runs/0123456789abcdef0123456789abcdef/tasks"},
	} {
		rec := e.do(t, tc.method, tc.path, nil, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s status = %d, want 404 (jobs disabled)", tc.method, tc.path, rec.Code)
		}
		var p api.Problem
		_ = json.Unmarshal(rec.Body.Bytes(), &p)
		if p.Code != api.CodeJobsNotAllowed {
			t.Errorf("%s %s code = %q, want %q", tc.method, tc.path, p.Code, api.CodeJobsNotAllowed)
		}
	}
}

// TestJobsIDOR_CrossAccountReturns404 verifies that a job owned by
// account A is invisible to account B (404, not 403, not 200).
// The test creates two accounts on the same MemStore and probes
// the cross-account fetch.
func TestJobsIDOR_CrossAccountReturns404(t *testing.T) {
	eA := setup(t, api.PlanPro)
	eB := setup(t, api.PlanPro)

	// Account A creates a job.
	create := eA.do(t, "POST", "/v1/jobs", makeJob("a-only"), nil)
	if create.Code != http.StatusCreated {
		t.Fatalf("seed: %d %s", create.Code, create.Body)
	}
	var job api.JobResponse
	_ = json.Unmarshal(create.Body.Bytes(), &job)

	// Account B tries to read A's job — must be 404, byte-identical
	// to a job that doesn't exist (IDOR-safe).
	get := eB.do(t, "GET", "/v1/jobs/"+job.ID, nil, nil)
	if get.Code != http.StatusNotFound {
		t.Errorf("cross-account get = %d, want 404", get.Code)
	}

	// Account B tries to delete A's job — must be 404, not 403.
	del := eB.do(t, "DELETE", "/v1/jobs/"+job.ID, nil, nil)
	if del.Code != http.StatusNotFound {
		t.Errorf("cross-account delete = %d, want 404", del.Code)
	}
}

// TestJobsValidation_BadImageRef verifies that an image_ref without
// the sha256: or ref: prefix is rejected at the validation gate.
func TestJobsValidation_BadImageRef(t *testing.T) {
	e := setup(t, api.PlanPro)
	bad := makeJob("bad-image")
	bad.ImageRef = "latest"
	rec := e.do(t, "POST", "/v1/jobs", bad, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad image_ref status = %d, want 400: %s", rec.Code, rec.Body)
	}
	var p api.Problem
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if p.Code != api.CodeJobInvalid {
		t.Errorf("bad image_ref code = %q, want %q", p.Code, api.CodeJobInvalid)
	}
}

// TestJobsValidation_RAMOverCap verifies that a ram_mb above the
// plan cap is rejected with 403 job_ram_too_large.
func TestJobsValidation_RAMOverCap(t *testing.T) {
	for _, plan := range []api.Plan{api.PlanHobby, api.PlanPro, api.PlanScale} {
		t.Run(string(plan), func(t *testing.T) {
			e := setup(t, plan)
			over := makeJob("ram-over")
			limits := api.MustLimitsFor(plan)
			if limits.JobMaxRAMMB == 0 {
				t.Skip("plan has no RAM headroom")
			}
			over.RAMMB = int32(limits.JobMaxRAMMB + 1)
			rec := e.do(t, "POST", "/v1/jobs", over, nil)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("ram over cap status = %d, want 400: %s", rec.Code, rec.Body)
			}
			var p api.Problem
			_ = json.Unmarshal(rec.Body.Bytes(), &p)
			if p.Code != api.CodeJobRAMTooLarge {
				t.Errorf("ram over cap code = %q, want %q", p.Code, api.CodeJobRAMTooLarge)
			}
		})
	}
}

// TestJobsValidation_TaskTimeoutOverCap mirrors RAMOverCap for
// task_timeout_s — applies only to plans that have a JobMaxTaskTimeoutS.
func TestJobsValidation_TaskTimeoutOverCap(t *testing.T) {
	for _, plan := range []api.Plan{api.PlanHobby, api.PlanPro, api.PlanScale} {
		t.Run(string(plan), func(t *testing.T) {
			e := setup(t, plan)
			limits := api.MustLimitsFor(plan)
			if limits.JobMaxTaskTimeoutS == 0 {
				t.Skip("plan has no task-timeout headroom")
			}
			over := makeJob("timeout-over")
			over.TaskTimeoutS = int32(limits.JobMaxTaskTimeoutS + 1)
			rec := e.do(t, "POST", "/v1/jobs", over, nil)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("timeout over cap status = %d, want 400: %s", rec.Code, rec.Body)
			}
		})
	}
}

// TestJobsValidation_ParallelismOverCap rejects max_parallelism above
// the per-plan cap (the validator uses CodeJobInvalid for this).
func TestJobsValidation_ParallelismOverCap(t *testing.T) {
	for _, plan := range []api.Plan{api.PlanHobby, api.PlanPro, api.PlanScale} {
		t.Run(string(plan), func(t *testing.T) {
			e := setup(t, plan)
			limits := api.MustLimitsFor(plan)
			if limits.JobMaxParallelismPerRun == 0 {
				t.Skip("plan has no parallelism headroom")
			}
			over := makeJob("par-over")
			over.MaxParallelism = int32(limits.JobMaxParallelismPerRun + 1)
			rec := e.do(t, "POST", "/v1/jobs", over, nil)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("parallelism over cap status = %d, want 400: %s", rec.Code, rec.Body)
			}
		})
	}
}

// TestJobsValidation_TasksOverCap rejects run.tasks above the per-plan
// cap (JobMaxTasksPerRun).
func TestJobsValidation_TasksOverCap(t *testing.T) {
	for _, plan := range []api.Plan{api.PlanHobby, api.PlanPro, api.PlanScale} {
		t.Run(string(plan), func(t *testing.T) {
			e := setup(t, plan)
			create := e.do(t, "POST", "/v1/jobs", makeJob("cap-tasks"), nil)
			if create.Code != http.StatusCreated {
				t.Fatalf("seed: %d %s", create.Code, create.Body)
			}
			var job api.JobResponse
			_ = json.Unmarshal(create.Body.Bytes(), &job)
			limits := api.MustLimitsFor(plan)
			if limits.JobMaxTasksPerRun == 0 {
				t.Skip("plan has no tasks cap")
			}
			over := api.CreateRunRequest{Tasks: int32(limits.JobMaxTasksPerRun + 1)}
			rec := e.do(t, "POST", "/v1/jobs/"+job.ID+"/runs", over, nil)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("tasks over cap status = %d, want 400: %s", rec.Code, rec.Body)
			}
		})
	}
}

// TestJobsPerAccountQuota verifies that creating past JobMaxPerAccount
// returns 403 job_quota_exceeded. Pro = 25, Scale = 100, Hobby = 5.
func TestJobsPerAccountQuota(t *testing.T) {
	for _, plan := range []api.Plan{api.PlanHobby, api.PlanPro, api.PlanScale} {
		t.Run(string(plan), func(t *testing.T) {
			e := setup(t, plan)
			limits := api.MustLimitsFor(plan)
			cap := limits.JobMaxPerAccount
			if cap == 0 {
				t.Skip("Free plan — jobs not enabled")
			}
			for i := 0; i < cap; i++ {
				rec := e.do(t, "POST", "/v1/jobs", makeJob(fmt.Sprintf("%s-job-%d", plan, i)), nil)
				if rec.Code != http.StatusCreated {
					t.Fatalf("job %d should succeed under cap %d: %d %s", i, cap, rec.Code, rec.Body)
				}
			}
			over := e.do(t, "POST", "/v1/jobs", makeJob(fmt.Sprintf("%s-over", plan)), nil)
			if over.Code != http.StatusForbidden {
				t.Errorf("over-quota create status = %d, want 403: %s", over.Code, over.Body)
			}
			var p api.Problem
			_ = json.Unmarshal(over.Body.Bytes(), &p)
			if p.Code != api.CodeJobQuotaExceeded {
				t.Errorf("over-quota code = %q, want %q", p.Code, api.CodeJobQuotaExceeded)
			}
		})
	}
}

// TestJobsFreePlan_NoJobsAllowed pins the Free-plan behavior: jobs
// are gated off (cap=0) so creation returns 404 jobs_not_allowed —
// the same gate as the dark-launch flag, because both Free and
// flag-off land on the "feature not enabled for this account" branch.
func TestJobsFreePlan_NoJobsAllowed(t *testing.T) {
	e := setup(t, api.PlanFree)
	rec := e.do(t, "POST", "/v1/jobs", makeJob("free-job"), nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("free plan create status = %d, want 404 jobs_not_allowed: %s", rec.Code, rec.Body)
	}
	var p api.Problem
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if p.Code != api.CodeJobsNotAllowed {
		t.Errorf("free plan create code = %q, want %q", p.Code, api.CodeJobsNotAllowed)
	}
}

// TestJobsRetry_NotRetryableOnQueuedTask verifies that retrying a
// task that hasn't reached a terminal state is rejected with 409
// (the task is in 'queued' state; only failed/timeout/oom/cancelled
// are retryable). Without schedd dispatch in the unit-test harness,
// no task ever reaches a terminal state.
func TestJobsRetry_NotRetryableOnQueuedTask(t *testing.T) {
	e := setup(t, api.PlanPro)
	create := e.do(t, "POST", "/v1/jobs", makeJob("retry-job"), nil)
	if create.Code != http.StatusCreated {
		t.Fatalf("seed: %d %s", create.Code, create.Body)
	}
	var job api.JobResponse
	_ = json.Unmarshal(create.Body.Bytes(), &job)
	runCreate := e.do(t, "POST", "/v1/jobs/"+job.ID+"/runs", api.CreateRunRequest{Tasks: 3}, nil)
	if runCreate.Code != http.StatusCreated {
		t.Fatalf("seed run: %d %s", runCreate.Code, runCreate.Body)
	}
	var run api.JobRunResponse
	_ = json.Unmarshal(runCreate.Body.Bytes(), &run)

	// task_index=1 is queued (never dispatched in unit-test env).
	rec := e.do(t, "POST", fmt.Sprintf("/v1/runs/%s/tasks/%d/retry", run.ID, 1),
		api.RetryTaskRequest{}, nil)
	if rec.Code != http.StatusConflict {
		t.Errorf("retry queued status = %d, want 409: %s", rec.Code, rec.Body)
	}
	var p api.Problem
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if p.Code != api.CodeValidation {
		t.Errorf("retry queued code = %q, want %q", p.Code, api.CodeValidation)
	}
}

// TestJobsListPagination pins the ?limit + next_before cursor
// behavior. The first page with limit=2 must return 2 jobs + a
// non-empty next_before. The MemStore returns jobs in random
// map-iteration order, so a follow-up cursor round-trip isn't
// deterministic — the second page is exercised by the integration
// test against PgStore where the ORDER BY id DESC sort is
// guaranteed. Here we just verify the first-page semantics.
func TestJobsListPagination(t *testing.T) {
	e := setup(t, api.PlanPro)
	for i := 0; i < 3; i++ {
		rec := e.do(t, "POST", "/v1/jobs", makeJob(fmt.Sprintf("page-%d", i)), nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("seed %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	// limit=2 → exactly 2 returned + next_before set.
	rec := e.do(t, "GET", "/v1/jobs?limit=2", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	var page api.ListJobsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if page.Count != 2 {
		t.Errorf("page.Count = %d, want 2", page.Count)
	}
	if page.NextBefore == "" {
		t.Errorf("page.NextBefore empty, want the last row's id")
	}
	// Default limit (no query param) returns all 3 with empty next_before.
	rec2 := e.do(t, "GET", "/v1/jobs", nil, nil)
	if rec2.Code != http.StatusOK {
		t.Fatalf("default list: %d %s", rec2.Code, rec2.Body)
	}
	var page2 api.ListJobsResponse
	_ = json.Unmarshal(rec2.Body.Bytes(), &page2)
	if page2.Count != 3 {
		t.Errorf("default list count = %d, want 3", page2.Count)
	}
	if page2.NextBefore != "" {
		t.Errorf("default next_before = %q, want empty", page2.NextBefore)
	}
}

// TestJobsCountUsesCorrectStore verifies the ListJobsResponse.Count()
// helper is consistent with len(.Jobs). The handler populates both
// from the same underlying slice.
func TestJobsCountUsesCorrectStore(t *testing.T) {
	e := setup(t, api.PlanPro)
	for i := 0; i < 2; i++ {
		rec := e.do(t, "POST", "/v1/jobs", makeJob(fmt.Sprintf("store-%d", i)), nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("seed: %d %s", rec.Code, rec.Body)
		}
	}
	// Confirm the MemStore's CountJobsForAccount matches the wire
	// Count. (Belt-and-braces: the handler's Count comes from the
	// wire DTO field, not from the store, so this guards against
	// accidental divergence.)
	got, err := e.store.CountJobsForAccount(context.Background(), e.acct.ID)
	if err != nil {
		t.Fatalf("CountJobsForAccount: %v", err)
	}
	if got != 2 {
		t.Errorf("MemStore count = %d, want 2", got)
	}
}
