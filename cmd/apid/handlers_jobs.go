// handlers_jobs.go — apid handlers for ADR-099 jobs (run-to-
// completion workloads). The jobs surface mirrors the cron shape
// minus the cron-shaped timing knobs (no schedule, no path) and
// plus the run/task fan-out tree (Cloud Run Jobs shape).
//
// Routes (registered in server.go::handler):
//
//	GET    /v1/jobs                         → listJobs
//	POST   /v1/jobs                         → createJob
//	GET    /v1/jobs/{id}                    → getJob
//	PATCH  /v1/jobs/{id}                    → updateJob
//	DELETE /v1/jobs/{id}                    → deleteJob
//	POST   /v1/jobs/{id}/runs               → createRun
//	GET    /v1/jobs/{id}/runs               → listRuns
//	GET    /v1/runs/{run_id}                → getRun
//	POST   /v1/runs/{run_id}/cancel         → cancelRun
//	GET    /v1/runs/{run_id}/tasks          → listRunTasks
//	POST   /v1/runs/{run_id}/tasks/{idx}/retry → retryTask
//
// Trust model
//
//   - Every route gates on FAAS_JOBS_ENABLED=1 AND on the plan
//     having JobMaxPerAccount > 0 (Free today; the LimitsFor
//     consult fires BEFORE the store lookup so a Free customer
//     gets a clean 404 with code=jobs_not_allowed).
//   - IDOR-safe: GetJob / UpdateJob / DeleteJob / CreateRun /
//     ListRuns all resolve JobID → Job.AccountID and compare
//     against acct.ID before reading or writing any other store.
//     Cross-account access returns 404 with code=job_not_found
//     (byte-identical to "wrong id") — mirrors the IDOR pattern
//     used by LoadApp / CronByID.
//   - Per-account quota consult (CountJobsForAccount vs
//     JobMaxPerAccount) is enforced by CreateJobIfUnderQuota
//     under the per-account row lock (mirrors
//     CreateCronIfUnderQuota's defence-in-depth shape).
//   - per-run task fan-out bounds (JobMaxTasksPerRun,
//     JobMaxParallelismPerRun, JobMaxRAMMB, JobMaxTaskTimeoutS)
//     are validated at the apid gate with 400 shape errors; the
//     schedd dispatch tick trusts the validated request shape.
//   - Plain env_overrides are accepted in plaintext (NOT sealed —
//     jobs are customer-data, not credentials). The wire shape
//     matches AppEnvVar exactly: map[string]string with empty
//     values rejected at write time.

package main

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// jobsDefaultLimit mirrors the crons / alerts default (50) so the
// CLI's --limit flag parses uniformly across list endpoints.
const jobsDefaultLimit = 50

// jobsMaxLimit caps the page size; the same shape parseCronRunsLimit
// uses. Anything higher gets clamped to keep List*ForAccount's
// in-memory projection bounded.
const jobsMaxLimit = 500

// gateJobsEnabled returns 404 jobs_not_allowed when the dark-launch
// flag is unset OR the plan has JobMaxPerAccount == 0 (Free today).
// Called at the top of every handler so the gate ordering is
// uniform — a Free customer gets the same response whether they
// POST /v1/jobs or GET /v1/jobs/{id}.
func gateJobsEnabled(w http.ResponseWriter, acct state.Account) (api.Limits, bool) {
	if !api.JobsEnabled() {
		api.WriteProblem(w, api.ErrJobsNotAllowed())
		return api.Limits{}, false
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok || limits.JobMaxPerAccount == 0 {
		api.WriteProblem(w, api.ErrJobsNotAllowed())
		return api.Limits{}, false
	}
	return limits, true
}

// jobResponse projects a state.Job onto the wire shape. Boundary
// conversion happens here because pkg/api cannot import pkg/state
// (memory: pkg-api-cannot-import-pkg-state).
func jobResponse(j state.Job) api.JobResponse {
	env := j.EnvOverrides
	if env == nil {
		env = map[string]string{}
	}
	return api.JobResponse{
		ID:             j.ID,
		Name:           j.Name,
		Kind:           string(j.Kind),
		ImageRef:       j.ImageRef,
		RAMMB:          j.RamMb,
		TaskTimeoutS:   j.TaskTimeoutS,
		MaxParallelism: j.MaxParallelism,
		RetryMax:       j.RetryMax,
		EnvOverrides:   env,
		Status:         string(j.Status),
		CreatedAt:      j.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:      j.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// jobRunResponse projects a state.JobRun onto the wire shape. RFC3339
// timestamps on every non-nil pointer; empty strings when nil
// (omitempty) so a freshly-queued run does not carry a sentinel
// "0001-01-01T00:00:00Z" started_at.
func jobRunResponse(r state.JobRun) api.JobRunResponse {
	out := api.JobRunResponse{
		ID:              r.ID,
		JobID:           r.JobID,
		TriggerKind:     string(r.TriggerKind),
		Tasks:           r.Tasks,
		Parallelism:     r.Parallelism,
		TasksSucceeded:  r.TasksSucceeded,
		TasksFailed:     r.TasksFailed,
		TasksCancelled:  r.TasksCancelled,
		TasksRunning:    r.TasksRunning,
		AggregateStatus: string(r.AggregateStatus),
		CreatedAt:       r.CreatedAt.UTC().Format(time.RFC3339),
	}
	if r.EnvOverrides != nil {
		out.EnvOverrides = r.EnvOverrides
	}
	if r.StartedAt != nil {
		out.StartedAt = r.StartedAt.UTC().Format(time.RFC3339)
	}
	if r.FinishedAt != nil {
		out.FinishedAt = r.FinishedAt.UTC().Format(time.RFC3339)
	}
	return out
}

// jobTaskResponse projects a state.JobTask onto the wire shape.
// instance_id is nil until schedd claims the task; error_class /
// error_message are nil until the task reaches a terminal-failure
// state.
func jobTaskResponse(t state.JobTask) api.JobTaskResponse {
	out := api.JobTaskResponse{
		TaskIndex: t.TaskIndex,
		Status:    string(t.Status),
		Attempt:   t.Attempt,
		CreatedAt: t.CreatedAt.UTC().Format(time.RFC3339),
	}
	if t.InstanceID != nil {
		s := *t.InstanceID
		out.InstanceID = &s
	}
	if t.ErrorClass != nil {
		s := *t.ErrorClass
		out.ErrorClass = &s
	}
	if t.ErrorMessage != nil {
		s := *t.ErrorMessage
		out.ErrorMessage = &s
	}
	if t.StartedAt != nil {
		out.StartedAt = t.StartedAt.UTC().Format(time.RFC3339)
	}
	if t.FinishedAt != nil {
		out.FinishedAt = t.FinishedAt.UTC().Format(time.RFC3339)
	}
	return out
}

// --- list -------------------------------------------------------------------

// listJobs returns every job for the account. The cursor is the
// jobs.id UUIDv7 (matches the deployments + alert-rules list shape);
// the handler emits NextBefore = last row's id when the page is full.
// Quota carries JobMaxPerAccount so the CLI can render a "3/25 jobs"
// progress bar without a second call.
func (s *server) listJobs(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := gateJobsEnabled(w, acct); !ok {
		return
	}
	limit, perr := parseJobsListLimit(r)
	if perr != nil {
		api.WriteProblem(w, perr)
		return
	}
	rows, err := s.store.ListJobsForAccount(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list jobs"))
		return
	}
	// Cursor (before) + limit are applied AFTER the fetch because
	// the per-account job count is bounded by JobMaxPerAccount
	// (Free=0, Hobby=5, Pro=25, Scale=100). Fetching all rows then
	// paginating in-memory keeps the cursor semantics simple — the
	// alternative is a `WHERE id < $before ORDER BY id DESC LIMIT
	// $n` query but the upper bound is small enough that the
	// in-memory fan-out is cheaper than the extra index walk.
	before := r.URL.Query().Get("before")
	page, next := paginateJobSlice(rows, before, limit)
	out := make([]api.JobResponse, 0, len(page))
	for _, j := range page {
		out = append(out, jobResponse(j))
	}
	limits, _ := api.LimitsFor(acct.Plan)
	writeJSON(w, http.StatusOK, api.ListJobsResponse{
		Jobs:       out,
		NextBefore: next,
		Quota:      limits.JobMaxPerAccount,
		Count:      len(page),
	})
}

// --- create -----------------------------------------------------------------

// createJob registers a new job definition. The handler validates
// shape + plan caps BEFORE the store consult; CreateJobIfUnderQuota
// applies the per-account quota check under the row lock.
func (s *server) createJob(w http.ResponseWriter, r *http.Request, acct state.Account) {
	limits, ok := gateJobsEnabled(w, acct)
	if !ok {
		return
	}
	var req api.CreateJobRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	trimmed, ok := api.TrimNonEmpty(req.Name)
	if !ok {
		api.WriteProblem(w, api.ErrJobInvalid("name must be non-empty"))
		return
	}
	req.Name = trimmed
	if prob := validateJobBody(req, limits); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	env := req.EnvOverrides
	if env == nil {
		env = map[string]string{}
	}
	row, err := s.store.CreateJobIfUnderQuota(r.Context(), state.Job{
		AccountID:      acct.ID,
		Kind:           state.JobKindRunToCompletion,
		Name:           req.Name,
		ImageRef:       req.ImageRef,
		RamMb:          req.RAMMB,
		TaskTimeoutS:   req.TaskTimeoutS,
		MaxParallelism: req.MaxParallelism,
		RetryMax:       req.RetryMax,
		EnvOverrides:   env,
		Status:         state.JobStatusActive,
	}, limits)
	if err != nil {
		var qe *state.JobQuotaError
		switch {
		case errors.As(err, &qe):
			api.WriteProblem(w, api.ErrJobQuotaExceeded(acct.Plan, api.PlanQuotaScopeAccount, int(qe.Limit), int(qe.Observed)))
		case errors.Is(err, state.ErrConflict):
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation, "Job name already exists", "a job with this name already exists for this account; pick a unique name"))
		default:
			api.WriteProblem(w, api.ErrCapacity("could not create job"))
		}
		return
	}
	s.log.Info("job created", "job", row.ID, "name", row.Name, "account", acct.ID)
	s.audit.Emit(r.Context(), "job.created", &acct.ID, map[string]any{
		"job_id":          row.ID,
		"name":            row.Name,
		"image_ref":       row.ImageRef,
		"ram_mb":          row.RamMb,
		"task_timeout_s":  row.TaskTimeoutS,
		"max_parallelism": row.MaxParallelism,
		"retry_max":       row.RetryMax,
	})
	writeJSON(w, http.StatusCreated, jobResponse(row))
}

// --- get --------------------------------------------------------------------

// getJob returns one job by id. IDOR-safe: a foreign account's
// job returns 404 job_not_found (byte-identical to "wrong id") so
// the SDK cannot enumerate cross-account.
func (s *server) getJob(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := gateJobsEnabled(w, acct); !ok {
		return
	}
	id := r.PathValue("id")
	j, err := s.store.GetJob(r.Context(), id, acct.ID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrJobNotFound(id))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not get job"))
		return
	}
	writeJSON(w, http.StatusOK, jobResponse(j))
}

// --- update -----------------------------------------------------------------

// updateJob applies a partial update. Pointer-based fields let the
// caller distinguish "unset" from "explicit zero"; matches the
// UpdateCron / UpdateAlertRule shape.
func (s *server) updateJob(w http.ResponseWriter, r *http.Request, acct state.Account) {
	limits, ok := gateJobsEnabled(w, acct)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var req api.UpdateJobRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if prob := validateJobUpdateBody(req, limits); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	env := req.EnvOverrides
	var envPtr *map[string]string
	if env != nil {
		envCopy := make(map[string]string, len(env))
		for k, v := range env {
			envCopy[k] = v
		}
		envPtr = &envCopy
	}
	j, err := s.store.UpdateJob(r.Context(), id, acct.ID,
		nil, req.ImageRef, req.RAMMB, req.TaskTimeoutS,
		req.MaxParallelism, req.RetryMax, envPtr, nil,
	)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrJobNotFound(id))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not update job"))
		return
	}
	s.audit.Emit(r.Context(), "job.updated", &acct.ID, map[string]any{
		"job_id": j.ID,
		"name":   j.Name,
	})
	writeJSON(w, http.StatusOK, jobResponse(j))
}

// --- delete -----------------------------------------------------------------

// deleteJob removes a job definition. Returns 204 on success; 409
// when non-terminal runs still reference the job (the CLI prints
// "cancel runs first" on this case — the store-side check is
// enforced inside DeleteJob).
func (s *server) deleteJob(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := gateJobsEnabled(w, acct); !ok {
		return
	}
	id := r.PathValue("id")
	if err := s.store.DeleteJob(r.Context(), id, acct.ID); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrJobNotFound(id))
			return
		}
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation, "Job has live runs", "this job has non-terminal runs; cancel them first"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not delete job"))
		return
	}
	s.audit.Emit(r.Context(), "job.deleted", &acct.ID, map[string]any{"job_id": id})
	w.WriteHeader(http.StatusNoContent)
}

// --- createRun --------------------------------------------------------------

// createRun schedules a new run of an existing job. tasks is
// required; parallelism + env_overrides default to the job's
// configured values when omitted. InsertJobTasks writes N child
// rows in one transaction; schedd's dispatch tick picks them up on
// the next runJobsTick.
func (s *server) createRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	limits, ok := gateJobsEnabled(w, acct)
	if !ok {
		return
	}
	jobID := r.PathValue("id")
	var req api.CreateRunRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if prob := validateCreateRunBody(req, limits); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	// IDOR-safe: resolve the job + verify ownership BEFORE
	// inserting the run. Cross-account jobID returns 404 (same as
	// unknown id).
	j, err := s.store.GetJob(r.Context(), jobID, acct.ID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrJobNotFound(jobID))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not load job"))
		return
	}
	parallelism := j.MaxParallelism
	if req.Parallelism != nil {
		parallelism = *req.Parallelism
	}
	env := req.EnvOverrides
	if env == nil {
		env = j.EnvOverrides
		if env == nil {
			env = map[string]string{}
		}
	}
	run, err := s.store.CreateJobRun(r.Context(), state.JobRun{
		JobID:           j.ID,
		AccountID:       acct.ID,
		TriggerKind:     state.JobRunTriggerManual,
		EnvOverrides:    env,
		Tasks:           req.Tasks,
		Parallelism:     parallelism,
		AggregateStatus: state.JobRunStatusQueued,
	})
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrJobNotFound(jobID))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not create run"))
		return
	}
	// InsertJobTasks writes [0..Tasks) task rows in one shot.
	// Without this the run row would have aggregate_status='queued'
	// but no child rows for the dispatch tick to claim.
	indices := make([]int32, req.Tasks)
	for i := int32(0); i < req.Tasks; i++ {
		indices[i] = i
	}
	if err := s.store.InsertJobTasks(r.Context(), run.ID, indices); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not insert run tasks"))
		return
	}
	s.audit.Emit(r.Context(), "job.run.created", &acct.ID, map[string]any{
		"job_id": j.ID,
		"run_id": run.ID,
		"tasks":  run.Tasks,
	})
	writeJSON(w, http.StatusCreated, jobRunResponse(run))
}

// --- listRuns ---------------------------------------------------------------

// listRuns returns every run for a job. Cursor + limit shape
// mirrors listJobs; the in-memory fan-out is bounded by
// JobMaxTasksPerRun (Hobby=100, Scale=5000).
func (s *server) listRuns(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := gateJobsEnabled(w, acct); !ok {
		return
	}
	jobID := r.PathValue("id")
	j, err := s.store.GetJob(r.Context(), jobID, acct.ID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrJobNotFound(jobID))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not list runs"))
		return
	}
	limit, perr := parseJobsListLimit(r)
	if perr != nil {
		api.WriteProblem(w, perr)
		return
	}
	rows, err := s.store.ListJobRunsForJob(r.Context(), j.ID, acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list runs"))
		return
	}
	before := r.URL.Query().Get("before")
	page, next := paginateJobRunSlice(rows, before, limit)
	out := make([]api.JobRunResponse, 0, len(page))
	for _, run := range page {
		out = append(out, jobRunResponse(run))
	}
	writeJSON(w, http.StatusOK, api.ListRunsResponse{
		Runs:       out,
		NextBefore: next,
	})
}

// --- getRun -----------------------------------------------------------------

// getRun returns one run by id. IDOR-safe (404 run_not_found on
// missing OR cross-account). The run may belong to a job that
// belongs to the account; the two-step resolve is identical to
// CronByID → AppByID → AccountID.
func (s *server) getRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := gateJobsEnabled(w, acct); !ok {
		return
	}
	runID := r.PathValue("run_id")
	run, err := s.store.GetJobRun(r.Context(), runID, acct.ID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrJobRunNotFound(runID))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not get run"))
		return
	}
	writeJSON(w, http.StatusOK, jobRunResponse(run))
}

// --- cancelRun --------------------------------------------------------------

// cancelRun stops a non-terminal run. 409 job_run_cancelled if the
// run is already in a terminal state — the dispatch tick has
// stamped succeeded / failed / deadline_exceeded before this point
// and the cancel would be a no-op.
func (s *server) cancelRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := gateJobsEnabled(w, acct); !ok {
		return
	}
	runID := r.PathValue("run_id")
	run, err := s.store.GetJobRun(r.Context(), runID, acct.ID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrJobRunNotFound(runID))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not load run"))
		return
	}
	switch run.AggregateStatus {
	case state.JobRunStatusSucceeded, state.JobRunStatusDeadLetter, state.JobRunStatusCancelled:
		api.WriteProblem(w, api.ErrJobRunCancelled(runID))
		return
	}
	// The schedd dispatch tick flips the run to 'cancelled' once
	// every in-flight task reports back. apid's role is to (a)
	// verify ownership (above) and (b) emit a cancel signal that
	// schedd picks up via pg_notify. The CancelledAt stamp on the
	// row comes from a follow-up UpdateJobRunCancelStatus call (the
	// store's MarkJobRunCancelled — the apid route deliberately
	// stays thin here so the dispatch path remains the single
	// source of truth for the cancel fan-out).
	if err := s.store.MarkJobRunCancelled(r.Context(), runID); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not cancel run"))
		return
	}
	s.audit.Emit(r.Context(), "job.run.cancelled", &acct.ID, map[string]any{
		"run_id": runID,
		"job_id": run.JobID,
	})
	updated, err := s.store.GetJobRun(r.Context(), runID, acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not reload run"))
		return
	}
	writeJSON(w, http.StatusAccepted, jobRunResponse(updated))
}

// --- listRunTasks -----------------------------------------------------------

// listRunTasks pages the task rows for a run. Cursor is the last
// task_index seen; the next page starts at that index + 1. Tasks
// are stored in task_index order, so this is a stable ascending
// walk.
func (s *server) listRunTasks(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := gateJobsEnabled(w, acct); !ok {
		return
	}
	runID := r.PathValue("run_id")
	run, err := s.store.GetJobRun(r.Context(), runID, acct.ID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrJobRunNotFound(runID))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not list tasks"))
		return
	}
	limit, perr := parseJobsListLimit(r)
	if perr != nil {
		api.WriteProblem(w, perr)
		return
	}
	// ListRunTasksForRun walks the child rows in task_index order.
	// The store-side method handles the `before` cursor.
	before := r.URL.Query().Get("before")
	rows, err := s.store.ListRunTasksForRun(r.Context(), run.ID, before, limit)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list tasks"))
		return
	}
	out := make([]api.JobTaskResponse, 0, len(rows))
	for _, t := range rows {
		out = append(out, jobTaskResponse(t))
	}
	next := ""
	if len(out) == limit && len(out) > 0 {
		next = fmt.Sprintf("%d", out[len(out)-1].TaskIndex+1)
	}
	writeJSON(w, http.StatusOK, api.ListRunTasksResponse{
		Tasks:      out,
		NextBefore: next,
	})
}

// --- retryTask --------------------------------------------------------------

// retryTask manually retries a failed/deadline task. The dispatch
// tick picks up the new attempt on the next runJobsTick. ResetAttempt
// (default true) zeroes the per-task attempt counter.
func (s *server) retryTask(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := gateJobsEnabled(w, acct); !ok {
		return
	}
	runID := r.PathValue("run_id")
	taskIndexStr := r.PathValue("idx")
	taskIndex, err := strconv.ParseInt(taskIndexStr, 10, 32)
	if err != nil || taskIndex < 0 {
		api.WriteProblem(w, api.ErrJobInvalid("task index must be a non-negative integer"))
		return
	}
	run, err := s.store.GetJobRun(r.Context(), runID, acct.ID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrJobRunNotFound(runID))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not load run"))
		return
	}
	var req api.RetryTaskRequest
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &req); err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
			return
		}
	}
	reset := true
	if req.ResetAttempt != nil {
		reset = *req.ResetAttempt
	}
	task, err := s.store.JobTaskByRunAndIndex(r.Context(), run.ID, int32(taskIndex))
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrJobTaskNotFound(runID, int(taskIndex)))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not load task"))
		return
	}
	// Manual retry only valid for terminal-failure tasks.
	switch task.Status {
	case state.JobTaskStatusQueued, state.JobTaskStatusClaimed, state.JobTaskStatusSucceeded:
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
			"Task not retryable",
			fmt.Sprintf("task is in state %q; only failed/timeout/oom/cancelled tasks are retryable", task.Status)))
		return
	}
	if err := s.store.MarkJobTaskRetried(r.Context(), run.ID, int32(taskIndex), reset); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not retry task"))
		return
	}
	updated, err := s.store.JobTaskByRunAndIndex(r.Context(), run.ID, int32(taskIndex))
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not reload task"))
		return
	}
	s.audit.Emit(r.Context(), "job.task.retried", &acct.ID, map[string]any{
		"run_id":     runID,
		"task_index": taskIndex,
		"reset":      reset,
	})
	writeJSON(w, http.StatusAccepted, jobTaskResponse(updated))
}

// --- validators -------------------------------------------------------------

// validateJobBody enforces the create-time shape + plan caps. The
// per-account quota consult is delegated to CreateJobIfUnderQuota;
// the shape consult lives here so the wire gets a stable 400 (not
// a quota 403) for malformed input.
func validateJobBody(req api.CreateJobRequest, limits api.Limits) *api.Problem {
	if !strings.HasPrefix(req.ImageRef, "sha256:") && !strings.HasPrefix(req.ImageRef, "ref:") {
		return api.ErrJobInvalid("image_ref must be a sha256: digest or ref: name")
	}
	if req.RAMMB <= 0 {
		return api.ErrJobInvalid("ram_mb must be positive")
	}
	if int(req.RAMMB) > limits.JobMaxRAMMB {
		return api.ErrJobRAMTooLarge(limits.Plan, int(req.RAMMB), limits.JobMaxRAMMB)
	}
	if req.TaskTimeoutS <= 0 {
		return api.ErrJobInvalid("task_timeout_s must be positive")
	}
	if int(req.TaskTimeoutS) > limits.JobMaxTaskTimeoutS {
		return api.ErrJobTimeoutTooLong(limits.Plan, int(req.TaskTimeoutS), limits.JobMaxTaskTimeoutS)
	}
	if req.MaxParallelism <= 0 {
		return api.ErrJobInvalid("max_parallelism must be positive")
	}
	if int(req.MaxParallelism) > limits.JobMaxParallelismPerRun {
		return api.ErrJobInvalid(fmt.Sprintf(
			"max_parallelism exceeds plan cap %d", limits.JobMaxParallelismPerRun))
	}
	if req.RetryMax < 0 {
		return api.ErrJobInvalid("retry_max must be non-negative")
	}
	return nil
}

// validateJobUpdateBody enforces the partial-update shape + plan
// caps. Pointer-based fields mean "unset" is a no-op; only non-nil
// fields are checked.
func validateJobUpdateBody(req api.UpdateJobRequest, limits api.Limits) *api.Problem {
	if req.ImageRef != nil && !strings.HasPrefix(*req.ImageRef, "sha256:") && !strings.HasPrefix(*req.ImageRef, "ref:") {
		return api.ErrJobInvalid("image_ref must be a sha256: digest or ref: name")
	}
	if req.RAMMB != nil {
		if *req.RAMMB <= 0 {
			return api.ErrJobInvalid("ram_mb must be positive")
		}
		if int(*req.RAMMB) > limits.JobMaxRAMMB {
			return api.ErrJobRAMTooLarge(limits.Plan, int(*req.RAMMB), limits.JobMaxRAMMB)
		}
	}
	if req.TaskTimeoutS != nil {
		if *req.TaskTimeoutS <= 0 {
			return api.ErrJobInvalid("task_timeout_s must be positive")
		}
		if int(*req.TaskTimeoutS) > limits.JobMaxTaskTimeoutS {
			return api.ErrJobTimeoutTooLong(limits.Plan, int(*req.TaskTimeoutS), limits.JobMaxTaskTimeoutS)
		}
	}
	if req.MaxParallelism != nil {
		if *req.MaxParallelism <= 0 {
			return api.ErrJobInvalid("max_parallelism must be positive")
		}
		if int(*req.MaxParallelism) > limits.JobMaxParallelismPerRun {
			return api.ErrJobInvalid(fmt.Sprintf(
				"max_parallelism exceeds plan cap %d", limits.JobMaxParallelismPerRun))
		}
	}
	if req.RetryMax != nil && *req.RetryMax < 0 {
		return api.ErrJobInvalid("retry_max must be non-negative")
	}
	return nil
}

// validateCreateRunBody enforces the create-run shape + plan caps.
// Tasks must be in [1, JobMaxTasksPerRun]; parallelism + env_overrides
// are optional and default to the job's values.
func validateCreateRunBody(req api.CreateRunRequest, limits api.Limits) *api.Problem {
	if req.Tasks <= 0 {
		return api.ErrJobInvalid("tasks must be positive")
	}
	if int(req.Tasks) > limits.JobMaxTasksPerRun {
		return api.ErrJobInvalid(fmt.Sprintf(
			"tasks exceeds plan cap %d", limits.JobMaxTasksPerRun))
	}
	if req.Parallelism != nil {
		if *req.Parallelism <= 0 {
			return api.ErrJobInvalid("parallelism must be positive")
		}
		if int(*req.Parallelism) > limits.JobMaxParallelismPerRun {
			return api.ErrJobInvalid(fmt.Sprintf(
				"parallelism exceeds plan cap %d", limits.JobMaxParallelismPerRun))
		}
	}
	return nil
}

// --- helpers ----------------------------------------------------------------

// parseJobsListLimit parses ?limit=N from the URL. Falls back to
// jobsDefaultLimit (50) when absent or unparseable; clamps to
// jobsMaxLimit (500). Returns a Problem on explicit invalid values
// (e.g. "limit=abc") so the SDK gets a stable 400.
func parseJobsListLimit(r *http.Request) (int, *api.Problem) {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return jobsDefaultLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, api.ErrJobInvalid("limit must be a positive integer")
	}
	if n > jobsMaxLimit {
		n = jobsMaxLimit
	}
	return n, nil
}

// paginateJobSlice applies the `before` cursor + limit for a slice
// of state.Job rows. The cursor is the last row's id (UUIDv7); rows
// with id < cursor are kept. Rows are pre-sorted by id DESC by the
// store, so the cursor pagination is well-defined.
func paginateJobSlice(rows []state.Job, before string, limit int) ([]state.Job, string) {
	out := make([]state.Job, 0, len(rows))
	for _, r := range rows {
		if before != "" && r.ID >= before {
			continue
		}
		out = append(out, r)
	}
	if len(out) <= limit {
		return out, ""
	}
	page := out[:limit]
	return page, page[len(page)-1].ID
}

// paginateJobRunSlice mirrors paginateJobSlice for state.JobRun
// rows. Two near-identical helpers exist because Go's type system
// cannot express a generic constraint on "any type with a string
// ID field" without an interface that the state types do not
// satisfy; the duplication is cheaper than the indirection.
func paginateJobRunSlice(rows []state.JobRun, before string, limit int) ([]state.JobRun, string) {
	out := make([]state.JobRun, 0, len(rows))
	for _, r := range rows {
		if before != "" && r.ID >= before {
			continue
		}
		out = append(out, r)
	}
	if len(out) <= limit {
		return out, ""
	}
	page := out[:limit]
	return page, page[len(page)-1].ID
}
