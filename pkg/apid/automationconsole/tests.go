package automationconsole

import (
	"encoding/json"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/automationchecks"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func editorTestSuite(data dashboard.AutomationConsoleData) (api.AutomationTestSuite, error) {
	sim, err := sample(data, api.WorkflowSpec{})
	if err != nil {
		return api.AutomationTestSuite{}, err
	}
	var scenarios []api.AutomationPublishCheckScenario
	if err := decode(data.Scenarios, api.AutomationPublishCheckMaxBytes, &scenarios); err != nil {
		return api.AutomationTestSuite{}, err
	}
	suite := api.AutomationTestSuite{Sample: api.TestInputsFromSimulation(sim), Cases: []api.AutomationTestCase{}}
	for _, c := range scenarios {
		suite.Cases = append(suite.Cases, api.AutomationTestCase{Name: c.Name, Simulation: api.TestInputsFromSimulation(c.Simulation), Expectations: c.Expectations, Live: c.Live})
	}
	return suite, nil
}

func savedTestInputsMatch(data dashboard.AutomationConsoleData, saved api.AutomationResponse) bool {
	if saved.TestSuite == nil {
		return false
	}
	suite, err := editorTestSuite(data)
	if err != nil {
		return false
	}
	expected, err := json.Marshal(suite)
	actual, actualErr := json.Marshal(*saved.TestSuite)
	return err == nil && actualErr == nil && automationchecks.EqualJSON(expected, actual)
}

func (s *Service) tests(w http.ResponseWriter, r *http.Request, scope Scope, data dashboard.AutomationConsoleData) {
	saved, failure := s.savedDraft(r, scope, data)
	if failure != nil {
		s.fail(w, r, scope, data, *failure)
		return
	}
	if saved.Version == 0 {
		s.invalid(w, r, scope, data, "Save the draft before saving test data.")
		return
	}
	switch r.PathValue("action") {
	case "save-tests":
		suite, err := editorTestSuite(data)
		if err != nil {
			s.invalid(w, r, scope, data, "Enter valid sample and named test case JSON before saving test data.")
			return
		}
		response := s.Host.Request(r, scope, Operation{Kind: "save-tests", Name: data.Name, Body: api.SaveAutomationTestSuiteRequest{ExpectedVersion: saved.Version, Suite: &suite}})
		if !success(response) {
			s.fail(w, r, scope, data, response)
			return
		}
		s.afterWrite(w, r, scope, data, response, "Test data saved. Reopen this automation to restore its sample and test cases.")
	case "clear-tests":
		if r.PostFormValue("confirm_clear_tests") != "yes" {
			s.invalid(w, r, scope, data, "Confirm removal of the saved sample, test cases, and results.")
			return
		}
		response := s.Host.Request(r, scope, Operation{Kind: "save-tests", Name: data.Name, Body: api.SaveAutomationTestSuiteRequest{ExpectedVersion: saved.Version}})
		if !success(response) {
			s.fail(w, r, scope, data, response)
			return
		}
		s.afterWrite(w, r, scope, data, response, "Saved test data and results removed. The editor still contains your unsaved sample and cases.")
	case "run-tests":
		if !savedTestInputsMatch(data, saved) {
			s.invalid(w, r, scope, data, "Save your current sample and test cases before running saved tests.")
			return
		}
		response := s.Host.Request(r, scope, Operation{Kind: "run-tests", Name: data.Name, Body: api.RunAutomationTestsRequest{ExpectedVersion: saved.Version}})
		if !success(response) {
			s.fail(w, r, scope, data, response)
			return
		}
		data.Message = "Saved test cases finished. Review the case and step results below."
		s.render(w, r, scope, data, http.StatusOK)
	}
}

func testMetadata(data *dashboard.AutomationConsoleData, saved api.AutomationResponse) {
	liveMetadata(data, saved)
	data.TestSuiteSaved = saved.TestSuite != nil
	data.TestReport = saved.TestReport
	data.TestReportFresh = false
	if saved.TestReport == nil || !savedTestInputsMatch(*data, saved) {
		return
	}
	spec, err := definition(*data)
	expected, expectedErr := json.Marshal(spec)
	actual, actualErr := json.Marshal(saved.Draft)
	if err != nil || expectedErr != nil || actualErr != nil || !automationchecks.EqualJSON(expected, actual) {
		return
	}
	hash, err := api.AutomationTestInputsHash(saved.Draft, *saved.TestSuite)
	data.TestReportFresh = err == nil && hash == saved.TestReport.InputsHash
}
