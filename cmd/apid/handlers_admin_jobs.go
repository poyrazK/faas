// Operator job-run incident controls.
//
// These routes replace the jobs backlog runbook's direct PostgreSQL reads and
// customer-token cancellation. Read responses expose only bounded job/run/task
// lifecycle metadata; task lease tokens, environment overrides, commands, and
// images remain outside the operator projection. Cancellation reuses the same
// atomic state primitive and scheduler notification as the customer route.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	operatorJobDefaultLimit = 50
	operatorJobMaxLimit     = 500
)

func (s *server) getOperatorActiveJobRuns(w http.ResponseWriter, r *http.Request, caller state.Account) {
	if allowed, prob := s.adminAllows(caller); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	accountID := strings.TrimSpace(r.URL.Query().Get("account_id"))
	if accountID != "" {
		var prob *api.Problem
		accountID, prob = parseOperatorJobUUID(accountID, "account_id")
		if prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		if _, err := s.store.AccountByID(r.Context(), accountID); err != nil {
			writeOperatorJobLookupError(w, err, "account")
			return
		}
	}
	prob, limit := api.ParseLimit(r.URL.Query().Get("limit"), operatorJobDefaultLimit, operatorJobMaxLimit, "operator job runs")
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	offset, prob := parseOperatorJobOffset(r.URL.Query().Get("offset"))
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	runs, err := s.store.JobRunListActive(r.Context(), accountID, limit, offset)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list active job runs"))
		return
	}
	items, err := s.projectOperatorJobRuns(r.Context(), runs)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not resolve active job runs"))
		return
	}
	nextOffset := -1
	if len(items) == limit {
		nextOffset = offset + len(items)
	}
	emitOperatorJobsView(r, s, caller, accountID, "jobs.active")
	writeJSON(w, http.StatusOK, api.OperatorJobRunListResponse{
		AccountID:  accountID,
		Runs:       items,
		Limit:      limit,
		Offset:     offset,
		NextOffset: nextOffset,
	})
}

func (s *server) getOperatorJobRun(w http.ResponseWriter, r *http.Request, caller state.Account) {
	if allowed, prob := s.adminAllows(caller); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	job, run, prob := s.resolveOperatorJobRun(r.Context(), r.PathValue("id"))
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	prob, limit := api.ParseLimit(r.URL.Query().Get("task_limit"), operatorJobDefaultLimit, operatorJobMaxLimit, "operator job tasks")
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	offset, prob := parseOperatorJobOffset(r.URL.Query().Get("task_offset"))
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	tasks, err := s.store.JobTaskList(r.Context(), run.ID, limit, offset)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not inspect job run tasks"))
		return
	}
	projectedTasks := make([]api.JobTaskResponse, 0, len(tasks))
	for _, task := range tasks {
		projectedTasks = append(projectedTasks, jobTaskResponse(task))
	}
	nextOffset := -1
	if len(projectedTasks) == limit {
		nextOffset = offset + len(projectedTasks)
	}
	emitOperatorActionView(r, s, caller, run.AccountID, "jobs.inspect")
	writeJSON(w, http.StatusOK, api.OperatorJobRunDetailResponse{
		Run:            projectOperatorJobRun(job, run),
		Tasks:          projectedTasks,
		TaskLimit:      limit,
		TaskOffset:     offset,
		NextTaskOffset: nextOffset,
	})
}

func (s *server) postOperatorJobRunCancel(w http.ResponseWriter, r *http.Request, caller state.Account) {
	if allowed, prob := s.adminAllows(caller); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	if r.URL.Query().Get("confirm") != "true" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"confirm required", "?confirm=true is required to cancel customer job work"))
		return
	}
	reason, prob := parseRequiredOperatorJobReason(r.URL.Query().Get("reason"))
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	job, run, prob := s.resolveOperatorJobRun(r.Context(), r.PathValue("id"))
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	previousStatus := run.AggregateStatus
	if previousStatus != "queued" && previousStatus != "running" && previousStatus != "cancelled" {
		emitOperatorJobCancelAudit(r, s, caller, job, run, previousStatus, reason, "rejected")
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"job run is terminal", "only queued or running job runs can be cancelled"))
		return
	}
	result := "cancelled"
	if previousStatus == "cancelled" {
		result = "already_cancelled"
	} else {
		cancelled, err := s.store.JobRunCancel(r.Context(), run.ID)
		if err != nil {
			writeOperatorJobLookupError(w, err, "job run")
			return
		}
		run = cancelled
		if run.AggregateStatus != "cancelled" {
			emitOperatorJobCancelAudit(r, s, caller, job, run, previousStatus, reason, "lost_race")
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
				"job run completed", "the run became terminal before cancellation was applied"))
			return
		}
		_ = s.notif.Notify(r.Context(), db.NotifyJobChanged,
			fmt.Sprintf(`{"kind":"run_cancelled","job_id":"%s","run_id":"%s","account_id":"%s"}`, job.ID, run.ID, run.AccountID))
	}
	emitOperatorJobCancelAudit(r, s, caller, job, run, previousStatus, reason, result)
	cancelledAt := time.Now().UTC().Format(time.RFC3339)
	if run.FinishedAt != nil {
		cancelledAt = run.FinishedAt.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, api.OperatorJobRunCancelResponse{
		Run:         projectOperatorJobRun(job, run),
		CancelledAt: cancelledAt,
		Reason:      reason,
	})
}

func (s *server) resolveOperatorJobRun(ctx context.Context, rawID string) (state.Job, state.JobRun, *api.Problem) {
	runID, prob := parseOperatorJobUUID(rawID, "run id")
	if prob != nil {
		return state.Job{}, state.JobRun{}, prob
	}
	run, err := s.store.JobRunGetByID(ctx, runID)
	if err != nil {
		return state.Job{}, state.JobRun{}, operatorJobLookupProblem(err, "job run")
	}
	job, err := s.store.JobGetByID(ctx, run.JobID)
	if err != nil {
		return state.Job{}, state.JobRun{}, operatorJobLookupProblem(err, "job")
	}
	if job.AccountID != run.AccountID {
		return state.Job{}, state.JobRun{}, api.NewProblem(http.StatusInternalServerError,
			api.CodeInternal, "job ownership mismatch", "job run ownership is inconsistent")
	}
	return job, run, nil
}

func (s *server) projectOperatorJobRuns(ctx context.Context, runs []state.JobRun) ([]api.OperatorJobRun, error) {
	items := make([]api.OperatorJobRun, 0, len(runs))
	jobs := make(map[string]state.Job)
	for _, run := range runs {
		job, ok := jobs[run.JobID]
		if !ok {
			var err error
			job, err = s.store.JobGetByID(ctx, run.JobID)
			if err != nil {
				return nil, err
			}
			jobs[run.JobID] = job
		}
		if job.AccountID != run.AccountID {
			return nil, errors.New("job run ownership mismatch")
		}
		items = append(items, projectOperatorJobRun(job, run))
	}
	return items, nil
}

func projectOperatorJobRun(job state.Job, run state.JobRun) api.OperatorJobRun {
	out := api.OperatorJobRun{
		JobID:           job.ID,
		JobName:         job.Name,
		RunID:           run.ID,
		AccountID:       run.AccountID,
		AggregateStatus: run.AggregateStatus,
		Tasks:           run.Tasks,
		TasksRunning:    run.TasksRunning,
		TasksSucceeded:  run.TasksSucceeded,
		TasksFailed:     run.TasksFailed,
		TasksCancelled:  run.TasksCancelled,
		RAMMB:           job.RAMMB,
		CreatedAt:       run.CreatedAt.UTC().Format(time.RFC3339),
	}
	if run.StartedAt != nil {
		out.StartedAt = run.StartedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func parseOperatorJobUUID(raw, field string) (string, *api.Problem) {
	id := strings.TrimSpace(raw)
	if _, err := uuid.Parse(id); err != nil {
		return "", api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"invalid "+field, field+" is required and must be a UUID")
	}
	return id, nil
}

func parseOperatorJobOffset(raw string) (int, *api.Problem) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(raw)
	if err != nil || offset < 0 {
		return 0, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"invalid offset", "offset must be a non-negative integer")
	}
	return offset, nil
}

func parseRequiredOperatorJobReason(raw string) (string, *api.Problem) {
	reason := strings.TrimSpace(raw)
	if len(reason) > obsOpsReasonMaxLen || !obsOpsReasonShape.MatchString(reason) {
		return "", api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"invalid reason", "reason is required and must match [a-z0-9_]{1,64}")
	}
	return reason, nil
}

func operatorJobLookupProblem(err error, kind string) *api.Problem {
	if errors.Is(err, state.ErrNotFound) {
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such "+kind)
	}
	return api.ErrCapacity("could not resolve " + kind)
}

func writeOperatorJobLookupError(w http.ResponseWriter, err error, kind string) {
	api.WriteProblem(w, operatorJobLookupProblem(err, kind))
}

func emitOperatorJobCancelAudit(r *http.Request, s *server, caller state.Account, job state.Job, run state.JobRun, previousStatus, reason, result string) {
	if s.audit == nil {
		return
	}
	subject := run.AccountID
	s.audit.Emit(r.Context(), "operator.action.cancel_job_run", &subject, map[string]any{
		"actor":             caller.ID,
		"target_account_id": run.AccountID,
		"job_id":            job.ID,
		"job_name":          job.Name,
		"run_id":            run.ID,
		"previous_status":   previousStatus,
		"result":            result,
		"reason":            reason,
	})
}

func emitOperatorJobsView(r *http.Request, s *server, caller state.Account, accountID, endpoint string) {
	if s.audit == nil {
		return
	}
	var subject *string
	targetKind := "fleet"
	if accountID != "" {
		subject = &accountID
		targetKind = "account"
	}
	s.audit.Emit(r.Context(), "operator.action.view", subject, map[string]any{
		"actor":             caller.ID,
		"endpoint":          endpoint,
		"target_kind":       targetKind,
		"target_account_id": accountID,
	})
}
