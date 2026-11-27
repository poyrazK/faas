package api

import "time"

const APISandboxSuiteMaxScenarios = 5

type APISandboxSuite struct {
	Revision          string   `json:"revision"`
	ScenarioIDs       []string `json:"scenario_ids"`
	RunBeforeApproval bool     `json:"run_before_approval,omitempty"`
}

type APISandboxSuiteResponse struct {
	Suite *APISandboxSuite `json:"suite"`
}

type APISandboxSuiteSaveRequest struct {
	ExpectedRevision  *string  `json:"expected_revision"`
	ScenarioIDs       []string `json:"scenario_ids"`
	RunBeforeApproval *bool    `json:"run_before_approval,omitempty"`
}

type APISandboxSuiteRunRequest struct {
	SuiteRevision        string `json:"suite_revision"`
	BaselineDeploymentID string `json:"baseline_deployment_id"`
}

type APISandboxSuiteRunSummary struct {
	ID                    string     `json:"id"`
	SuiteRevision         string     `json:"suite_revision"`
	BaselineDeploymentID  string     `json:"baseline_deployment_id"`
	CandidateDeploymentID string     `json:"candidate_deployment_id"`
	ScenarioCount         int        `json:"scenario_count"`
	CompletedCount        int        `json:"completed_count"`
	CreatedAt             time.Time  `json:"created_at"`
	FinishedAt            *time.Time `json:"finished_at,omitempty"`
	Automatic             bool       `json:"automatic,omitempty"`
	WaitingForCapacity    bool       `json:"waiting_for_capacity,omitempty"`
}

type APISandboxSuiteRunsResponse struct {
	Runs            []APISandboxSuiteRunSummary `json:"runs"`
	AutomaticStatus string                      `json:"automatic_status,omitempty"`
	ReleaseGate     *APISandboxReleaseGate      `json:"release_gate,omitempty"`
}

type APISandboxReleaseGate struct {
	Status     string `json:"status"`
	SuiteRunID string `json:"suite_run_id,omitempty"`
}

type APISandboxSuiteScenarioResult struct {
	ScenarioID       string                      `json:"scenario_id"`
	ScenarioName     string                      `json:"scenario_name"`
	ScenarioRevision string                      `json:"scenario_revision"`
	Status           string                      `json:"status"`
	Comparison       *APISandboxComparisonReport `json:"comparison,omitempty"`
}

type APISandboxSuiteRunReport struct {
	APISandboxSuiteRunSummary
	Verdict     string                          `json:"verdict"`
	StaleReason string                          `json:"stale_reason,omitempty"`
	FailureCode string                          `json:"failure_code,omitempty"`
	Scenarios   []APISandboxSuiteScenarioResult `json:"scenarios"`
}
