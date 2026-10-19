package automationconsole

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func (s *Service) inspectLiveSuites(r *http.Request, scope Scope, data *dashboard.AutomationConsoleData) {
	if data.Version == 0 {
		return
	}
	var listing api.ListAutomationLiveSuitesResponse
	if !read(s.Host.Request(r, scope, Operation{Kind: "live-suite-list", Name: data.Name}), &listing) {
		data.LiveSuiteError = "Shared live suite history is temporarily unavailable."
		return
	}
	data.LiveSuites = &listing
	id := data.LiveSuiteID
	if id == "" {
		id = r.URL.Query().Get("suite")
	}
	if id == "" {
		return
	}
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil {
		data.LiveSuiteError = "Choose a retained suite for this automation."
		return
	}
	data.LiveSuiteID = parsed.String()
	var selected api.AutomationLiveSuite
	if !read(s.Host.Request(r, scope, Operation{Kind: "live-suite-get", Name: data.Name, RunID: parsed.String()}), &selected) || selected.ID != parsed.String() || selected.Name != data.Name {
		data.LiveSuiteError = "This suite is unavailable for the selected automation."
		return
	}
	data.LiveSuite = &selected
	s.inspectLiveSuiteComparison(r, scope, data)
}
