package api

const (
	APISandboxApprovalReady       = "ready"
	APISandboxApprovalNotWaiting  = "not_waiting"
	APISandboxApprovalUnsupported = "unsupported"
	APISandboxApprovalNoBaseline  = "no_baseline"
	APISandboxApprovalNoScenario  = "no_scenario"
	APISandboxApprovalStale       = "stale"
)

type APISandboxApprovalCheck struct {
	Comparison  APISandboxComparisonReport `json:"comparison"`
	Verdict     string                     `json:"verdict"`
	StaleReason string                     `json:"stale_reason,omitempty"`
}

type APISandboxApprovalChecksResponse struct {
	Availability         string                             `json:"availability"`
	BaselineDeploymentID string                             `json:"baseline_deployment_id,omitempty"`
	ScenarioRevision     string                             `json:"scenario_revision,omitempty"`
	ScenarioName         string                             `json:"scenario_name,omitempty"`
	LibraryScenarios     []APISandboxLibraryScenarioSummary `json:"library_scenarios,omitempty"`
	Checks               []APISandboxApprovalCheck          `json:"checks"`
}
