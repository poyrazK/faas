package apidserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) saveStoredWorkflowTestSuite(w http.ResponseWriter, r *http.Request, account state.Account) {
	var body api.SaveStoredWorkflowTestSuiteRequest
	if err := decodeJSONSized(r, &body, api.StoredWorkflowTestSuiteRequestMaxBytes); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			api.WriteProblem(w, api.ErrRequestBodyTooLarge(api.StoredWorkflowTestSuiteRequestMaxBytes, api.StoredWorkflowTestSuiteRequestMaxBytes+1))
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
			writeStoredWorkflowError(w, state.ErrStoredWorkflowInvalid)
			return
		}
	}
	s.applyStoredWorkflowMutation(w, r, account, state.StoredWorkflowMutation{Action: "tests", ExpectedVersion: body.ExpectedVersion, TestSuite: raw})
}

func (s *server) runStoredWorkflowTests(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	var body api.RunStoredWorkflowTestsRequest
	if !decodeStoredWorkflowBody(w, r, &body) {
		return
	}
	store, ok := s.store.(state.StoredWorkflowTestStore)
	if !ok {
		writeStoredWorkflowError(w, errors.New("saved tests unavailable"))
		return
	}
	report, err := store.RunStoredWorkflowTests(r.Context(), app.ID, r.PathValue("name"), body.ExpectedVersion, account.Plan)
	if err != nil {
		writeStoredWorkflowError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func storedWorkflowSavedTests(record state.StoredWorkflow, item *api.StoredWorkflowResponse) error {
	if len(record.TestSuite) > 0 {
		item.TestSuite = new(api.StoredWorkflowTestSuite)
		if err := json.Unmarshal(record.TestSuite, item.TestSuite); err != nil {
			return err
		}
	}
	if len(record.TestReport) > 0 {
		item.TestReport = new(api.StoredWorkflowTestReport)
		if err := json.Unmarshal(record.TestReport, item.TestReport); err != nil {
			return err
		}
	}
	return nil
}
