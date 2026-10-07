package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type workflowSchedulePreviewOptions struct {
	evaluationAt time.Time
	since        *time.Time
	count        int
}

func parseWorkflowSchedulePreviewOptions(r *http.Request, observedAt time.Time) (workflowSchedulePreviewOptions, error) {
	options := workflowSchedulePreviewOptions{evaluationAt: observedAt, count: api.WorkflowSchedulePreviewDefaultCount}
	query := r.URL.Query()
	if raw := query.Get("at"); raw != "" {
		value, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil || value.Before(observedAt.AddDate(-5, 0, 0)) || value.After(observedAt.AddDate(5, 0, 0)) {
			return options, errors.New("at must be an RFC3339 timestamp within five years of now")
		}
		options.evaluationAt = value.UTC()
	}
	if raw := query.Get("since"); raw != "" {
		value, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil || value.After(options.evaluationAt) || value.Before(options.evaluationAt.AddDate(-5, 0, 0)) {
			return options, errors.New("since must be an RFC3339 timestamp no later than at")
		}
		value = value.UTC()
		options.since = &value
	}
	if raw := query.Get("count"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > api.WorkflowSchedulePreviewMaxCount {
			return options, errors.New("count must be between 1 and 20")
		}
		options.count = value
	}
	return options, nil
}

func (s *server) previewWorkflowSchedule(w http.ResponseWriter, r *http.Request, account state.Account) {
	ctx, cancel := context.WithTimeout(r.Context(), api.WorkflowSchedulePreviewReadTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	if app.PlatformTenantRequired {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Tenant schedule preview required", "preview a tenant-configurable schedule through the platform-tenant-self route"))
		return
	}
	options, ok := workflowSchedulePreviewRequest(w, r)
	if !ok {
		return
	}
	deployment, trigger, ok := s.loadWorkflowScheduleForPreview(w, r, app, r.PathValue("name"))
	if !ok {
		return
	}
	store, ok := s.store.(state.WorkflowScheduleStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow schedule preview unavailable"))
		return
	}
	cursors, err := store.ListWorkflowScheduleCursors(r.Context(), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("failed to read workflow schedule state"))
		return
	}
	var cursor *state.WorkflowScheduleCursor
	for i := range cursors {
		if cursors[i].WorkflowName == r.PathValue("name") {
			cursor = &cursors[i]
			break
		}
	}
	result, err := buildWorkflowSchedulePreview(r.PathValue("name"), deployment, trigger, cursor, options)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("failed to build workflow schedule preview"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}

func (s *server) previewPlatformTenantSelfWorkflowSchedule(w http.ResponseWriter, r *http.Request, account state.Account) {
	ctx, cancel := context.WithTimeout(r.Context(), api.WorkflowSchedulePreviewReadTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	tenantID, app, ok := s.platformTenantEventApp(w, r, account)
	if !ok {
		return
	}
	options, ok := workflowSchedulePreviewRequest(w, r)
	if !ok {
		return
	}
	store, ok := s.store.(state.TenantWorkflowScheduleStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("tenant workflow schedule preview unavailable"))
		return
	}
	schedules, err := store.ListTenantWorkflowSchedules(r.Context(), account.ID, tenantID, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "workflow schedule not found")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("failed to read tenant workflow schedules"))
		return
	}
	name := r.PathValue("name")
	var selected *state.TenantWorkflowSchedule
	for i := range schedules {
		if schedules[i].WorkflowName == name {
			selected = &schedules[i]
			break
		}
	}
	if selected == nil {
		s.notFound(w, "tenant-configurable workflow schedule not found")
		return
	}
	deployment := state.Deployment{ID: selected.DeploymentID}
	result, err := buildWorkflowSchedulePreview(name, deployment, selected.EffectiveTrigger, selected.Cursor, options)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("failed to build tenant workflow schedule preview"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}

func workflowSchedulePreviewRequest(w http.ResponseWriter, r *http.Request) (workflowSchedulePreviewOptions, bool) {
	observedAt := time.Now().UTC()
	options, err := parseWorkflowSchedulePreviewOptions(r, observedAt)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return options, false
	}
	return options, true
}

func (s *server) loadWorkflowScheduleForPreview(w http.ResponseWriter, r *http.Request, app state.App, name string) (state.Deployment, api.WorkflowTriggerSpec, bool) {
	deployment, err := s.store.LiveDeploymentForScope(r.Context(), app.ID, "default")
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "workflow schedule not found")
		return state.Deployment{}, api.WorkflowTriggerSpec{}, false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("failed to read workflow deployment"))
		return state.Deployment{}, api.WorkflowTriggerSpec{}, false
	}
	if store, ok := s.store.(state.AutomationStore); ok {
		deployment.Workflows, err = store.EffectiveWorkflowDefinitions(r.Context(), app.ID, deployment.Workflows)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("failed to read published automations"))
			return state.Deployment{}, api.WorkflowTriggerSpec{}, false
		}
	}
	var definitions []api.WorkflowSpec
	if err := json.Unmarshal(deployment.Workflows, &definitions); err != nil {
		api.WriteProblem(w, api.ErrCapacity("deployed workflow schedules are invalid"))
		return state.Deployment{}, api.WorkflowTriggerSpec{}, false
	}
	for _, definition := range definitions {
		if definition.Name == name && definition.Trigger != nil && definition.Trigger.Type == "schedule" {
			return deployment, *definition.Trigger, true
		}
	}
	s.notFound(w, "workflow schedule not found")
	return state.Deployment{}, api.WorkflowTriggerSpec{}, false
}

func buildWorkflowSchedulePreview(name string, deployment state.Deployment, trigger api.WorkflowTriggerSpec,
	cursor *state.WorkflowScheduleCursor, options workflowSchedulePreviewOptions) (api.WorkflowSchedulePreviewResponse, error) {
	observedAt := time.Now().UTC()
	var lastEvaluated *time.Time
	cursorMatches := false
	if cursor != nil {
		value := cursor.LastEvaluatedAt
		lastEvaluated = &value
		cursorMatches = state.WorkflowScheduleCursorMatches(*cursor, deployment.ID, trigger)
	}
	return api.BuildWorkflowSchedulePreview(api.WorkflowSchedulePreviewInput{
		WorkflowName: name, DeploymentID: deployment.ID, Trigger: trigger,
		Enabled:    trigger.Enabled == nil || *trigger.Enabled,
		ObservedAt: observedAt, EvaluationAt: options.evaluationAt,
		LastEvaluated: lastEvaluated, CursorMatches: cursorMatches,
		SinceOverride: options.since, Count: options.count,
	})
}
