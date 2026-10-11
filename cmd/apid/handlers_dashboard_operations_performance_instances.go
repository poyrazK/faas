package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

func workflowPerformanceContributorURL(path string, opts api.OperationWorkflowPerformanceOptions, cohort, token string, group api.OperationWorkflowPerformanceGroup) string {
	q := url.Values{"scope": {opts.Scope}, "workflow": {opts.Workflow}, "cohort": {cohort}, "dimension": {group.Dimension}, "cohort_token": {token}}
	if opts.TenantID != "" {
		q.Set("tenant_id", opts.TenantID)
	}
	if group.ContractVersion > 0 {
		q.Set("contract_version", strconv.Itoa(group.ContractVersion))
	}
	for key, value := range map[string]string{"condition_evidence_status": group.ConditionEvidenceStatus, "condition_id": group.ConditionID, "condition_description": group.ConditionDescription, "condition_result": group.ConditionResult, "state": group.State, "operation": group.Operation, "code": group.Code, "owner": group.Owner, "kind": group.Kind, "decision_code": group.DecisionCode, "rule_id": group.RuleID, "rule_version": group.RuleVersion, "group_id": group.GroupID, "path_id": group.PathID, "outcome_code": group.OutcomeCode} {
		if value != "" {
			q.Set(key, value)
		}
	}
	if group.Unassigned {
		q.Set("unassigned", "true")
	}
	return path + "?" + q.Encode()
}
func workflowPerformanceGroupLabel(group api.OperationWorkflowPerformanceGroup) string {
	owner := group.Owner
	if owner == "" {
		owner = "unassigned"
	}
	switch group.Dimension {
	case "condition_completion", "condition_blocked":
		measure := "Completion time"
		if group.Dimension == "condition_blocked" {
			measure = "Blocked time"
		}
		return fmt.Sprintf("%s · condition %s = %s · decision %s · rule %s / %s → %s · contract %d", measure, group.ConditionID, group.ConditionResult, group.DecisionCode, group.RuleID, group.RuleVersion, group.OutcomeCode, group.ContractVersion)
	case "decision_completion", "decision_blocked", "active_condition_elapsed", "active_condition_blocked":
		return workflowDecisionOutcomeGroupLabel(group)
	case "state":
		return fmt.Sprintf("State %s · contract %d", group.State, group.ContractVersion)
	case "blocker":
		return fmt.Sprintf("%s / %s · contract %d · owner %s", group.Operation, group.Code, group.ContractVersion, owner)
	case "condition_refresh_age":
		return "Refresh priority · oldest overdue report age"
	case "condition_refresh_blocked":
		return "Refresh priority · full workflow blocked duration"
	case "condition_missing_owner":
		return fmt.Sprintf("Missing condition reports · state %s · contract %d · refresh owner %s", group.State, group.ContractVersion, owner)
	case "condition_refresh_owner":
		return "Overdue condition refresh · owner " + owner
	case "verification_owner":
		return "Verification owner " + owner
	case "blocked_time":
		return "Total blocked time"
	case "verification_wait":
		return "Total verification wait"
	default:
		return "Total state time"
	}
}
func (s *server) renderAppWorkflowPerformanceInstances(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, app state.App) {
	store, ok := s.store.(state.OperationWorkflowPerformanceInstanceStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow performance unavailable"))
		return
	}
	opts, err := operationWorkflowPerformanceInstanceOptions(r, true, app.ID)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	data := dashboard.WorkflowPerformanceData{AppSlug: app.Slug, Scope: opts.Scope, TenantID: opts.TenantID, Workflow: opts.Workflow, ListURL: dashboardCustomerOperationsURL(app.Slug), SummaryURL: dashboardCustomerOperationsURL(app.Slug) + "/performance", GroupLabel: workflowPerformanceGroupLabel(opts.OperationWorkflowPerformanceGroup)}
	refresh := url.Values{"scope": {opts.Scope}, "workflow": {opts.Workflow}}
	if opts.TenantID != "" {
		refresh.Set("tenant_id", opts.TenantID)
	}
	data.RefreshURL = data.SummaryURL + "?" + refresh.Encode()
	result, err := store.ListAccountWorkflowPerformanceInstances(r.Context(), acct.ID, opts)
	if errors.Is(err, state.ErrWorkflowPerformanceCohortChanged) {
		data.CohortChanged = true
	} else if err != nil {
		writeOperationError(w, err)
		return
	} else {
		data.Drilldown = &result
		data.EvaluatedAt = dashboardJobsTime(result.EvaluatedAt)
		for _, entry := range result.Items {
			history := url.Values{"scope": {opts.Scope}, "tenant_id": {entry.PlatformTenantID}, "subject_type": {entry.Subject.Type}, "subject_id": {entry.Subject.ID}, "workflow": {opts.Workflow}, "workflow_instance_id": {entry.State.InstanceID}}
			contributor := dashboard.WorkflowPerformanceContributor{Entry: entry, HistoryURL: data.ListURL + "?" + history.Encode() + "#workflows", OperationURL: data.ListURL + "/" + url.PathEscape(entry.OperationID)}
			if entry.DecisionEvidence != nil {
				contributor.DecisionOperationURL = data.ListURL + "/" + url.PathEscape(entry.DecisionEvidence.OperationID)
			}
			data.Contributors = append(data.Contributors, contributor)
		}
	}
	count, err := s.store.CountDeployedApps(r.Context(), acct.ID)
	if err != nil {
		log.Warn("dashboard contributors: count apps", "err", err)
	}
	page := dashboard.Page{Title: app.Slug + " workflow contributors", Body: "workflow_performance", Account: dashboardAccountView(acct, count), Data: data}
	if err = dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}

func workflowDecisionOutcomeGroupLabel(group api.OperationWorkflowPerformanceGroup) string {
	if group.Dimension == "active_condition_elapsed" || group.Dimension == "active_condition_blocked" {
		measure := "Elapsed time"
		if group.Dimension == "active_condition_blocked" {
			measure = "Blocked time"
		}
		return fmt.Sprintf("%s · last reported %s=%s · decision %s · rule %s / %s · contract %d", measure+workflowConditionFreshnessLabel(group.ConditionEvidenceStatus), group.ConditionID, group.ConditionResult, group.DecisionCode, group.RuleID, group.RuleVersion, group.ContractVersion)
	}

	measure := "Completion time"
	if group.Dimension == "decision_blocked" {
		measure = "Blocked time"
	}
	identity := "decision " + group.DecisionCode
	if group.Kind == "path" {
		identity = "path " + group.GroupID + " / " + group.PathID
	}
	if group.RuleID != "" {
		identity += " · rule " + group.RuleID + " / " + group.RuleVersion
	}
	return fmt.Sprintf("%s · %s → %s · contract %d", measure, identity, group.OutcomeCode, group.ContractVersion)
}

func workflowConditionFreshnessLabel(status string) string {
	if status == "" {
		return ""
	}
	return " · evidence " + status
}
