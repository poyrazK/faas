package automationconsole

import (
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

// Attempt history is fetched only for an explicitly selected, observed step.
func (s *Service) inspectAttempts(r *http.Request, scope Scope, data *dashboard.AutomationConsoleData, id string) *Response {
	selected := r.URL.Query().Get("step")
	found := false
	for i := range data.Steps {
		step := &data.Steps[i]
		step.AttemptsURL = consoleURL(scope.Slug, data.Name, id) + "&step=" + url.QueryEscape(step.Name) + "#selected-step"
		found = found || step.Name == selected
	}
	if selected == "" {
		return nil
	}
	if !found {
		response := Response{Status: http.StatusNotFound}
		return &response
	}
	data.SelectedStep = selected
	var history api.ListWorkflowStepAttemptsResponse
	response := s.Host.Request(r, scope, Operation{Kind: "attempts", RunID: id, Step: selected})
	if !read(response, &history) {
		data.AttemptsError = "Step attempt history is temporarily unavailable."
		return nil
	}
	for _, attempt := range history.Attempts {
		data.Attempts = append(data.Attempts, dashboard.AutomationConsoleAttempt{Number: attempt.Attempt,
			Status: attempt.Status, StartedAt: attempt.StartedAt, FinishedAt: attempt.FinishedAt,
			HTTPStatus: attempt.HTTPStatus, NextAttemptAt: attempt.NextAttemptAt})
	}
	return nil
}
