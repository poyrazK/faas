package main

import (
	"log/slog"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) renderAppWorkflowPerformance(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, app state.App) {
	w.Header().Set("Cache-Control", "no-store")
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeOperationError(w, state.ErrInvalidArgument)
		return
	}
	if q.Has("dimension") {
		s.renderAppWorkflowPerformanceInstances(w, r, log, acct, app)
		return
	}
	for _, key := range []string{"tenant_id", "workflow"} {
		if values := q[key]; len(values) == 1 && values[0] == "" {
			q.Del(key)
		}
	}
	if !q.Has("scope") {
		q.Set("scope", api.DefaultEnvScope)
	}
	clone := r.Clone(r.Context())
	u := *r.URL
	clone.URL = &u
	clone.URL.RawQuery = q.Encode()
	opts, err := operationWorkflowPerformanceOptions(clone, true, app.ID)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	data := dashboard.WorkflowPerformanceData{AppSlug: app.Slug, Scope: opts.Scope, TenantID: opts.TenantID, Workflow: opts.Workflow, ListURL: dashboardCustomerOperationsURL(app.Slug), SummaryURL: dashboardCustomerOperationsURL(app.Slug) + "/performance"}
	if opts.Workflow != "" {
		store, ok := s.store.(state.OperationWorkflowPerformanceStore)
		if !ok {
			api.WriteProblem(w, api.ErrCapacity("workflow performance unavailable"))
			return
		}
		summary, err := store.SummarizeAccountWorkflowPerformance(r.Context(), acct.ID, opts)
		if err != nil {
			writeOperationError(w, err)
			return
		}
		data.Summary = &summary
		data.EvaluatedAt = dashboardJobsTime(summary.EvaluatedAt)
		data.Cohorts = []dashboard.WorkflowPerformanceCohort{{Name: "Completed", OperationWorkflowPerformanceCohort: summary.Completed}, {Name: "Ongoing", OperationWorkflowPerformanceCohort: summary.Ongoing}}
		for index := range data.Cohorts {
			cohort := &data.Cohorts[index]
			value := "completed"
			if index == 1 {
				value = "ongoing"
			}
			cohort.StateTimeURL = workflowPerformanceContributorURL(data.SummaryURL, opts, value, summary.CohortToken, api.OperationWorkflowPerformanceGroup{Dimension: "state_time"})
			cohort.BlockedTimeURL = workflowPerformanceContributorURL(data.SummaryURL, opts, value, summary.CohortToken, api.OperationWorkflowPerformanceGroup{Dimension: "blocked_time"})
			cohort.VerificationWaitURL = workflowPerformanceContributorURL(data.SummaryURL, opts, value, summary.CohortToken, api.OperationWorkflowPerformanceGroup{Dimension: "verification_wait"})
			for _, group := range cohort.States {
				cohort.StateGroups = append(cohort.StateGroups, dashboard.WorkflowStatePerformanceLink{OperationWorkflowStatePerformance: group, URL: workflowPerformanceContributorURL(data.SummaryURL, opts, value, summary.CohortToken, api.OperationWorkflowPerformanceGroup{Dimension: "state", ContractVersion: group.ContractVersion, State: group.State})})
			}
			for _, group := range cohort.Blockers {
				cohort.BlockerGroups = append(cohort.BlockerGroups, dashboard.WorkflowBlockerPerformanceLink{OperationWorkflowBlockerPerformance: group, URL: workflowPerformanceContributorURL(data.SummaryURL, opts, value, summary.CohortToken, api.OperationWorkflowPerformanceGroup{Dimension: "blocker", ContractVersion: group.ContractVersion, Operation: group.Operation, Code: group.Code, Owner: group.Owner, Unassigned: group.Owner == ""})})
			}
			for _, group := range cohort.VerificationOwners {
				cohort.VerificationGroups = append(cohort.VerificationGroups, dashboard.WorkflowVerificationPerformanceLink{OperationWorkflowVerificationPerformance: group, URL: workflowPerformanceContributorURL(data.SummaryURL, opts, value, summary.CohortToken, api.OperationWorkflowPerformanceGroup{Dimension: "verification_owner", Owner: group.Owner, Unassigned: group.Owner == ""})})
			}
		}
	}
	count, err := s.store.CountDeployedApps(r.Context(), acct.ID)
	if err != nil {
		log.Warn("dashboard performance: count apps", "err", err)
	}
	page := dashboard.Page{Title: app.Slug + " workflow performance", Body: "workflow_performance", Account: dashboardAccountView(acct, count), Data: data}
	if err = dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}
