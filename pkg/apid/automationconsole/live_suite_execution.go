package automationconsole

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func (s *Service) startLiveSuite(w http.ResponseWriter, r *http.Request, scope Scope, data dashboard.AutomationConsoleData) {
	saved, failure := s.savedDraft(r, scope, data)
	if failure != nil {
		s.fail(w, r, scope, data, *failure)
		return
	}
	if !savedTestInputsMatch(data, saved) {
		s.invalid(w, r, scope, data, "Save your current sample and test cases before starting a live suite.")
		return
	}
	version, err := strconv.ParseInt(r.PostFormValue("live_published_version"), 10, 64)
	if err != nil {
		s.invalid(w, r, scope, data, "Reload the published revision before starting a suite.")
		return
	}
	id, err := uuid.Parse(r.PostFormValue("live_suite_start_id"))
	if err != nil || id.Version() != 7 {
		s.invalid(w, r, scope, data, "Reload before starting a new suite.")
		return
	}
	body := api.StartAutomationLiveSuiteRequest{ExpectedVersion: data.Version, ExpectedPublishedVersion: version, ExpectedDeploymentID: r.PostFormValue("live_deployment_id"), Cases: r.PostForm["live_suite_case"], ConfirmRealActions: r.PostFormValue("confirm_live_suite_start") == "yes"}
	response := s.Host.Request(r, scope, Operation{Kind: "live-suite-start", Name: data.Name, RunID: id.String(), Body: body})
	s.liveSuiteResult(w, r, scope, data, id.String(), response)
}

func (s *Service) continueLiveSuite(w http.ResponseWriter, r *http.Request, scope Scope, data dashboard.AutomationConsoleData) {
	id, err := uuid.Parse(r.PostFormValue("live_suite_id"))
	if err != nil || id == uuid.Nil {
		s.invalid(w, r, scope, data, "Choose a retained dashboard suite to continue.")
		return
	}
	body := api.AdvanceAutomationLiveSuiteRequest{ConfirmRealActions: r.PostFormValue("confirm_live_suite_continue") == "yes"}
	response := s.Host.Request(r, scope, Operation{Kind: "live-suite-advance", Name: data.Name, RunID: id.String(), Body: body})
	s.liveSuiteResult(w, r, scope, data, id.String(), response)
}

func (s *Service) liveSuiteResult(w http.ResponseWriter, r *http.Request, scope Scope, data dashboard.AutomationConsoleData, id string, response Response) {
	var view api.AutomationLiveSuite
	if !read(response, &view) {
		s.fail(w, r, scope, data, response)
		return
	}
	if view.ID != id || view.Name != data.Name || !view.Managed {
		s.fail(w, r, scope, data, badResponse())
		return
	}
	data.LiveSuiteID = id
	data.LiveSuiteAuto = view.Outcome == "pending" || view.Outcome == "incomplete"
	data.Message = "Suite progress saved. Cases run in order and stop at the first failed or unresolved result."
	s.render(w, r, scope, data, http.StatusOK)
}

func setupLiveSuiteProgress(data *dashboard.AutomationConsoleData) {
	if data.LiveSuite == nil || !data.LiveSuite.Managed {
		return
	}
	data.LiveSuiteProgress = &dashboard.AutomationLiveProgress{URL: data.BaseURL + "/live-suites/" + url.PathEscape(data.LiveSuite.ID) + "/advance?" + url.Values{"name": {data.Name}}.Encode(), IntervalMS: int(api.AutomationLiveProgressPollInterval / time.Millisecond), TimeoutMS: int(api.AutomationLiveProgressRequestTimeout / time.Millisecond), MaxRequests: api.AutomationLiveProgressMaxRequests}
}

// AdvanceLiveSuite is an authenticated, CSRF-protected action. Reopening a
// suite uses Show/GET and never invokes this endpoint automatically.
func (s *Service) AdvanceLiveSuite(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), api.AutomationLiveProgressRequestTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	scope, ok := s.Host.Scope(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("suite_id"))
	name := r.URL.Query().Get("name")
	if err != nil || id == uuid.Nil || name == "" || len(name) > api.AutomationNameMaxBytes || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		api.WriteProblem(w, api.ErrValidation("Choose a retained automation suite."))
		return
	}
	if !s.suiteProgressCSRF(w, r, scope) {
		return
	}
	response := s.Host.Request(r, scope, Operation{Kind: "live-suite-advance", Name: name, RunID: id.String(), Body: api.AdvanceAutomationLiveSuiteRequest{ConfirmRealActions: true}})
	var view api.AutomationLiveSuite
	if !read(response, &view) || view.ID != id.String() || view.Name != name || !view.Managed {
		status := response.Status
		if status < 400 || status > 503 {
			status = http.StatusServiceUnavailable
		}
		api.WriteProblem(w, api.NewProblem(status, api.CodeCapacity, "Suite progress unavailable", "Progress stopped. Reopen this same suite and confirm continuation after resolving the problem; do not start a replacement to recover it."))
		return
	}
	s.renderLiveSuiteProgress(w, scope, name, view)
}

func (s *Service) suiteProgressCSRF(w http.ResponseWriter, r *http.Request, scope Scope) bool {
	r.Body = http.MaxBytesReader(w, r.Body, api.AutomationLiveProgressFormMaxBytes)
	if r.ParseForm() != nil {
		api.WriteProblem(w, api.ErrValidation("The suite continuation request is invalid or too large."))
		return false
	}
	for key := range r.PostForm {
		if key != "csrf_token" && key != "confirm_real_actions" {
			api.WriteProblem(w, api.ErrValidation("Suite continuation accepts only confirmation and the CSRF token."))
			return false
		}
	}
	if r.PostFormValue("confirm_real_actions") != "yes" {
		api.WriteProblem(w, api.ErrValidation("Confirm real actions before continuing this suite."))
		return false
	}
	return s.Host.VerifyCSRF(w, r, scope)
}

func (s *Service) renderLiveSuiteProgress(w http.ResponseWriter, scope Scope, name string, view api.AutomationLiveSuite) {
	data := dashboard.AutomationConsoleData{Name: name, LiveSuite: &view, BaseURL: "/dashboard/apps/" + url.PathEscape(scope.Slug) + "/automations"}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := dashboard.Render(w, s.Log, "", dashboard.Page{Body: "automation_live_suite_result", Data: data}); err != nil {
		api.WriteProblem(w, api.ErrCapacity("Suite progress is temporarily unavailable."))
	}
}
