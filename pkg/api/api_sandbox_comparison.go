package api

import "time"

const (
	APISandboxComparisonRunning      = "running"
	APISandboxComparisonPassed       = "passed"
	APISandboxComparisonRegression   = "regression"
	APISandboxComparisonInconclusive = "inconclusive"
)

type APISandboxComparisonRequest struct {
	BaselineDeploymentID string `json:"baseline_deployment_id"`
	ScenarioRevision     string `json:"scenario_revision"`
}

type APISandboxComparisonBaseline struct {
	DeploymentID string    `json:"deployment_id"`
	CommitSHA    string    `json:"commit_sha,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type APISandboxComparisonReport struct {
	ID                    string              `json:"id"`
	BaselineDeploymentID  string              `json:"baseline_deployment_id"`
	CandidateDeploymentID string              `json:"candidate_deployment_id"`
	ScenarioRevision      string              `json:"scenario_revision"`
	ScenarioName          string              `json:"scenario_name"`
	Verdict               string              `json:"verdict"`
	Reason                string              `json:"reason,omitempty"`
	FailureSide           string              `json:"failure_side,omitempty"`
	FailureCode           string              `json:"failure_code,omitempty"`
	FirstFailingStep      *APISandboxRunStep  `json:"first_failing_step,omitempty"`
	Baseline              APISandboxRunReport `json:"baseline"`
	Candidate             APISandboxRunReport `json:"candidate"`
	CreatedAt             time.Time           `json:"created_at"`
}

// CompletedAt is the time both sandbox runs had finished.
func (report APISandboxComparisonReport) CompletedAt() (time.Time, bool) {
	baseline, candidate := report.Baseline.FinishedAt, report.Candidate.FinishedAt
	if baseline == nil || candidate == nil {
		return time.Time{}, false
	}
	if baseline.After(*candidate) {
		return *baseline, true
	}
	return *candidate, true
}

type APISandboxComparisonsResponse struct {
	Comparisons []APISandboxComparisonReport   `json:"comparisons"`
	Baselines   []APISandboxComparisonBaseline `json:"baselines"`
}

// CompareAPISandboxRuns derives the verdict exclusively from the pinned run
// receipts. An infrastructure failure cannot establish a regression.
func CompareAPISandboxRuns(id string, at time.Time, baseline, candidate APISandboxRunReport) APISandboxComparisonReport {
	report := APISandboxComparisonReport{
		ID: id, CreatedAt: at, BaselineDeploymentID: baseline.SourceDeploymentID, CandidateDeploymentID: candidate.SourceDeploymentID,
		ScenarioRevision: candidate.ScenarioRevision, ScenarioName: candidate.ScenarioName,
		Baseline: baseline, Candidate: candidate, Verdict: APISandboxComparisonRunning,
	}
	if !baseline.Terminal() || !candidate.Terminal() {
		return report
	}
	if baseline.Status == APISandboxRunPassed && candidate.Status == APISandboxRunPassed {
		report.Verdict = APISandboxComparisonPassed
		return report
	}
	report.Verdict = APISandboxComparisonInconclusive
	failed := candidate
	report.FailureSide, report.Reason = "candidate", "candidate_unavailable"
	if baseline.Status != APISandboxRunPassed {
		failed = baseline
		report.FailureSide, report.Reason = "baseline", "baseline_unavailable"
	}
	report.FailureCode = failed.FailureCode
	for _, step := range failed.Steps {
		if step.FailureCode == "" {
			continue
		}
		report.FirstFailingStep = &step
		if failed.Status == APISandboxRunFailed && failed.FailureCode == step.FailureCode && sandboxBehaviorFailure(step.FailureCode) {
			if report.FailureSide == "baseline" {
				report.Reason = "baseline_check_failed"
			} else {
				report.Verdict, report.Reason = APISandboxComparisonRegression, "candidate_check_failed"
			}
		}
		break
	}
	return report
}

func sandboxBehaviorFailure(code string) bool {
	switch code {
	case "unexpected_http_status", "invalid_json_response", "json_assertion_failed",
		"webhook_duplicate_mismatch", "outbound_webhook_payload_mismatch":
		return true
	default:
		return false
	}
}
