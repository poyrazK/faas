package apidserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operation"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

func workflowPerformanceContributorURL(path string, opts api.OperationWorkflowPerformanceOptions, cohort, token string, group api.OperationWorkflowPerformanceGroup) string {
	q := url.Values{"scope": {opts.Scope}, "workflow": {opts.Workflow}, "cohort": {cohort}, "dimension": {group.Dimension}, "cohort_token": {token}}
	if group.Dimension == "condition_gap_owner" || group.Dimension == "condition_gap_queue" {
		q.Set("baseline_token", opts.BaselineToken)
	}
	if opts.TenantID != "" {
		q.Set("tenant_id", opts.TenantID)
	}
	if group.ContractVersion > 0 {
		q.Set("contract_version", strconv.Itoa(group.ContractVersion))
	}
	for key, value := range map[string]string{"gap_status": group.GapStatus, "condition_evidence_status": group.ConditionEvidenceStatus, "condition_id": group.ConditionID, "condition_description": group.ConditionDescription, "condition_result": group.ConditionResult, "state": group.State, "operation": group.Operation, "code": group.Code, "owner": group.Owner, "kind": group.Kind, "decision_code": group.DecisionCode, "rule_id": group.RuleID, "rule_version": group.RuleVersion, "group_id": group.GroupID, "path_id": group.PathID, "outcome_code": group.OutcomeCode} {
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
	case "condition_gap_queue":
		return "Workflow reporting-gap queue"
	case "condition_gap_owner":
		return fmt.Sprintf("Reporting gaps · %s · state %s · contract %d · refresh owner %s", group.GapStatus, group.State, group.ContractVersion, owner)
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
func (s *server) renderAppWorkflowPerformanceInstances(ctx context.Context, w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, app state.App) {
	store, ok := s.store.(state.OperationWorkflowPerformanceInstanceStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow performance unavailable"))
		return
	}
	r = normalizeWorkflowGapQueueForm(ctx, r)
	opts, err := operation.OperationWorkflowPerformanceInstanceOptions(r, true, app.ID)
	if err != nil {
		operation.WriteOperationError(w, err)
		return
	}
	data := dashboard.WorkflowPerformanceData{BaselineToken: opts.BaselineToken, AppSlug: app.Slug, Scope: opts.Scope, TenantID: opts.TenantID, Workflow: opts.Workflow, ListURL: dashboardCustomerOperationsURL(app.Slug), SummaryURL: dashboardCustomerOperationsURL(app.Slug) + "/performance", GroupLabel: workflowPerformanceGroupLabel(opts.OperationWorkflowPerformanceGroup)}
	refresh := url.Values{"scope": {opts.Scope}, "workflow": {opts.Workflow}}
	if opts.TenantID != "" {
		refresh.Set("tenant_id", opts.TenantID)
	}
	data.RefreshURL = data.SummaryURL + "?" + refresh.Encode()
	result, err := store.ListAccountWorkflowPerformanceInstances(ctx, acct.ID, opts)
	if errors.Is(err, state.ErrWorkflowPerformanceCohortChanged) {
		data.CohortChanged = true
	} else if err != nil {
		operation.WriteOperationError(w, err)
		return
	} else {
		data.Drilldown = &result
		data.EvaluatedAt = dashboardJobsTime(result.EvaluatedAt)
		data.Contributors = workflowPerformanceContributorViews(data, opts, result)
	}
	count, err := s.store.CountDeployedApps(ctx, acct.ID)
	if err != nil {
		log.Warn("dashboard contributors: count apps", "err", err)
	}
	page := dashboard.Page{Title: app.Slug + " workflow contributors", Body: "workflow_performance", Account: dashboardAccountView(acct, count), Data: data}
	if err = dashboard.Render(w, log, httpsec.NonceFromContext(ctx), page); err != nil {
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

// Empty optional HTML form controls are omitted; API parsing stays strict.
func normalizeWorkflowGapQueueForm(ctx context.Context, r *http.Request) *http.Request {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || q.Get("dimension") != "condition_gap_queue" {
		return r
	}
	for _, key := range []string{"gap_status", "owner"} {
		if values := q[key]; len(values) == 1 && values[0] == "" {
			q.Del(key)
		}
	}
	clone := r.Clone(ctx)
	u := *r.URL
	clone.URL = &u
	clone.URL.RawQuery = q.Encode()
	return clone
}
func workflowGapQueueGroupLinks(path string, opts api.OperationWorkflowPerformanceOptions, result api.OperationWorkflowPerformanceInstancesResponse, entry api.OperationWorkflowPerformanceInstance) []dashboard.WorkflowGapProgressGroupLink {
	links := []dashboard.WorkflowGapProgressGroupLink{}
	if entry.GapQueuePriority == nil || result.NewMissingProgress == nil {
		return links
	}
	version := entry.State.ContractVersion
	if version == 0 {
		version = 1
	}
	for _, group := range result.NewMissingProgress.Groups {
		if group.ContractVersion != version || group.State != entry.State.State {
			continue
		}
		for _, guidance := range entry.GapQueuePriority.Guidance {
			if guidance.Status != group.GapStatus || guidance.Expectation.Definition.RefreshOwner != group.Owner {
				continue
			}
			selector := api.OperationWorkflowPerformanceGroup{Dimension: "condition_gap_owner", ContractVersion: group.ContractVersion, State: group.State, Owner: group.Owner, Unassigned: group.Unassigned, GapStatus: group.GapStatus}
			links = append(links, dashboard.WorkflowGapProgressGroupLink{OperationWorkflowGapProgressGroup: group, ContributorsURL: workflowPerformanceContributorURL(path, opts, "ongoing", result.CohortToken, selector)})
			break
		}
	}
	return links
}

func workflowPerformanceContributorViews(data dashboard.WorkflowPerformanceData, opts api.OperationWorkflowPerformanceInstanceOptions, result api.OperationWorkflowPerformanceInstancesResponse) []dashboard.WorkflowPerformanceContributor {
	views := []dashboard.WorkflowPerformanceContributor{}
	for _, entry := range result.Items {
		history := url.Values{"scope": {opts.Scope}, "tenant_id": {entry.PlatformTenantID}, "subject_type": {entry.Subject.Type}, "subject_id": {entry.Subject.ID}, "workflow": {opts.Workflow}, "workflow_instance_id": {entry.State.InstanceID}}
		contributor := dashboard.WorkflowPerformanceContributor{Entry: entry, HistoryURL: data.ListURL + "?" + history.Encode() + "#workflows", OperationURL: data.ListURL + "/" + url.PathEscape(entry.OperationID)}
		if entry.DecisionEvidence != nil {
			contributor.DecisionOperationURL = data.ListURL + "/" + url.PathEscape(entry.DecisionEvidence.OperationID)
		}
		contributor.GapGroups = workflowGapQueueGroupLinks(data.SummaryURL, opts.OperationWorkflowPerformanceOptions, result, entry)
		views = append(views, contributor)
	}
	return views
}
