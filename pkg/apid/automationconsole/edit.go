package automationconsole

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/automationchecks"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func (s *Service) edit(w http.ResponseWriter, r *http.Request, scope Scope, data dashboard.AutomationConsoleData) {
	spec, err := definition(data)
	if err != nil {
		s.invalid(w, r, scope, data, "Enter a valid JSON definition with the matching automation name.")
		return
	}
	action := r.PathValue("action")
	operation := Operation{Kind: action, Name: data.Name}
	switch action {
	case "save":
		operation.Body = api.SaveAutomationDraftRequest{ExpectedVersion: data.Version, Definition: spec}
	case "validate":
		operation.Body = api.ValidateAutomationRequest{Definition: spec}
	case "simulate":
		body, err := sample(data, spec)
		if err != nil {
			s.invalid(w, r, scope, data, "Enter valid sample and mock JSON before simulating.")
			return
		}
		operation.Body = body
	}
	response := s.Host.Request(r, scope, operation)
	if !success(response) {
		s.fail(w, r, scope, data, response)
		return
	}
	if action == "save" {
		s.afterWrite(w, r, scope, data, response, "Draft saved. Save test data separately to keep its sample and checks.")
		return
	}
	if action == "validate" {
		var result api.ValidateAutomationResponse
		if !read(response, &result) {
			s.fail(w, r, scope, data, badResponse())
			return
		}
		data.Validation = &result
	} else {
		var result api.SimulateAutomationResponse
		if !read(response, &result) {
			s.fail(w, r, scope, data, badResponse())
			return
		}
		data.Simulation = &result
	}
	s.render(w, r, scope, data, http.StatusOK)
}

func (s *Service) savedDraft(r *http.Request, scope Scope, data dashboard.AutomationConsoleData) (api.AutomationResponse, *Response) {
	response := s.Host.Request(r, scope, Operation{Kind: "get", Name: data.Name})
	var saved api.AutomationResponse
	if !read(response, &saved) {
		return saved, &response
	}
	spec, err := definition(data)
	expected, expectedErr := canonicalDefinition(spec)
	actual, actualErr := canonicalDefinition(saved.Draft)
	if err != nil || expectedErr != nil || actualErr != nil || saved.Version != data.Version || !automationchecks.EqualJSON(expected, actual) {
		problem := api.ErrValidation("Save your current definition and reload the draft before checking or publishing.")
		raw, _ := json.Marshal(problem)
		response = Response{Status: http.StatusConflict, Body: raw}
		return saved, &response
	}
	return saved, nil
}

// JSONB storage can reorder nested input/filter keys. Compare their values
// without converting large JSON numbers to float64 or changing array order.
func canonicalDefinition(spec api.WorkflowSpec) ([]byte, error) {
	raw, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func (s *Service) check(w http.ResponseWriter, r *http.Request, scope Scope, data dashboard.AutomationConsoleData) {
	saved, failure := s.savedDraft(r, scope, data)
	if failure != nil {
		s.fail(w, r, scope, data, *failure)
		return
	}
	if saved.TestSuite != nil && !savedTestInputsMatch(data, saved) {
		s.invalid(w, r, scope, data, "Save your current sample and test cases before checking or publishing.")
		return
	}
	var scenarios []api.AutomationPublishCheckScenario
	if err := decode(data.Scenarios, api.AutomationPublishCheckMaxBytes, &scenarios); err != nil || len(scenarios) == 0 {
		s.invalid(w, r, scope, data, "Add at least one JSON check scenario with expected step results.")
		return
	}
	for i := range scenarios {
		scenarios[i].Simulation.Definition = saved.Draft
	}
	response := s.Host.Request(r, scope, Operation{Kind: "check", Name: data.Name,
		Body: api.CheckAutomationPublicationRequest{ExpectedVersion: saved.Version, Scenarios: scenarios}})
	var checked api.CheckAutomationPublicationResponse
	if !read(response, &checked) {
		s.fail(w, r, scope, data, response)
		return
	}
	if checked.Receipt == "" {
		s.fail(w, r, scope, data, badResponse())
		return
	}
	data.CheckEvidence = &checked.Evidence
	if r.PathValue("action") == "check" {
		s.render(w, r, scope, data, http.StatusOK)
		return
	}
	if _, err := uuid.Parse(r.PostFormValue("action_key")); err != nil {
		s.invalid(w, r, scope, data, "Reload the page before publishing.")
		return
	}
	response = s.Host.Request(r, scope, Operation{Kind: "publish", Name: data.Name, Key: r.PostFormValue("action_key"),
		Body: api.PublishAutomationRequest{ExpectedVersion: saved.Version, CheckReceipt: checked.Receipt, TakeOverManifest: r.PostFormValue("take_over_manifest") == "yes"}})
	if !success(response) {
		s.fail(w, r, scope, data, response)
		return
	}
	s.afterWrite(w, r, scope, data, response, "Checks passed and the draft was published. Future runs use this revision.")
}

func (s *Service) enable(w http.ResponseWriter, r *http.Request, scope Scope, data dashboard.AutomationConsoleData) {
	enabled := r.PostFormValue("enabled") == "true"
	if r.PostFormValue("enabled") != "true" && r.PostFormValue("enabled") != "false" {
		s.invalid(w, r, scope, data, "Choose whether automatic starts should be paused or resumed.")
		return
	}
	response := s.Host.Request(r, scope, Operation{Kind: "enabled", Name: data.Name,
		Body: api.SetAutomationEnabledRequest{ExpectedVersion: data.Version, Enabled: &enabled}})
	if !success(response) {
		s.fail(w, r, scope, data, response)
		return
	}
	redirect(w, r, scope, data.Name, "", "")
}

func (s *Service) run(w http.ResponseWriter, r *http.Request, scope Scope, data dashboard.AutomationConsoleData) {
	if r.PostFormValue("confirm_run") != "yes" {
		s.invalid(w, r, scope, data, "Confirm that this run may perform real actions in your app and connected services.")
		return
	}
	if _, err := uuid.Parse(r.PostFormValue("action_key")); err != nil {
		s.invalid(w, r, scope, data, "Reload the page before starting a run.")
		return
	}
	var saved api.AutomationResponse
	response := s.Host.Request(r, scope, Operation{Kind: "get", Name: data.Name})
	if !read(response, &saved) {
		s.fail(w, r, scope, data, response)
		return
	}
	if saved.Published == nil {
		s.invalid(w, r, scope, data, "Publish the automation before starting a run.")
		return
	}
	body, err := sample(data, *saved.Published)
	if err != nil {
		s.invalid(w, r, scope, data, "Enter valid sample JSON before starting a run.")
		return
	}
	input := body.Input
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	response = s.Host.Request(r, scope, Operation{Kind: "run", Name: data.Name, Key: r.PostFormValue("action_key"), Body: input})
	var run api.WorkflowRunResponse
	if !read(response, &run) {
		s.fail(w, r, scope, data, response)
		return
	}
	redirect(w, r, scope, data.Name, "", run.ID)
}
