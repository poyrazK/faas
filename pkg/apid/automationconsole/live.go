package automationconsole

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func liveMetadata(data *dashboard.AutomationConsoleData, saved api.AutomationResponse) {
	data.LiveEditorChanged = !savedTestInputsMatch(*data, saved)
	data.LiveCases = nil
	if saved.TestSuite == nil {
		return
	}
	for _, c := range saved.TestSuite.Cases {
		if c.Live != nil && len(c.Live.Expectations) > 0 {
			data.LiveCases = append(data.LiveCases, c.Name)
		}
	}
}

func (s *Service) inspectLive(r *http.Request, scope Scope, data *dashboard.AutomationConsoleData) {
	if data.Version == 0 {
		return
	}
	var listing api.ListAutomationLiveVerificationsResponse
	response := s.Host.Request(r, scope, Operation{Kind: "live-list", Name: data.Name})
	if !read(response, &listing) {
		data.LiveError = "Live verification history is temporarily unavailable."
		return
	}
	data.LiveHistory = &listing
	if data.LiveID == "" {
		data.LiveID = r.URL.Query().Get("verification")
	}
	if data.LiveID == "" {
		return
	}
	var selected api.AutomationLiveVerification
	response = s.Host.Request(r, scope, Operation{Kind: "live-get", Name: data.Name, RunID: data.LiveID})
	if !read(response, &selected) {
		data.LiveError = "This live verification is unavailable for the selected automation."
		return
	}
	data.LiveSelected = &selected
	data.LiveRunURL = consoleURL(scope.Slug, data.Name, selected.RunID)
}

func (s *Service) live(w http.ResponseWriter, r *http.Request, scope Scope, data dashboard.AutomationConsoleData) {
	if r.PathValue("action") == "check-live" {
		response := s.Host.Request(r, scope, Operation{Kind: "live-check", Name: data.Name, RunID: r.PostFormValue("verification_id"), Body: struct{}{}})
		s.liveResult(w, r, scope, data, response, "Live run checked. Pending runs have no assertion verdict yet.")
		return
	}
	saved, failure := s.savedDraft(r, scope, data)
	if failure != nil {
		s.fail(w, r, scope, data, *failure)
		return
	}
	if !savedTestInputsMatch(data, saved) {
		s.invalid(w, r, scope, data, "Save your current sample and test cases before starting live verification.")
		return
	}
	if _, err := uuid.Parse(r.PostFormValue("action_key")); err != nil {
		s.invalid(w, r, scope, data, "Reload before starting live verification.")
		return
	}
	version, err := strconv.ParseInt(r.PostFormValue("live_published_version"), 10, 64)
	if err != nil {
		s.invalid(w, r, scope, data, "Reload the published revision before starting live verification.")
		return
	}
	body := api.StartAutomationLiveVerificationRequest{ExpectedVersion: data.Version, ExpectedPublishedVersion: version,
		ExpectedDeploymentID: r.PostFormValue("live_deployment_id"), CaseName: r.PostFormValue("live_case"), ConfirmRealActions: r.PostFormValue("confirm_live") == "yes"}
	response := s.Host.Request(r, scope, Operation{Kind: "live-start", Name: data.Name, Key: r.PostFormValue("action_key"), Body: body})
	s.liveResult(w, r, scope, data, response, "Live verification started. This run may perform real actions. Refresh its results after execution.")
}

func (s *Service) liveResult(w http.ResponseWriter, r *http.Request, scope Scope, data dashboard.AutomationConsoleData, response Response, message string) {
	var view api.AutomationLiveVerification
	if !read(response, &view) {
		s.fail(w, r, scope, data, response)
		return
	}
	data.LiveID, data.Message = view.RunID, message
	s.render(w, r, scope, data, http.StatusOK)
}
