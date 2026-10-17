package automationconsole

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func setupLiveProgress(data *dashboard.AutomationConsoleData) {
	if data.LiveSelected == nil {
		return
	}
	data.LiveProgress = &dashboard.AutomationLiveProgress{
		URL:        data.BaseURL + "/live-verifications/" + url.PathEscape(data.LiveSelected.RunID) + "?" + url.Values{"name": {data.Name}}.Encode(),
		IntervalMS: int(api.AutomationLiveProgressPollInterval / time.Millisecond), TimeoutMS: int(api.AutomationLiveProgressRequestTimeout / time.Millisecond),
		MaxRequests: api.AutomationLiveProgressMaxRequests,
	}
}

func (s *Service) LiveProgress(w http.ResponseWriter, r *http.Request) {
	s.serveLiveProgress(w, r, false)
}

func (s *Service) CheckLiveProgress(w http.ResponseWriter, r *http.Request) {
	s.serveLiveProgress(w, r, true)
}

func (s *Service) serveLiveProgress(w http.ResponseWriter, r *http.Request, check bool) {
	ctx, cancel := context.WithTimeout(r.Context(), api.AutomationLiveProgressRequestTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	scope, ok := s.Host.Scope(w, r)
	if !ok {
		return
	}
	name, id := r.URL.Query().Get("name"), r.PathValue("run_id")
	runID, err := uuid.Parse(id)
	if err != nil || runID == uuid.Nil || name == "" || len(name) > api.AutomationNameMaxBytes || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		api.WriteProblem(w, api.ErrValidation("Choose a saved automation and live verification to inspect."))
		return
	}
	kind := "live-get"
	if check {
		if !s.liveProgressCSRF(w, r, scope) {
			return
		}
		kind = "live-check"
	}
	response := s.Host.Request(r, scope, Operation{Kind: kind, Name: name, RunID: runID.String()})
	var view api.AutomationLiveVerification
	if !read(response, &view) || view.RunID != runID.String() {
		status := response.Status
		if status < http.StatusBadRequest || status > http.StatusServiceUnavailable {
			status = http.StatusServiceUnavailable
		}
		api.WriteProblem(w, api.NewProblem(status, api.CodeCapacity, "Progress unavailable", "Live verification progress could not be refreshed. Use the manual result check or reopen the automation."))
		return
	}
	s.renderLiveProgress(w, scope, name, view)
}

func (s *Service) liveProgressCSRF(w http.ResponseWriter, r *http.Request, scope Scope) bool {
	r.Body = http.MaxBytesReader(w, r.Body, api.AutomationLiveProgressFormMaxBytes)
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("The result-check request is invalid or too large."))
		return false
	}
	for key := range r.PostForm {
		if key != "csrf_token" {
			api.WriteProblem(w, api.ErrValidation("Result checks accept only the CSRF token; editor data and execution actions are not accepted."))
			return false
		}
	}
	return s.Host.VerifyCSRF(w, r, scope)
}

func (s *Service) renderLiveProgress(w http.ResponseWriter, scope Scope, name string, view api.AutomationLiveVerification) {
	data := dashboard.AutomationConsoleData{Name: name, LiveSelected: &view,
		BaseURL: "/dashboard/apps/" + url.PathEscape(scope.Slug) + "/automations", LiveRunURL: consoleURL(scope.Slug, name, view.RunID)}
	if err := dashboard.Render(w, s.Log, "", dashboard.Page{Body: "automation_live_result", Data: data}); err != nil {
		api.WriteProblem(w, api.ErrCapacity("Live verification progress is temporarily unavailable."))
	}
}
