package main

// Dashboard surface for run-to-completion jobs and per-app queues
// (issue #1397 / G7). Read projections are bounded and account-scoped;
// the only mutation is the queue dead-letter replay form, which delegates
// to the same store transition used by the JSON API.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	dashboardJobsAction     = "queue_dead_letter_replay"
	dashboardJobsCSRFCookie = "faas_csrf_queue_replay"
	dashboardJobsPageLimit  = 100
	dashboardQueueLimit     = 20
)

// parseAppQueuesPath recognizes the per-app queue alias. The account-level
// page remains /dashboard/jobs; this alias makes queue links discoverable
// alongside the other app-scoped dashboard pages.
func parseAppQueuesPath(rest string) (string, bool) {
	rest = strings.TrimSuffix(rest, "/")
	const suffix = "/queues"
	if !strings.HasSuffix(rest, suffix) {
		return "", false
	}
	slug := strings.TrimSuffix(rest, suffix)
	if slug == "" || strings.Contains(slug, "/") {
		return "", false
	}
	return slug, true
}

func parseAppJobsPath(rest string) (string, bool) {
	rest = strings.TrimSuffix(rest, "/")
	const suffix = "/jobs"
	if !strings.HasSuffix(rest, suffix) {
		return "", false
	}
	slug := strings.TrimSuffix(rest, suffix)
	if slug == "" || strings.Contains(slug, "/") {
		return "", false
	}
	return slug, true
}

func (s *server) renderJobsQueues(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, selectedApp string) {
	ctx := r.Context()
	apps, err := s.store.ListApps(ctx, acct.ID)
	if err != nil {
		log.Warn("dashboard jobs: list apps", "account_id", acct.ID, "err", err)
		apps = nil
	}

	if selectedApp != "" {
		app, err := s.store.AppBySlug(ctx, selectedApp)
		if err != nil || app.AccountID != acct.ID {
			http.NotFound(w, r)
			return
		}
	}
	data := dashboard.JobsQueuesData{SelectedApp: selectedApp, Action: dashboardJobsActionFlash(r)}
	jobs, err := s.store.JobListByAccount(ctx, acct.ID, dashboardJobsPageLimit, 0)
	if err != nil {
		log.Warn("dashboard jobs: list jobs", "account_id", acct.ID, "err", err)
		data.ErrorMessage = "Job data is temporarily unavailable. Please try again shortly."
	} else {
		data.Jobs = projectDashboardJobs(jobs)
	}
	runs, err := s.store.JobRunListByAccount(ctx, acct.ID, dashboardJobsPageLimit, 0)
	if err != nil {
		log.Warn("dashboard jobs: list runs", "account_id", acct.ID, "err", err)
	} else {
		data.Runs = projectDashboardJobRuns(runs, jobs)
	}

	for _, app := range apps {
		if selectedApp != "" && app.Slug != selectedApp {
			continue
		}
		queue, ok := s.projectDashboardQueue(ctx, log, acct, app)
		if ok {
			data.Queues = append(data.Queues, queue)
		}
	}
	if s.sessions != nil {
		token, tokenErr := middleware.IssueForAuthenticatedNamed(s.sessions, dashboardJobsAction, acct.ID, dashboardJobsCSRFCookie)
		if tokenErr != nil {
			log.Warn("dashboard jobs: issue csrf", "account_id", acct.ID, "err", tokenErr)
		} else {
			data.ActionCSRF = token
			http.SetCookie(w, &http.Cookie{Name: dashboardJobsCSRFCookie, Value: token, Path: "/", HttpOnly: true,
				Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
		}
	}
	view, _ := AccountFrom(ctx)
	page := dashboard.Page{
		Title: "Jobs & Queues", Body: "jobs_queues", Account: dashboardAccountView(view, len(apps)), Data: data,
	}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(ctx), page); err != nil {
		renderProblem(w, log, err)
	}
}

func (s *server) projectDashboardQueue(ctx context.Context, log *slog.Logger, acct state.Account, app state.App) (dashboard.QueuePageItem, bool) {
	stats, err := s.store.QueueState(ctx, app.ID)
	if err != nil {
		log.Warn("dashboard jobs: queue state", "account_id", acct.ID, "app_id", app.ID, "err", err)
		return dashboard.QueuePageItem{}, false
	}
	limits := api.MustLimitsFor(acct.Plan)
	item := dashboard.QueuePageItem{
		App:   dashboard.AppListItem{Slug: app.Slug, Status: string(app.Status), URL: appURLForDomain(app.Slug, s.domain)},
		Depth: stats.Depth, InFlight: stats.InFlight, PlanCap: limits.MaxQueueDepth,
	}
	if !stats.OldestPendingAt.IsZero() {
		item.OldestPendingAt = dashboardJobsTime(stats.OldestPendingAt)
	}
	if rows, err := s.store.QueuePeek(ctx, app.ID, dashboardQueueLimit, ""); err == nil {
		item.Pending = projectDashboardQueueMessages(rows, false)
	} else {
		log.Warn("dashboard jobs: queue peek", "account_id", acct.ID, "app_id", app.ID, "err", err)
	}
	if rows, err := s.store.QueueDeadLetter(ctx, app.ID, dashboardQueueLimit, ""); err == nil {
		item.DeadLetters = projectDashboardQueueMessages(rows, true)
	} else {
		log.Warn("dashboard jobs: queue dead letter", "account_id", acct.ID, "app_id", app.ID, "err", err)
	}
	return item, true
}

func projectDashboardJobs(rows []state.Job) []dashboard.JobPageItem {
	items := make([]dashboard.JobPageItem, 0, len(rows))
	for _, job := range rows {
		items = append(items, dashboard.JobPageItem{ID: job.ID, Name: job.Name, Kind: job.Kind, ImageRef: job.ImageRef,
			Status: job.Status, RAMMB: job.RAMMB, TaskTimeoutSec: job.TaskTimeoutS, MaxParallelism: job.MaxParallelism,
			RetryMax: job.RetryMax, CreatedAt: dashboardJobsTime(job.CreatedAt), UpdatedAt: dashboardJobsTime(job.UpdatedAt)})
	}
	return items
}

func projectDashboardJobRuns(rows []state.JobRun, jobs []state.Job) []dashboard.JobRunPageItem {
	names := make(map[string]string, len(jobs))
	for _, job := range jobs {
		names[job.ID] = job.Name
	}
	items := make([]dashboard.JobRunPageItem, 0, len(rows))
	for _, run := range rows {
		item := dashboard.JobRunPageItem{ID: run.ID, JobID: run.JobID, JobName: names[run.JobID], TriggerKind: run.TriggerKind,
			AggregateStatus: run.AggregateStatus, Tasks: run.Tasks, TasksSucceeded: run.TasksSucceeded, TasksFailed: run.TasksFailed,
			TasksCancelled: run.TasksCancelled, TasksRunning: run.TasksRunning, DeadLetterCount: run.DeadLetterCount,
			CreatedAt: dashboardJobsTime(run.CreatedAt)}
		if run.StartedAt != nil {
			item.StartedAt = dashboardJobsTime(*run.StartedAt)
		}
		if run.FinishedAt != nil {
			item.FinishedAt = dashboardJobsTime(*run.FinishedAt)
		}
		items = append(items, item)
	}
	return items
}

func projectDashboardQueueMessages(rows []state.Invocation, replayable bool) []dashboard.QueueMessageItem {
	items := make([]dashboard.QueueMessageItem, 0, len(rows))
	for _, row := range rows {
		item := dashboard.QueueMessageItem{ID: row.ID, CreatedAt: dashboardJobsTime(row.CreatedAt), Attempts: row.Attempts,
			Payload: dashboardJobsPayload(row.Payload), LastError: row.LastError, Replayable: replayable}
		if row.CompletedAt != nil {
			item.FailedAt = dashboardJobsTime(*row.CompletedAt)
		}
		items = append(items, item)
	}
	return items
}

func dashboardJobsPayload(payload []byte) string {
	const maxPayload = 2048
	if len(payload) > maxPayload {
		return string(payload[:maxPayload]) + "…"
	}
	return string(payload)
}

func dashboardJobsTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func dashboardJobsActionFlash(r *http.Request) string {
	if r.URL.Query().Get("action") == "replayed" {
		return "replayed"
	}
	if r.URL.Query().Get("action") == "error" {
		return "error"
	}
	return ""
}

func (s *server) dashboardQueueDeadLetterReplay(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	slug, id := r.PathValue("slug"), r.PathValue("id")
	if !validSlug(slug) || id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardJobsAction, acct.ID, dashboardJobsCSRFCookie); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid CSRF token", "please reload the page and try again"))
		return
	}
	app, err := s.store.AppBySlug(r.Context(), slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	inv, err := s.store.InvocationByID(r.Context(), id)
	if err != nil || inv.AccountID != acct.ID || inv.AppID != app.ID || inv.State != state.InvocationDeadLetter {
		http.NotFound(w, r)
		return
	}
	if _, err := s.store.RetryQueueDeadLetter(r.Context(), acct.ID, id); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrInvocationNotFound(id))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("queue dead-letter replay"))
		return
	}
	values := url.Values{"app": []string{slug}, "action": []string{"replayed"}}
	http.Redirect(w, r, "/dashboard/jobs?"+values.Encode(), http.StatusSeeOther)
}
