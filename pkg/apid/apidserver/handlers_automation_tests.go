package apidserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) saveAutomationTestSuite(w http.ResponseWriter, r *http.Request, account state.Account) {
	var body api.SaveAutomationTestSuiteRequest
	if err := decodeJSONSized(r, &body, api.AutomationTestSuiteRequestMaxBytes); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			api.WriteProblem(w, api.ErrRequestBodyTooLarge(api.AutomationTestSuiteRequestMaxBytes, api.AutomationTestSuiteRequestMaxBytes+1))
		} else {
			api.WriteProblem(w, api.ErrValidation("invalid saved test data"))
		}
		return
	}
	var raw json.RawMessage
	if body.Suite != nil {
		var err error
		raw, err = json.Marshal(body.Suite)
		if err != nil {
			writeAutomationError(w, state.ErrAutomationInvalid)
			return
		}
	}
	s.applyAutomationMutation(w, r, account, state.AutomationMutation{Action: "tests", ExpectedVersion: body.ExpectedVersion, TestSuite: raw})
}

func (s *server) runAutomationTests(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	var body api.RunAutomationTestsRequest
	if !decodeAutomationBody(w, r, &body) {
		return
	}
	store, ok := s.store.(state.AutomationTestStore)
	if !ok {
		writeAutomationError(w, errors.New("saved tests unavailable"))
		return
	}
	report, err := store.RunAutomationTests(r.Context(), app.ID, r.PathValue("name"), body.ExpectedVersion, account.Plan)
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func automationSavedTests(record state.Automation, item *api.AutomationResponse) error {
	if len(record.TestSuite) > 0 {
		item.TestSuite = new(api.AutomationTestSuite)
		if err := json.Unmarshal(record.TestSuite, item.TestSuite); err != nil {
			return err
		}
	}
	if len(record.TestReport) > 0 {
		item.TestReport = new(api.AutomationTestReport)
		if err := json.Unmarshal(record.TestReport, item.TestReport); err != nil {
			return err
		}
	}
	return nil
}
