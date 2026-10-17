package automationconsole

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func (s *Service) control(w http.ResponseWriter, r *http.Request, scope Scope, data dashboard.AutomationConsoleData) {
	run, failure := s.scopedRun(r, scope, data.Name, r.PostFormValue("run_id"))
	if failure != nil {
		s.fail(w, r, scope, data, *failure)
		return
	}
	if failure := s.selectAutomation(r, scope, &data, data.Name); failure != nil {
		s.fail(w, r, scope, data, *failure)
		return
	}
	operation, message := controlOperation(r, run)
	if message != "" {
		s.controlFailure(w, r, scope, data, run.ID, Response{Status: http.StatusBadRequest}, message)
		return
	}
	// Do not reject a repeat using a new diagnostics snapshot: the API's
	// idempotency wrapper must be able to replay an already admitted continuation.
	response := s.Host.Request(r, scope, operation)
	var updated api.WorkflowRunResponse
	if !read(response, &updated) || updated.ID != run.ID || updated.AppID != scope.AppID || updated.WorkflowName != data.Name {
		if success(response) {
			response = badResponse()
		}
		s.controlFailure(w, r, scope, data, run.ID, response, "")
		return
	}
	redirect(w, r, scope, data.Name, "", run.ID)
}

func controlOperation(r *http.Request, run api.WorkflowRunResponse) (Operation, string) {
	action := r.PathValue("action")
	if r.PostFormValue("confirm_"+action) != "yes" {
		return Operation{}, "Confirm the selected run action before submitting it."
	}
	key := r.PostFormValue("action_key")
	if _, err := uuid.Parse(key); err != nil {
		return Operation{}, "Refresh the run status before submitting a run action."
	}
	operation := Operation{Kind: action, RunID: run.ID, Key: key}
	if action == "resume" {
		count, err := strconv.Atoi(r.PostFormValue("expected_resume_count"))
		if err != nil || count < 0 || count > api.WorkflowRunMaxResumes {
			return Operation{}, "Refresh the recovery preview to read its continuation count."
		}
		operation.Body = api.ResumeWorkflowRunRequest{ExpectedResumeCount: &count}
	}
	return operation, ""
}

func (s *Service) controlFailure(w http.ResponseWriter, r *http.Request, scope Scope, data dashboard.AutomationConsoleData, id string, response Response, message string) {
	if failure := s.inspectRun(r, scope, &data, id); failure != nil {
		data.Run = nil
	}
	// A failed/stale action requires a fresh GET and a new confirmation.
	data.RunControlsDisabled = true
	if message != "" {
		data.Error = message
		s.render(w, r, scope, data, response.Status)
		return
	}
	s.fail(w, r, scope, data, response)
}
