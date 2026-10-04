package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cronexpr"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) listAutomations(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	response, err := s.automationList(r.Context(), app, account)
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}
func (s *server) getAutomation(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	response, err := s.automationList(r.Context(), app, account)
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	for _, definition := range response.Automations {
		if definition.Name == r.PathValue("name") {
			writeJSON(w, http.StatusOK, definition)
			return
		}
	}
	s.notFound(w, "no such automation")
}
func (s *server) automationList(ctx context.Context, app state.App, account state.Account) (api.ListAutomationsResponse, error) {
	response := api.ListAutomationsResponse{AppSlug: app.Slug, RuntimeEnabled: s.workflowRuntimeEnabled, UnavailableReason: workflowScheduleUnavailableReason(s.workflowRuntimeEnabled, app, account), MaxDefinitions: account.Plan.WorkflowMaxPerApp(), Automations: []api.AutomationResponse{}}
	store, ok := s.store.(state.AutomationStore)
	if !ok {
		return response, errors.New("automation storage unavailable")
	}
	records, err := store.ListAutomations(ctx, app.ID)
	if err != nil {
		return response, err
	}
	dep, err := s.store.LiveDeploymentForScope(ctx, app.ID, "default")
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return response, err
	}
	if errors.Is(err, state.ErrNotFound) && response.UnavailableReason == "" {
		response.UnavailableReason = "deployment_unavailable"
	}
	response.Automations, err = automationResponses(dep.Workflows, records)
	return response, err
}
func automationResponses(manifest json.RawMessage, records []state.Automation) ([]api.AutomationResponse, error) {
	var definitions []api.WorkflowSpec
	if len(manifest) > 0 {
		if err := json.Unmarshal(manifest, &definitions); err != nil {
			return nil, err
		}
	}
	byName := make(map[string]api.AutomationResponse)
	for _, definition := range definitions {
		byName[definition.Name] = api.AutomationResponse{Name: definition.Name, Source: "manifest", Draft: definition, Published: &definition, Enabled: definition.Trigger == nil || definition.Trigger.Enabled == nil || *definition.Trigger.Enabled}
	}
	for _, record := range records {
		item := byName[record.Name]
		item.Name = record.Name
		item.Version = record.Version
		item.UpdatedAt = &record.UpdatedAt
		if err := json.Unmarshal(record.Draft, &item.Draft); err != nil {
			return nil, err
		}
		if len(record.Published) > 0 {
			var published api.WorkflowSpec
			if err := json.Unmarshal(record.Published, &published); err != nil {
				return nil, err
			}
			if published.Trigger != nil && published.Trigger.Type != "manual" {
				enabled := record.Enabled
				published.Trigger.Enabled = &enabled
			}
			item.Published = &published
			item.PublishedVersion = record.PublishedVersion
			item.Enabled = record.Enabled
			item.Source = "dashboard"
		} else if item.Source == "" {
			item.Source = "draft"
			item.Enabled = record.Enabled
		}
		byName[record.Name] = item
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]api.AutomationResponse, 0, len(names))
	for _, name := range names {
		result = append(result, byName[name])
	}
	return result, nil
}
func (s *server) saveAutomationDraft(w http.ResponseWriter, r *http.Request, account state.Account) {
	var body api.SaveAutomationDraftRequest
	if !decodeAutomationBody(w, r, &body) {
		return
	}
	raw, err := json.Marshal(body.Definition)
	if err != nil {
		writeAutomationError(w, state.ErrAutomationInvalid)
		return
	}
	s.applyAutomationMutation(w, r, account, state.AutomationMutation{Action: "save", ExpectedVersion: body.ExpectedVersion, Draft: raw})
}
func (s *server) publishAutomation(w http.ResponseWriter, r *http.Request, account state.Account) {
	var body api.PublishAutomationRequest
	if !decodeAutomationBody(w, r, &body) {
		return
	}
	s.applyAutomationMutation(w, r, account, state.AutomationMutation{Action: "publish", ExpectedVersion: body.ExpectedVersion, TakeOverManifest: body.TakeOverManifest})
}
func (s *server) setAutomationEnabled(w http.ResponseWriter, r *http.Request, account state.Account) {
	var body api.SetAutomationEnabledRequest
	if !decodeAutomationBody(w, r, &body) {
		return
	}
	if body.Enabled == nil {
		api.WriteProblem(w, api.ErrValidation("enabled is required and must be a boolean"))
		return
	}
	s.applyAutomationMutation(w, r, account, state.AutomationMutation{Action: "enable", ExpectedVersion: body.ExpectedVersion, Enabled: *body.Enabled})
}
func (s *server) deleteAutomation(w http.ResponseWriter, r *http.Request, account state.Account) {
	version, err := strconv.ParseInt(r.URL.Query().Get("expected_version"), 10, 64)
	if err != nil || version <= 0 {
		api.WriteProblem(w, api.ErrValidation("expected_version must be a positive integer"))
		return
	}
	restore := false
	if value := r.URL.Query().Get("restore_manifest"); value != "" {
		restore, err = strconv.ParseBool(value)
	}
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("restore_manifest must be a boolean"))
		return
	}
	s.applyAutomationMutation(w, r, account, state.AutomationMutation{Action: "delete", ExpectedVersion: version, RestoreManifest: restore})
}
func (s *server) applyAutomationMutation(w http.ResponseWriter, r *http.Request, account state.Account, mutation state.AutomationMutation) {
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.AutomationStore)
	if !ok {
		writeAutomationError(w, errors.New("automation storage unavailable"))
		return
	}
	record, err := store.MutateAutomation(r.Context(), app.ID, r.PathValue("name"), mutation)
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	if mutation.Action == "delete" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Return this mutation's version, even if another writer changes it immediately.
	dep, err := s.store.LiveDeploymentForScope(r.Context(), app.ID, "default")
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		writeAutomationError(w, err)
		return
	}
	responses, err := automationResponses(dep.Workflows, []state.Automation{record})
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	for _, response := range responses {
		if response.Name == record.Name {
			writeJSON(w, http.StatusOK, response)
			return
		}
	}
}
func (s *server) validateAutomation(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	var body api.ValidateAutomationRequest
	if !decodeAutomationBody(w, r, &body) {
		return
	}
	response := api.ValidateAutomationResponse{Issues: []string{}, StepOrder: []string{}}
	order, err := s.checkAutomationDefinition(r.Context(), app.ID, body.Definition, account.Plan)
	if err != nil && !errors.Is(err, state.ErrAutomationInvalid) {
		writeAutomationError(w, err)
		return
	}
	if err != nil {
		response.Issues = append(response.Issues, err.Error())
	} else {
		response.Valid = true
		response.StepOrder = order
	}
	if response.Valid && body.Definition.Trigger != nil && body.Definition.Trigger.Type == "schedule" {
		trigger := body.Definition.Trigger
		schedule, err := cronexpr.Parse(trigger.Schedule, trigger.Timezone)
		if err == nil {
			response.NextFireAt = schedule.Next(time.Now().UTC()).UTC().Format(time.RFC3339)
		}
	}
	writeJSON(w, http.StatusOK, response)
}
func (s *server) checkAutomationDefinition(ctx context.Context, appID string, spec api.WorkflowSpec, plan api.Plan) ([]string, error) {
	order, err := api.ValidateWorkflowDAG(spec, plan)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", state.ErrAutomationInvalid, err)
	}
	if store, ok := s.store.(state.WorkflowOutboundStore); ok {
		err = store.ValidateWorkflowOutboundBindings(ctx, appID, spec)
	}
	return order, err
}
func decodeAutomationBody(w http.ResponseWriter, r *http.Request, body any) bool {
	if err := decodeJSONSized(r, body, api.AutomationDefinitionMaxBytes); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			api.WriteProblem(w, api.ErrRequestBodyTooLarge(api.AutomationDefinitionMaxBytes, api.AutomationDefinitionMaxBytes+1))
		} else {
			api.WriteProblem(w, api.ErrValidation(err.Error()))
		}
		return false
	}
	return true
}
func writeAutomationError(w http.ResponseWriter, err error) {
	if problem := api.AsProblem(err); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	var quota *state.AutomationQuotaError
	switch {
	case errors.As(err, &quota):
		api.WriteProblem(w, api.ErrAutomationDefinitionsQuota(quota.Plan, quota.Limit, quota.Observed))
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such automation"))
	case errors.Is(err, state.ErrAutomationVersionConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeAutomationVersionConflict, "Automation changed", err.Error()))
	case errors.Is(err, state.ErrAutomationOwnershipConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeAutomationOwnershipConflict, "Confirm YAML ownership change", err.Error()))
	case errors.Is(err, state.ErrAutomationInvalid):
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeAutomationInvalid, "Invalid automation", err.Error()))
	case errors.Is(err, state.ErrWorkflowEventTargetUnavailable):
		api.WriteProblem(w, api.ErrWorkflowDeploymentUnavailable())
	default:
		api.WriteProblem(w, api.ErrCapacity("failed to read or update automation"))
	}
}
