package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cronexpr"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) listWorkflowSchedules(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	response := api.ListWorkflowSchedulesResponse{RuntimeEnabled: s.workflowRuntimeEnabled,
		UnavailableReason: workflowScheduleUnavailableReason(s.workflowRuntimeEnabled, app, account),
		Schedules:         []api.WorkflowScheduleResponse{}}
	deployment, err := s.store.LiveDeploymentForScope(r.Context(), app.ID, "default")
	if errors.Is(err, state.ErrNotFound) {
		writeJSON(w, http.StatusOK, response)
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("failed to read workflow deployment"))
		return
	}
	if store, ok := s.store.(state.AutomationStore); ok {
		deployment.Workflows, err = store.EffectiveWorkflowDefinitions(r.Context(), app.ID, deployment.Workflows)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("failed to read published automations"))
			return
		}
	}
	store, ok := s.store.(state.WorkflowScheduleStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow schedule inspection unavailable"))
		return
	}
	cursors, err := store.ListWorkflowScheduleCursors(r.Context(), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("failed to read workflow schedules"))
		return
	}
	response.Schedules, err = workflowScheduleResponses(deployment, cursors, time.Now().UTC())
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("deployed workflow schedules are invalid"))
		return
	}
	if response.UnavailableReason != "" {
		for i := range response.Schedules {
			response.Schedules[i].NextFireAt = ""
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func workflowScheduleUnavailableReason(runtimeEnabled bool, app state.App, account state.Account) string {
	switch {
	case !runtimeEnabled:
		return "runtime_disabled"
	case !account.Active():
		return "account_inactive"
	case !account.Plan.WorkflowsAllowed():
		return "plan_not_allowed"
	case app.MaintenanceMode:
		return "maintenance"
	case app.PlatformTenantRequired:
		return "tenant_required"
	default:
		return ""
	}
}

func workflowScheduleResponses(deployment state.Deployment, cursors []state.WorkflowScheduleCursor, now time.Time) ([]api.WorkflowScheduleResponse, error) {
	var definitions []api.WorkflowSpec
	if len(deployment.Workflows) > 0 {
		if err := json.Unmarshal(deployment.Workflows, &definitions); err != nil {
			return nil, err
		}
	}
	byName := make(map[string]state.WorkflowScheduleCursor, len(cursors))
	for _, cursor := range cursors {
		byName[cursor.WorkflowName] = cursor
	}
	result := make([]api.WorkflowScheduleResponse, 0)
	for _, definition := range definitions {
		trigger := definition.Trigger
		if trigger == nil || trigger.Type != "schedule" {
			continue
		}
		schedule, err := cronexpr.Parse(trigger.Schedule, trigger.Timezone)
		if err != nil {
			return nil, err
		}
		timezone, _ := cronexpr.NormalizeTimezone(trigger.Timezone)
		item := api.WorkflowScheduleResponse{WorkflowName: definition.Name, DeploymentID: deployment.ID,
			Schedule: trigger.Schedule, Timezone: timezone, Overlap: trigger.Overlap,
			Enabled: trigger.Enabled == nil || *trigger.Enabled}
		if item.Overlap == "" {
			item.Overlap = "skip"
		}
		if item.Enabled {
			item.NextFireAt = schedule.Next(now).UTC().Format(time.RFC3339)
		}
		if cursor, exists := byName[definition.Name]; exists {
			item.LastEvaluatedAt = cursor.LastEvaluatedAt.UTC().Format(time.RFC3339)
			item.LastStatus, item.LastRunID = cursor.Status, cursor.LastRunID
			if cursor.ScheduledFor != nil {
				item.LastScheduledFor = cursor.ScheduledFor.UTC().Format(time.RFC3339)
			}
		}
		result = append(result, item)
	}
	return result, nil
}
