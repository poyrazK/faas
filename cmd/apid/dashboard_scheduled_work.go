package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

const dashboardPolicyFormMaxBytes = 64 * 1024

func dashboardCronSchedulePolicyAction(slug string) string {
	return "scheduled_cron_policy_" + slug
}

func dashboardCronSchedulePolicyCookie(slug string) string {
	return "faas_csrf_cron_policy_" + slug
}

func dashboardFailureRulesJSON(rules *workpolicy.FailureRules) string {
	if rules == nil {
		return ""
	}
	b, err := json.MarshalIndent(rules, "", "  ")
	if err != nil {
		return ""
	}
	return string(b)
}

func projectDashboardScheduleOccurrences(rows []state.ScheduleOccurrence) []dashboard.ScheduleOccurrencePageItem {
	items := make([]dashboard.ScheduleOccurrencePageItem, 0, len(rows))
	for _, occurrence := range rows {
		item := dashboard.ScheduleOccurrencePageItem{
			ScheduledFor: dashboardJobsTime(occurrence.ScheduledFor), Status: occurrence.Status,
			StatusClass: dashboardOccurrenceStatusClass(occurrence.Status), Reason: occurrence.Reason,
			RunID: occurrence.JobRunID, TaskID: occurrence.AppTaskID,
			BlockingID: occurrence.BlockingOccurrenceID,
		}
		if occurrence.StartDeadlineAt != nil {
			item.DeadlineAt = dashboardJobsTime(*occurrence.StartDeadlineAt)
		}
		items = append(items, item)
	}
	return items
}

func dashboardOccurrenceStatusClass(status string) string {
	switch status {
	case "queued", "running":
		return status
	case "succeeded":
		return "succeeded"
	case "failed", "missed_deadline":
		return "failed"
	case "cancelled":
		return "cancelled"
	default:
		return "unknown"
	}
}

func dashboardScheduleFlash(r *http.Request) string {
	switch r.URL.Query().Get("scheduled_work") {
	case "updated":
		return "updated"
	case "replayed":
		return "replayed"
	case "error":
		return "error"
	default:
		return ""
	}
}

func parseDashboardSchedulePolicyForm(w http.ResponseWriter, r *http.Request) (workpolicy.SchedulePolicy, *workpolicy.FailureRules, error) {
	r.Body = http.MaxBytesReader(w, r.Body, dashboardPolicyFormMaxBytes)
	if err := r.ParseForm(); err != nil {
		return workpolicy.SchedulePolicy{}, nil, err
	}
	deadline, err := strconv.Atoi(strings.TrimSpace(r.FormValue("start_deadline_seconds")))
	if err != nil || deadline < 0 || deadline > api.WorkPolicyMaxStartDeadlineSeconds {
		return workpolicy.SchedulePolicy{}, nil, errors.New("start_deadline_seconds must be between 0 and 2592000")
	}
	policy := workpolicy.SchedulePolicy{
		Version: workpolicy.Version, Overlap: strings.TrimSpace(r.FormValue("overlap")),
		StartDeadlineSeconds: deadline, MissedRuns: strings.TrimSpace(r.FormValue("missed_runs")),
	}
	if err := policy.Validate(); err != nil {
		return workpolicy.SchedulePolicy{}, nil, err
	}
	var failureRules *workpolicy.FailureRules
	if raw := strings.TrimSpace(r.FormValue("failure_rules_json")); raw != "" {
		var parsed workpolicy.FailureRules
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			return workpolicy.SchedulePolicy{}, nil, fmt.Errorf("failure_rules_json must be valid JSON: %w", err)
		}
		failureRules = &parsed
	}
	if problem := validateWorkPolicies(&policy, failureRules); problem != nil {
		return workpolicy.SchedulePolicy{}, nil, errors.New(problem.Detail)
	}
	return policy, failureRules, nil
}

func (s *server) dashboardUpdateJobSchedulePolicy(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardJobSchedulePolicyAction, acct.ID, dashboardJobSchedulePolicyCookie); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid CSRF token", "please reload the page and try again"))
		return
	}
	name := r.PathValue("name")
	job, found, err := s.resolveJob(r.Context(), name, acct)
	if err != nil {
		s.log.Error("dashboard update job schedule policy: resolve job", "account_id", acct.ID, "job", name, "err", err)
		dashboardRedirectScheduledWork(w, r, "/dashboard/jobs", "error")
		return
	}
	if !found || job.CronSchedule == "" || !acct.Plan.JobsAllowed() {
		dashboardRedirectScheduledWork(w, r, "/dashboard/jobs", "error")
		return
	}
	policy, rules, err := parseDashboardSchedulePolicyForm(w, r)
	if err != nil {
		dashboardRedirectScheduledWork(w, r, "/dashboard/jobs", "error")
		return
	}
	updater, ok := s.store.(state.JobScheduleUpdateStore)
	if !ok {
		dashboardRedirectScheduledWork(w, r, "/dashboard/jobs", "error")
		return
	}
	updated, err := updater.JobUpdateWithSchedule(r.Context(), job.ID, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		state.JobPolicyOptions{SchedulePolicy: &policy, FailureRules: rules})
	if err != nil {
		s.log.Warn("dashboard update job schedule policy failed", "account_id", acct.ID, "job_id", job.ID, "err", err)
		dashboardRedirectScheduledWork(w, r, "/dashboard/jobs", "error")
		return
	}
	s.audit.Emit(r.Context(), "job.updated", &acct.ID, map[string]any{
		"job_id": updated.ID, "name": updated.Name,
		"old": map[string]any{"schedule_policy": job.SchedulePolicy, "failure_rules": job.FailureRules},
		"new": map[string]any{"schedule_policy": updated.SchedulePolicy, "failure_rules": updated.FailureRules},
	})
	_ = s.notif.Notify(r.Context(), db.NotifyJobChanged,
		fmt.Sprintf(`{"kind":"updated","job_id":"%s","account_id":"%s"}`, updated.ID, acct.ID))
	dashboardRedirectScheduledWork(w, r, "/dashboard/jobs", "updated")
}

func (s *server) dashboardUpdateCronSchedulePolicy(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	slug, id := r.PathValue("slug"), r.PathValue("id")
	if !validSlug(slug) || !dashboardFireCronIDRe.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardCronSchedulePolicyAction(slug), acct.ID, dashboardCronSchedulePolicyCookie(slug)); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid CSRF token", "please reload the page and try again"))
		return
	}
	redirectPath := "/dashboard/apps/" + slug + "?scheduled_work=error#cron-" + id
	app, err := s.store.AppBySlug(r.Context(), slug)
	if err != nil || app.AccountID != acct.ID {
		dashboardRedirectScheduledWork(w, r, "/dashboard/apps", "error")
		return
	}
	cron, err := s.store.CronByID(r.Context(), id)
	if err != nil || cron.AppID != app.ID || len(cron.Command) == 0 {
		dashboardRedirectScheduledWork(w, r, "/dashboard/apps/"+url.PathEscape(slug), "error")
		return
	}
	policy, rules, err := parseDashboardSchedulePolicyForm(w, r)
	if err != nil {
		http.Redirect(w, r, redirectPath, http.StatusSeeOther)
		return
	}
	options := state.CronOptions{RetryMax: cron.RetryMax, RetryBackoffSeconds: cron.RetryBackoffSeconds,
		SchedulePolicy: &policy, FailureRules: rules}
	updated, err := s.store.UpdateCronWithOptions(r.Context(), cron.ID, nil, nil, nil, nil, nil, nil, options)
	if err != nil {
		s.log.Warn("dashboard update command cron schedule policy failed", "account_id", acct.ID, "app_id", app.ID, "cron_id", cron.ID, "err", err)
		http.Redirect(w, r, redirectPath, http.StatusSeeOther)
		return
	}
	s.audit.Emit(r.Context(), "cron.updated", &acct.ID, map[string]any{
		"cron_id": updated.ID, "app_id": updated.AppID,
		"old": map[string]any{"schedule_policy": cron.SchedulePolicy, "failure_rules": cron.FailureRules},
		"new": map[string]any{"schedule_policy": updated.SchedulePolicy, "failure_rules": updated.FailureRules},
	})
	_ = s.notif.Notify(r.Context(), db.NotifyCronChanged, `{"kind":"updated","cron":"`+id+`"}`)
	http.Redirect(w, r, dashboardPolicyFormPage(slug, id, "updated"), http.StatusSeeOther)
}

func dashboardRedirectScheduledWork(w http.ResponseWriter, r *http.Request, path, status string) {
	values := url.Values{}
	values.Set("scheduled_work", status)
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	http.Redirect(w, r, path+separator+values.Encode(), http.StatusSeeOther)
}

func dashboardPolicyFormPage(slug, cronID, status string) string {
	return "/dashboard/apps/" + url.PathEscape(slug) + "?scheduled_work=" + url.QueryEscape(status) + "#cron-" + url.PathEscape(cronID)
}

func (s *server) dashboardReplayFailedJobRun(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardReplayFailedAction, acct.ID, dashboardReplayFailedCSRFCookie); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid CSRF token", "please reload the page and try again"))
		return
	}
	name, runID := r.PathValue("name"), r.PathValue("id")
	if name == "" || runID == "" {
		dashboardRedirectScheduledWork(w, r, "/dashboard/jobs", "error")
		return
	}
	// The source run ID is the form's stable idempotency key. A double submit
	// or a browser retry must not fan out the same failed partitions twice.
	apiRequest := r.Clone(r.Context())
	apiURL := *r.URL
	apiURL.Path = "/v1/jobs/" + url.PathEscape(name) + "/runs/" + url.PathEscape(runID) + "/replay-failed"
	apiURL.RawPath = ""
	apiRequest.URL = &apiURL
	apiRequest.Header = r.Header.Clone()
	apiRequest.Header.Set("Idempotency-Key", "dashboard-job-replay-"+runID)
	apiRequest.SetPathValue("name", name)
	apiRequest.SetPathValue("id", runID)
	result := httptest.NewRecorder()
	s.idempotent(s.replayFailedJobRun)(result, apiRequest, acct)
	if result.Code == http.StatusCreated {
		dashboardRedirectScheduledWork(w, r, "/dashboard/jobs", "replayed")
		return
	}
	dashboardRedirectScheduledWork(w, r, "/dashboard/jobs", "error")
}
