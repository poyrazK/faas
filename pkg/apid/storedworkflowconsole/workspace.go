package storedworkflowconsole

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/storedworkflowtemplates"
)

func (s *Service) workspace(r *http.Request, scope Scope) (dashboard.StoredWorkflowConsoleData, *Response) {
	data := dashboard.StoredWorkflowConsoleData{Slug: scope.Slug}
	var listing api.ListStoredWorkflowsResponse
	response := s.Host.Request(r, scope, Operation{Kind: "list"})
	if !read(response, &listing) {
		return data, &response
	}
	data.RuntimeEnabled, data.UnavailableReason = listing.RuntimeEnabled, listing.UnavailableReason
	data.RuntimeObserved = true
	for _, item := range listing.StoredWorkflows {
		data.Items = append(data.Items, dashboard.StoredWorkflowConsoleItem{Name: item.Name, Source: item.Source,
			URL: consoleURL(scope.Slug, item.Name, ""), Version: item.Version, PublishedVersion: item.PublishedVersion,
			Enabled: item.Published != nil && item.Enabled, FailurePaused: item.FailurePaused})
	}
	var policy api.StoredWorkflowPublishPolicy
	response = s.Host.Request(r, scope, Operation{Kind: "policy"})
	if !read(response, &policy) {
		return data, &response
	}
	data.PolicyMode = policy.Mode
	for _, item := range []dashboard.StoredWorkflowStarterItem{
		{Name: "scheduled-report", Description: "Collect and deliver a recurring report.", Setup: "Deploy report handlers, choose a timezone, then enable the schedule."},
		{Name: "webhook-enrichment", Description: "Enrich an incoming event and save it to your app.", Setup: "Deploy lookup and record handlers, then configure a verified webhook."},
		{Name: "approval-flow", Description: "Wait for approval, fulfill the request, or expire it.", Setup: "Deploy the approval handlers and connect an authorized approval event source."},
		{Name: "contact-sync", Description: "Read a provider contact and synchronize your app.", Setup: "Connect a managed integration, replace its placeholder ID, and deploy the sync handler."},
	} {
		item.URL = "/dashboard/apps/" + url.PathEscape(scope.Slug) + "/workflows/stored?template=" + url.QueryEscape(item.Name)
		data.Starters = append(data.Starters, item)
	}
	return data, nil
}

func setBundle(data *dashboard.StoredWorkflowConsoleData, bundle storedworkflowtemplates.Bundle) {
	data.Name, data.Definition = bundle.Definition.Name, pretty(bundle.Definition)
	data.Sample = prettySample(bundle.Sample)
	data.Scenarios = prettyScenarios(bundle.Scenarios)
}

func prettySample(sample api.SimulateStoredWorkflowRequest) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(pretty(sample)), &fields) != nil {
		return ""
	}
	delete(fields, "definition")
	return pretty(fields)
}
func prettyScenarios(scenarios []api.StoredWorkflowPublishCheckScenario) string {
	var fields []map[string]json.RawMessage
	if json.Unmarshal([]byte(pretty(scenarios)), &fields) != nil {
		return ""
	}
	for _, scenario := range fields {
		var sample api.SimulateStoredWorkflowRequest
		if json.Unmarshal(scenario["simulation"], &sample) != nil {
			return ""
		}
		scenario["simulation"] = json.RawMessage(prettySample(sample))
	}
	return pretty(fields)
}

func (s *Service) selectStoredWorkflow(r *http.Request, scope Scope, data *dashboard.StoredWorkflowConsoleData, name string) *Response {
	var storedWorkflow api.StoredWorkflowResponse
	response := s.Host.Request(r, scope, Operation{Kind: "get", Name: name})
	if !read(response, &storedWorkflow) {
		return &response
	}
	if bundle, err := storedworkflowtemplates.Load(name); err == nil {
		setBundle(data, bundle)
	}
	if data.Sample == "" {
		data.Sample = "{\"input\": {}, \"mock_outputs\": {}}"
		data.Scenarios = "[]"
	}
	if storedWorkflow.TestSuite != nil {
		data.Sample, data.Scenarios = pretty(storedWorkflow.TestSuite.Sample), pretty(storedWorkflow.TestSuite.Cases)
	}
	data.Name, data.Version, data.Definition = storedWorkflow.Name, storedWorkflow.Version, pretty(storedWorkflow.Draft)
	data.Published, data.Enabled, data.FailurePaused = storedWorkflow.Published != nil, storedWorkflow.Enabled, storedWorkflow.FailurePaused
	s.inspectStoredWorkflow(r, scope, data)
	return nil
}

func (s *Service) inspectStoredWorkflow(r *http.Request, scope Scope, data *dashboard.StoredWorkflowConsoleData) {
	var health api.StoredWorkflowHealthResponse
	if read(s.Host.Request(r, scope, Operation{Kind: "health", Name: data.Name}), &health) {
		data.Health = &health
	} else {
		data.HealthError = "Recent health is temporarily unavailable."
	}
	var runs api.ListWorkflowRunsResponse
	if read(s.Host.Request(r, scope, Operation{Kind: "runs", Name: data.Name}), &runs) {
		for _, run := range runs.Runs {
			if run.AppID == scope.AppID && run.WorkflowName == data.Name {
				data.Runs = append(data.Runs, runView(scope, run))
			}
		}
	} else {
		data.RunsError = "Run history is temporarily unavailable."
	}
}

func (s *Service) inspectRun(r *http.Request, scope Scope, data *dashboard.StoredWorkflowConsoleData, id string) *Response {
	run, failure := s.scopedRun(r, scope, data.Name, id)
	if failure != nil {
		return failure
	}
	view := runView(scope, run)
	data.Run = &view
	var steps api.ListWorkflowStepsResponse
	response := s.Host.Request(r, scope, Operation{Kind: "steps", RunID: id})
	if !read(response, &steps) {
		return &response
	}
	for _, step := range steps.Steps {
		data.Steps = append(data.Steps, dashboard.StoredWorkflowConsoleStep{Name: step.StepName, Status: step.Status, Attempt: step.Attempt})
	}
	s.inspectRecovery(r, scope, data, id)
	return s.inspectAttempts(r, scope, data, id)
}

func runView(scope Scope, run api.WorkflowRunResponse) dashboard.StoredWorkflowConsoleRun {
	view := dashboard.StoredWorkflowConsoleRun{ID: run.ID, Name: run.WorkflowName, Status: run.Status,
		CreatedAt: run.CreatedAt, DeploymentID: run.DeploymentID, ResumeCount: run.ResumeCount,
		CanCancel: run.CancelledAt == nil && (run.Status == "pending" || run.Status == "running" || run.Status == "awaiting_event"),
		URL:       consoleURL(scope.Slug, run.WorkflowName, run.ID)}
	if run.CurrentStep != nil {
		view.CurrentStep = *run.CurrentStep
	}
	return view
}
func consoleURL(slug, name, run string) string {
	query := url.Values{"name": {name}}
	if run != "" {
		query.Set("run", run)
	}
	return "/dashboard/apps/" + url.PathEscape(slug) + "/workflows/stored?" + query.Encode()
}

func (s *Service) afterWrite(w http.ResponseWriter, r *http.Request, scope Scope, data dashboard.StoredWorkflowConsoleData, response Response, message string) {
	var saved api.StoredWorkflowResponse
	if !read(response, &saved) {
		s.fail(w, r, scope, data, badResponse())
		return
	}
	current, failure := s.workspace(r, scope)
	if failure != nil {
		s.fail(w, r, scope, data, *failure)
		return
	}
	data.Version, data.Published, data.Enabled, data.FailurePaused = saved.Version, saved.Published != nil, saved.Enabled, saved.FailurePaused
	data.Items, data.Starters = current.Items, current.Starters
	data.RuntimeEnabled, data.UnavailableReason, data.PolicyMode = current.RuntimeEnabled, current.UnavailableReason, current.PolicyMode
	data.RuntimeObserved = current.RuntimeObserved
	data.Message = message
	s.inspectStoredWorkflow(r, scope, &data)
	s.render(w, r, scope, data, http.StatusOK)
}

// Refresh display metadata without replacing a submitted revision or editor text.
func (s *Service) refreshMetadata(r *http.Request, scope Scope, data *dashboard.StoredWorkflowConsoleData) {
	if data.Items == nil {
		current, failure := s.workspace(r, scope)
		if failure == nil {
			data.Items, data.Starters = current.Items, current.Starters
			data.RuntimeEnabled, data.UnavailableReason, data.PolicyMode = current.RuntimeEnabled, current.UnavailableReason, current.PolicyMode
			data.RuntimeObserved = current.RuntimeObserved
		}
	}
	if data.Name == "" {
		return
	}
	var current api.StoredWorkflowResponse
	response := s.Host.Request(r, scope, Operation{Kind: "get", Name: data.Name})
	data.PublicationObserved = false
	if read(response, &current) && current.Name == data.Name {
		data.PublicationObserved = true
		testMetadata(data, current)
		data.Published, data.Enabled, data.FailurePaused = current.Published != nil, current.Enabled, current.FailurePaused
		data.PublishedSchedule = current.Published != nil && current.Published.Trigger != nil && current.Published.Trigger.Type == "schedule"
	} else if response.Status == http.StatusNotFound {
		data.PublicationObserved = true
		data.Published, data.Enabled, data.FailurePaused, data.PublishedSchedule = false, false, false, false
	}
}
