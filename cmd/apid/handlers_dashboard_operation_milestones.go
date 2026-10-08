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
		data.Milestones = append(data.Milestones, dashboard.CustomerOperationMilestone{ID: fact.ID, Name: fact.Name, OperationID: fact.OperationID,
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
				data.Workflows[index].Steps = append(data.Workflows[index].Steps, dashboard.CustomerOperationWorkflowStep{
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
			FromState: entry.FromState, State: entry.State, Revision: entry.Revision,
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
	for i := range data.Workflows {
		workflow := &data.Workflows[i]
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
