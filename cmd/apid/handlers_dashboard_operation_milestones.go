// adr: 640
package main

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"time"
)

func populateDashboardOperationMilestones(r *http.Request, log *slog.Logger, acct state.Account, app state.App, candidate any, data *dashboard.CustomerOperationsData) {
	if data.Detail == nil && data.SubjectType == "" {
		return
	}
	data.MilestonesVisible = true
	store, ok := candidate.(state.OperationMilestoneStore)
	if !ok {
		data.MilestonesError = "Business milestones are unavailable."
		return
	}
	opts := api.OperationMilestoneListOptions{AppID: app.ID, Scope: data.Scope, TenantID: data.TenantID, Cursor: r.URL.Query().Get("milestone_cursor"), Limit: api.OperationHistoryPageDefault}
	if data.Detail != nil {
		opts.OperationID = data.Detail.ID
	} else {
		opts.SubjectType, opts.SubjectID = data.SubjectType, data.SubjectID
		opts.Workflow, opts.WorkflowInstanceID = r.URL.Query().Get("workflow"), r.URL.Query().Get("workflow_instance_id")
		opts.WorkflowStateCursor = r.URL.Query().Get("workflow_state_cursor")
		opts.WorkflowStaleOnly = data.StaleOnly
	}
	page, err := store.ListAccountOperationMilestones(r.Context(), acct.ID, opts)
	if err != nil {
		data.MilestonesError = "Business history could not be loaded. Refresh without the history cursors."
		log.Warn("dashboard operations: read milestones", "app_id", app.ID, "err", err)
		return
	}
	for _, fact := range page.Milestones {
		decision, _ := api.ParseOperationBusinessDecision(fact.Payload)
		compensation, _ := api.ParseOperationBusinessCompensation(fact.Payload)
		sourceEffectURL := ""
		if compensation != nil {
			sourceEffectURL = dashboardCustomerOperationsURL(app.Slug) + "/" + url.PathEscape(compensation.SourceEffect.OperationID)
		}
		effect, _ := api.ParseOperationBusinessEffect(fact.Payload)
		invariant, _ := api.ParseOperationBusinessInvariant(fact.Payload)
		reconciliation, _ := api.ParseOperationWorkflowReconciliation(fact.Payload)
		data.Milestones = append(data.Milestones, dashboard.CustomerOperationMilestone{Compensation: compensation, SourceEffectURL: sourceEffectURL, Effect: effect, Invariant: invariant, Decision: decision, Reconciliation: reconciliation, ID: fact.ID, Name: fact.Name, OperationID: fact.OperationID,
			OperationURL: dashboardCustomerOperationsURL(app.Slug) + "/" + url.PathEscape(fact.OperationID), Payload: string(fact.Payload),
			OccurredAt: dashboardJobsTime(fact.OccurredAt), CreatedAt: dashboardJobsTime(fact.CreatedAt), WorkflowMapped: len(fact.WorkflowSteps) > 0})
		for _, step := range fact.WorkflowSteps {
			index := -1
			for i := range data.Workflows {
				if data.Workflows[i].Name == step.Workflow && data.Workflows[i].InstanceID == step.InstanceID {
					index = i
					break
				}
			}
			if index == -1 {
				data.Workflows = append(data.Workflows, dashboard.CustomerOperationWorkflow{Name: step.Workflow, Title: step.Title, InstanceID: step.InstanceID})
				index = len(data.Workflows) - 1
			}
			seen := false
			for _, prior := range data.Workflows[index].Steps {
				if prior.OperationID == fact.OperationID && prior.MilestoneID == fact.ID {
					seen = true
					break
				}
			}
			if !seen {
				data.Workflows[index].Steps = append(data.Workflows[index].Steps, dashboard.CustomerOperationWorkflowStep{Compensation: compensation, SourceEffectURL: sourceEffectURL, Effect: effect, Invariant: invariant, Decision: decision, Reconciliation: reconciliation,
					Position: step.Position, Label: step.Label, MilestoneID: fact.ID, OperationID: fact.OperationID,
					OperationURL: dashboardCustomerOperationsURL(app.Slug) + "/" + url.PathEscape(fact.OperationID),
					Payload:      string(fact.Payload),
					OccurredAt:   dashboardJobsTime(fact.OccurredAt), PublishedAt: dashboardJobsTime(fact.CreatedAt),
				})
			}
		}
	}
	for _, state := range page.WorkflowStates {
		index := -1
		for i := range data.Workflows {
			if data.Workflows[i].Name == state.Workflow && data.Workflows[i].InstanceID == state.InstanceID {
				index = i
				break
			}
		}
		if index == -1 {
			data.Workflows = append(data.Workflows, dashboard.CustomerOperationWorkflow{Name: state.Workflow, Title: state.Workflow, InstanceID: state.InstanceID})
			index = len(data.Workflows) - 1
		}
		data.Workflows[index].State = state.State
		data.Workflows[index].Terminal = state.Terminal
		data.Workflows[index].OutcomeCode = state.OutcomeCode
		data.Workflows[index].OutcomeDescription = state.OutcomeDescription
		data.Workflows[index].DeadlineAt = state.DeadlineAt
		data.Workflows[index].Overdue = state.Overdue
		data.Workflows[index].OverdueSeconds = state.OverdueSeconds
		data.Workflows[index].Stale = state.Stale
		data.Workflows[index].StateOccurredAt = dashboardJobsTime(state.OccurredAt)
		if state.StaleAfterSeconds > 0 {
			data.Workflows[index].StateStaleAfter = (time.Duration(state.StaleAfterSeconds) * time.Second).String()
		}
		data.Workflows[index].StateUpdatedAt = dashboardJobsTime(state.UpdatedAt)
		data.Workflows[index].StateRevision = state.Revision
	}
	for _, entry := range page.WorkflowStateHistory {
		index := -1
		for i := range data.Workflows {
			if data.Workflows[i].Name == entry.Workflow && data.Workflows[i].InstanceID == entry.InstanceID {
				index = i
				break
			}
		}
		if index == -1 {
			data.Workflows = append(data.Workflows, dashboard.CustomerOperationWorkflow{Name: entry.Workflow, Title: entry.Workflow, InstanceID: entry.InstanceID})
			index = len(data.Workflows) - 1
		}
		data.Workflows[index].StateHistory = append(data.Workflows[index].StateHistory, dashboard.CustomerOperationWorkflowStateHistory{
			ID: entry.ID, OperationID: entry.OperationID, OperationURL: dashboardCustomerOperationsURL(app.Slug) + "/" + url.PathEscape(entry.OperationID),
			ResolutionVerifications: entry.ResolutionVerifications, Blockers: entry.Blockers, DependsOn: entry.DependsOn, DependenciesOnly: entry.DependenciesOnly, BlockerResolutions: entry.BlockerResolutions, OutcomeCode: entry.OutcomeCode, OutcomeDescription: entry.OutcomeDescription, OutcomeOnly: entry.OutcomeOnly, DeadlineAt: entry.DeadlineAt, DeadlineOnly: entry.DeadlineOnly, BlockersOnly: entry.BlockersOnly, FromState: entry.FromState, State: entry.State, Revision: entry.Revision,
			OccurredAt: dashboardJobsTime(entry.OccurredAt), PublishedAt: dashboardJobsTime(entry.PublishedAt),
		})
	}
	if opts.WorkflowStaleOnly {
		staleWorkflows := data.Workflows[:0]
		for _, workflow := range data.Workflows {
			if workflow.Stale {
				staleWorkflows = append(staleWorkflows, workflow)
			}
		}
		data.Workflows = staleWorkflows
	}
	sort.Slice(data.Workflows, func(i, j int) bool {
		if data.Workflows[i].Name != data.Workflows[j].Name {
			return data.Workflows[i].Name < data.Workflows[j].Name
		}
		return data.Workflows[i].InstanceID < data.Workflows[j].InstanceID
	})
	if instance := page.WorkflowInstance; instance != nil {
		found := false
		for i := range data.Workflows {
			if data.Workflows[i].Name == instance.Workflow && data.Workflows[i].InstanceID == instance.InstanceID {
				data.Workflows[i].Decision = instance.Decision
				found = true
			}
		}
		if !found && !opts.WorkflowStaleOnly {
			data.Workflows = append(data.Workflows, dashboard.CustomerOperationWorkflow{Name: instance.Workflow, Title: instance.Workflow, InstanceID: instance.InstanceID, Decision: instance.Decision})
		}
	}
	for i := range data.Workflows {
		workflow := &data.Workflows[i]
		if instance := page.WorkflowInstance; instance != nil && workflow.Name == instance.Workflow && workflow.InstanceID == instance.InstanceID {
			if instance.State != nil {
				workflow.SLA = instance.State.SLA
			}
			workflow.Bottlenecks = instance.Bottlenecks
			workflow.ResolutionVerifications = instance.ResolutionVerifications
			workflow.AwaitingVerificationCount = instance.AwaitingVerificationCount
			workflow.ResolutionVerificationCount = instance.ResolutionVerificationCount
			workflow.Readiness = instance.Readiness
			if instance.Readiness != nil {
				labels := map[string]string{"state_unknown": "No retained reported state", "terminal": "Workflow is terminal", "transition_undeclared": "Transition is not declared", "from_state_mismatch": "Reported state differs from the proposed source", "revision_mismatch": "Reported revision has changed", "contract_version_mismatch": "Contract version differs", "application_blocked": "Application reports blockers for this Operation", "dependency_unmet": "Prerequisites remain unmet", "milestone_required": "Milestones must accompany this transition", "policy_evidence_required": "Matching business policy evidence must accompany this transition", "dependency_required": "Required prerequisite workflow links have not been reported", "invariant_evidence_required": "Passing invariant evidence must accompany this transition", "effect_evidence_required": "Confirmed business effect evidence must accompany this transition"}
				for _, readiness := range instance.Readiness.Items {
					item := dashboard.CustomerOperationTransitionReadiness{OperationWorkflowTransitionReadiness: readiness}
					for _, reason := range readiness.Reasons {
						item.ReasonLabels = append(item.ReasonLabels, labels[reason])
					}
					workflow.ReadinessItems = append(workflow.ReadinessItems, item)
				}
			}
			workflow.DependencyTrace = instance.DependencyTrace
			if instance.DependencyTrace != nil {
				tenant := data.TenantID
				if instance.State != nil {
					tenant = instance.State.PlatformTenantID
				}
				for _, finding := range instance.DependencyTrace.Findings {
					view := dashboard.CustomerOperationDependencyFinding{OperationWorkflowDependencyFinding: finding, Title: map[string]string{"reported_blockers": "Application-reported blockers", "state_unknown": "No retained state", "outcome_unknown": "Outcome not reported", "outcome_mismatch": "Required outcome does not match", "awaiting_application": "Waiting for application progress", "state_stale": "Stale reported state", "deadline_overdue": "Business deadline passed", "cycle": "Cyclic dependency chain", "trace_limit": "Trace limit reached"}[finding.Kind]}
					for _, reference := range finding.Path {
						q := url.Values{"scope": {data.Scope}, "tenant_id": {tenant}, "subject_type": {reference.SubjectType}, "subject_id": {reference.SubjectID}, "workflow": {reference.Workflow}, "workflow_instance_id": {reference.InstanceID}}
						view.Steps = append(view.Steps, dashboard.CustomerOperationDependencyTraceStep{OperationWorkflowDependency: reference, URL: dashboardCustomerOperationsURL(app.Slug) + "?" + q.Encode() + "#workflows"})
					}
					workflow.DependencyFindings = append(workflow.DependencyFindings, view)
				}
			}
			workflow.DependencyImpact = instance.DependencyImpact
			if instance.DependencyImpact != nil {
				for _, dependent := range instance.DependencyImpact.Items {
					tenant := dependent.State.PlatformTenantID
					if tenant == "" {
						tenant = data.TenantID
					}
					q := url.Values{"scope": {data.Scope}, "tenant_id": {tenant}, "subject_type": {dependent.Subject.Type}, "subject_id": {dependent.Subject.ID}, "workflow": {dependent.State.Workflow}, "workflow_instance_id": {dependent.State.InstanceID}}
					workflow.DependentWorkflows = append(workflow.DependentWorkflows, dashboard.CustomerOperationDependentWorkflow{OperationWorkflowDependentInstance: dependent, URL: dashboardCustomerOperationsURL(app.Slug) + "?" + q.Encode() + "#workflows"})
				}
			}
			for _, related := range instance.RelatedWorkflows {
				q := url.Values{}
				q.Set("scope", data.Scope)
				if instance.State != nil {
					q.Set("tenant_id", instance.State.PlatformTenantID)
				}
				q.Set("subject_type", related.Dependency.SubjectType)
				q.Set("subject_id", related.Dependency.SubjectID)
				q.Set("workflow", related.Dependency.Workflow)
				q.Set("workflow_instance_id", related.Dependency.InstanceID)
				workflow.RelatedWorkflows = append(workflow.RelatedWorkflows, dashboard.CustomerOperationWorkflowRelation{OperationWorkflowRelatedInstance: related, URL: dashboardCustomerOperationsURL(app.Slug) + "?" + q.Encode() + "#workflows"})
			}
		}
		query := r.URL.Query()
		query.Set("workflow", workflow.Name)
		query.Set("workflow_instance_id", workflow.InstanceID)
		query.Del("milestone_cursor")
		query.Del("workflow_state_cursor")
		workflow.HistoryURL = r.URL.Path + "?" + query.Encode() + "#workflows"
		workflow.Selected = opts.Workflow == workflow.Name && opts.WorkflowInstanceID == workflow.InstanceID
		sort.SliceStable(workflow.StateHistory, func(a, b int) bool { return workflow.StateHistory[a].Revision < workflow.StateHistory[b].Revision })
		sort.SliceStable(data.Workflows[i].Steps, func(a, b int) bool {
			left, right := data.Workflows[i].Steps[a], data.Workflows[i].Steps[b]
			if left.Position != right.Position {
				return left.Position < right.Position
			}
			return left.OccurredAt < right.OccurredAt
		})
	}
	if page.NextCursor != "" {
		query := r.URL.Query()
		query.Set("milestone_cursor", page.NextCursor)
		data.NextMilestonesURL = r.URL.Path + "?" + query.Encode() + "#milestones"
	}
	if page.NextWorkflowStateCursor != "" {
		query := r.URL.Query()
		query.Set("workflow_state_cursor", page.NextWorkflowStateCursor)
		data.NextWorkflowStateURL = r.URL.Path + "?" + query.Encode() + "#workflows"
	}
}
