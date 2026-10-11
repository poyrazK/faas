package main

import (
	"fmt"
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
		data.ActiveConditionGroups = workflowActiveConditionLinks(data.SummaryURL, opts, summary)
		data.ConditionGroups = workflowConditionOutcomeLinks(data.SummaryURL, opts, summary)
		if summary.DecisionOutcomes != nil {
			data.DecisionGroups = workflowDecisionOutcomeLinks(data.SummaryURL, opts, summary)
			if summary.DecisionOutcomes.RuleComparisons != nil {
				data.RuleComparisons = workflowRuleComparisonViews(summary.DecisionOutcomes.RuleComparisons, data.ListURL, opts)
			}
		}
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

func workflowDecisionOutcomeLinks(path string, opts api.OperationWorkflowPerformanceOptions, summary api.OperationWorkflowPerformanceSummary) []dashboard.WorkflowDecisionOutcomeLink {
	links := make([]dashboard.WorkflowDecisionOutcomeLink, 0, len(summary.DecisionOutcomes.Groups))
	for _, value := range summary.DecisionOutcomes.Groups {
		group := api.OperationWorkflowPerformanceGroup{Dimension: "decision_completion", ContractVersion: value.ContractVersion, Kind: value.Kind, DecisionCode: value.DecisionCode, RuleID: value.RuleID, RuleVersion: value.RuleVersion, GroupID: value.GroupID, PathID: value.PathID, OutcomeCode: value.OutcomeCode}
		link := dashboard.WorkflowDecisionOutcomeLink{OperationWorkflowDecisionOutcomeGroup: value, CompletionURL: workflowPerformanceContributorURL(path, opts, "completed", summary.CohortToken, group)}
		group.Dimension = "decision_blocked"
		link.BlockedURL = workflowPerformanceContributorURL(path, opts, "completed", summary.CohortToken, group)
		links = append(links, link)
	}
	return links
}

func workflowRuleComparisonViews(summary *api.OperationWorkflowRuleComparisonSummary, listURL string, opts api.OperationWorkflowPerformanceOptions) []dashboard.WorkflowRuleComparison {
	views := make([]dashboard.WorkflowRuleComparison, 0, len(summary.Comparisons))
	for _, comparison := range summary.Comparisons {
		view := dashboard.WorkflowRuleComparison{OperationWorkflowRuleComparison: comparison}
		for _, version := range comparison.Versions {
			item := dashboard.WorkflowRuleVersion{OperationWorkflowRuleVersionStatistics: version}
			for _, outcome := range version.Outcomes {
				item.Outcomes = append(item.Outcomes, dashboard.WorkflowRuleOutcome{OperationWorkflowRuleVersionOutcome: outcome, Percent: fmt.Sprintf("%d.%02d%%", outcome.ProportionBasisPoints/100, outcome.ProportionBasisPoints%100)})
			}

			if version.SingleVersion != nil {
				item.SingleVersion = &dashboard.WorkflowRuleCohort{OperationWorkflowRuleVersionCohort: *version.SingleVersion}
				for _, outcome := range version.SingleVersion.Outcomes {
					item.SingleVersion.Outcomes = append(item.SingleVersion.Outcomes, dashboard.WorkflowRuleOutcome{OperationWorkflowRuleVersionOutcome: outcome, Percent: fmt.Sprintf("%d.%02d%%", outcome.ProportionBasisPoints/100, outcome.ProportionBasisPoints%100)})
				}
			}
			view.Versions = append(view.Versions, item)
		}

		for _, example := range comparison.MultiVersionExamples {
			query := url.Values{"scope": {opts.Scope}, "tenant_id": {example.PlatformTenantID}, "subject_type": {example.Subject.Type}, "subject_id": {example.Subject.ID}, "workflow": {opts.Workflow}, "workflow_instance_id": {example.InstanceID}}
			overlap := dashboard.WorkflowRuleOverlap{OperationWorkflowRuleVersionOverlap: example, HistoryURL: listURL + "?" + query.Encode() + "#workflows"}
			for _, fact := range example.Evidence {
				overlap.Evidence = append(overlap.Evidence, dashboard.WorkflowRuleEvidence{OperationWorkflowDecisionOutcomeEvidence: fact, OperationURL: listURL + "/" + url.PathEscape(fact.OperationID)})
			}
			view.MultiVersionExamples = append(view.MultiVersionExamples, overlap)
		}
		views = append(views, view)
	}
	return views
}

func workflowConditionOutcomeLinks(path string, opts api.OperationWorkflowPerformanceOptions, summary api.OperationWorkflowPerformanceSummary) []dashboard.WorkflowConditionOutcomeLink {
	links := []dashboard.WorkflowConditionOutcomeLink{}
	if summary.ConditionOutcomes == nil {
		return links
	}
	for _, value := range summary.ConditionOutcomes.Groups {
		group := api.OperationWorkflowPerformanceGroup{Dimension: "condition_completion", ContractVersion: value.ContractVersion, DecisionCode: value.DecisionCode, RuleID: value.RuleID, RuleVersion: value.RuleVersion, ConditionID: value.ConditionID, ConditionDescription: value.ConditionDescription, ConditionResult: value.ConditionResult, OutcomeCode: value.OutcomeCode}
		link := dashboard.WorkflowConditionOutcomeLink{OperationWorkflowConditionOutcomeGroup: value, CompletionURL: workflowPerformanceContributorURL(path, opts, "completed", summary.CohortToken, group)}
		group.Dimension = "condition_blocked"
		link.BlockedURL = workflowPerformanceContributorURL(path, opts, "completed", summary.CohortToken, group)
		links = append(links, link)
	}
	return links
}

func workflowActiveConditionLinks(path string, opts api.OperationWorkflowPerformanceOptions, summary api.OperationWorkflowPerformanceSummary) []dashboard.WorkflowActiveConditionLink {
	links := []dashboard.WorkflowActiveConditionLink{}
	if summary.ActiveConditions == nil {
		return links
	}
	for _, value := range summary.ActiveConditions.Groups {
		group := api.OperationWorkflowPerformanceGroup{Dimension: "active_condition_elapsed", ContractVersion: value.ContractVersion, DecisionCode: value.DecisionCode, RuleID: value.RuleID, RuleVersion: value.RuleVersion, ConditionID: value.ConditionID, ConditionDescription: value.ConditionDescription, ConditionResult: value.ConditionResult}
		link := dashboard.WorkflowActiveConditionLink{OperationWorkflowActiveConditionGroup: value, ElapsedURL: workflowPerformanceContributorURL(path, opts, "ongoing", summary.CohortToken, group)}
		group.Dimension = "active_condition_blocked"
		link.BlockedURL = workflowPerformanceContributorURL(path, opts, "ongoing", summary.CohortToken, group)
		links = append(links, link)
	}
	return links
}
