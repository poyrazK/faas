package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type storedWorkflowCheckedPublishReport struct {
	PublishingPolicy      api.StoredWorkflowPublishPolicy `json:"publishing_policy"`
	Diff                  storedWorkflowDefinitionDiff    `json:"diff"`
	Name                  string                          `json:"name"`
	CheckedVersion        int64                           `json:"checked_version"`
	DefinitionHash        string                          `json:"definition_hash"`
	Checks                storedWorkflowCheckReport       `json:"checks"`
	Published             bool                            `json:"published"`
	PublicationAttempted  bool                            `json:"publication_attempted"`
	PublicationOutcome    string                          `json:"publication_outcome"`
	PublicationHTTPStatus int                             `json:"publication_http_status,omitempty"`
	PublicationErrorCode  string                          `json:"publication_error_code,omitempty"`
	StoredWorkflow        *storedWorkflowMutationSummary  `json:"stored_workflow,omitempty"`
	Error                 string                          `json:"error,omitempty"`
}

func publishStoredWorkflowWithScenarios(client *api.Client, app, name string, version int64, takeover bool, scenarios []loadedStoredWorkflowScenario, requireCoverage ...bool) int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	draft, err := client.GetStoredWorkflow(ctx, app, name)
	if err != nil {
		return printErr("Could not load stored workflow draft", err)
	}
	if draft.Version != version || draft.Name != name || draft.Draft.Name != name {
		return printErr("Stored workflow draft changed", errors.New("name and version must match the saved draft; reload it before publishing"))
	}
	snapshot, err := json.Marshal(draft.Draft)
	if err != nil {
		return printErr("Invalid saved stored workflow draft", err)
	}
	for i := range scenarios {
		local, err := json.Marshal(scenarios[i].request.Definition)
		if err != nil || !equalStoredWorkflowScenarioJSON(local, snapshot) {
			return printErr("Scenario definition differs from saved draft", errors.New("apply the intended definition and use its returned version before publishing"))
		}
		// Simulation always receives the fetched snapshot and requires complete traces.
		scenarios[i].request.Definition = draft.Draft
		requireComplete := true
		scenarios[i].scenario.RequireComplete = &requireComplete
	}
	policy, err := client.GetStoredWorkflowPublishPolicy(ctx, app)
	if err != nil {
		return printErr("Could not read publishing policy", err)
	}
	if policy.Version < 0 || (policy.Mode != "optional" && policy.Mode != "scenarios" && policy.Mode != "coverage") {
		return printErr("Invalid publishing policy", errors.New("server returned an unknown publishing requirement"))
	}
	coverageRequired := policy.Mode == "coverage" || (len(requireCoverage) > 0 && requireCoverage[0])
	if !jsonOutput {
		if _, err := fmt.Fprintf(osStdout, "Publishing policy: %s (version %d)\n", policy.Mode, policy.Version); err != nil {
			return printErr("Could not write publishing policy", err)
		}
	}
	diff, err := diffStoredWorkflowDefinition(draft)
	if err != nil {
		return printErr("Could not compare stored workflow definitions", err)
	}
	if !jsonOutput {
		if code := printStoredWorkflowDefinitionDiff(diff); code != 0 {
			return code
		}
	}
	report := storedWorkflowCheckedPublishReport{PublishingPolicy: policy, Diff: diff, Name: name, CheckedVersion: version, DefinitionHash: fmt.Sprintf("%x", sha256.Sum256(snapshot)), PublicationOutcome: "not_attempted", Checks: runStoredWorkflowScenarios(ctx, client, app, scenarios, coverageRequired)}
	if !report.Checks.Passed {
		report.Error = "scenario checks failed; publication was not requested"
		if report.Checks.Coverage != nil && report.Checks.Coverage.Required && !report.Checks.Coverage.Passed {
			report.Error = "coverage gate failed; publication was not requested"
		}
		return printStoredWorkflowCheckedPublishReport(report)
	}
	checkedAt := time.Now().UTC()
	evidence := storedWorkflowPublicationEvidence(report, checkedAt)
	if err := evidence.Validate(draft.Draft, version, checkedAt); err != nil {
		report.Error = "check metadata cannot be recorded; review scenario names and evidence size before publishing"
		return printStoredWorkflowCheckedPublishReport(report)
	}
	current, err := client.GetStoredWorkflow(ctx, app, name)
	if err != nil {
		report.Error = "could not confirm the saved draft after scenario checks"
		return printStoredWorkflowCheckedPublishReport(report)
	}
	currentSnapshot, err := json.Marshal(current.Draft)
	if err != nil || current.Version != version || current.Name != name || !equalStoredWorkflowScenarioJSON(currentSnapshot, snapshot) {
		report.Error = "saved draft changed during scenario checks; reload it and rerun"
		return printStoredWorkflowCheckedPublishReport(report)
	}
	// Independently rerun assertions on the server. Never fall back after rejection.
	verified, err := client.CheckStoredWorkflowPublication(ctx, app, name, storedWorkflowServerCheckRequest(version, scenarios, coverageRequired))
	if err != nil {
		report.Error = "server publishing checks failed; inspect the app publishing policy and rerun --scenarios (coverage policy requires all paths or reasoned exclusions)"
		return printStoredWorkflowCheckedPublishReport(report)
	}
	if verified.Receipt == "" || !time.Now().Before(verified.ExpiresAt) || !verified.Evidence.ServerVerified || verified.Evidence.Validate(draft.Draft, version, time.Now()) != nil {
		report.Error = "server returned an invalid or expired publishing receipt; publication was not requested"
		return printStoredWorkflowCheckedPublishReport(report)
	}
	evidence = &verified.Evidence
	// The server atomically enforces this version even if a mutation races the recheck.
	report.PublicationAttempted = true
	report.PublicationOutcome = "unknown"
	published, err := client.PublishStoredWorkflow(ctx, app, name, api.PublishStoredWorkflowRequest{ExpectedVersion: version, TakeOverManifest: takeover, CheckEvidence: evidence, CheckReceipt: verified.Receipt})
	if err != nil {
		report.Error = "publication was not confirmed; inspect the stored workflow status before retrying"
		var apiError *api.APIError
		if errors.As(err, &apiError) {
			report.PublicationHTTPStatus = apiError.Problem.Status
			report.PublicationErrorCode = apiError.Problem.Code
			switch apiError.Problem.Status {
			case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity:
				report.PublicationOutcome = "rejected"
				report.Error = fmt.Sprintf("publication rejected (HTTP %d); resolve the rejection before retrying", apiError.Problem.Status)
				if apiError.Problem.Status == http.StatusConflict {
					if apiError.Problem.Code == "stored_workflow_publish_check_required" {
						report.Error = "publishing receipt is missing, expired or stale; inspect the app policy and rerun scenario checks"
					} else {
						report.Error = "publication rejected because the draft version or ownership conflicted; reload the draft and rerun checks"
					}
				}
			}
		}
		return printStoredWorkflowCheckedPublishReport(report)
	}
	summary := summarizeStoredWorkflow(published)
	report.Published, report.StoredWorkflow = true, &summary
	report.PublicationOutcome = "confirmed"
	return printStoredWorkflowCheckedPublishReport(report)
}

func printStoredWorkflowCheckedPublishReport(report storedWorkflowCheckedPublishReport) int {
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		if code := printStoredWorkflowCheckReport(report.Checks); code != 0 {
			if !report.Checks.Passed {
				printErr("Stored workflow was not published", errors.New(report.Error))
			}
			return code
		}
		if report.Error != "" {
			title := "Stored workflow was not published"
			if report.PublicationAttempted {
				title = "Publication was not confirmed"
			}
			if report.PublicationOutcome == "rejected" {
				title = "Publication was rejected"
			}
			return printErr(title, errors.New(report.Error))
		}
		if report.StoredWorkflow != nil {
			if _, err := fmt.Fprintf(osStdout, "Published stored workflow %q at version %d after checking draft version %d.\n", report.Name, report.StoredWorkflow.Version, report.CheckedVersion); err != nil {
				return printErr("Could not write publish report", err)
			}
		}
	}
	if !report.Published {
		return 1
	}
	return 0
}

func storedWorkflowPublicationEvidence(report storedWorkflowCheckedPublishReport, checkedAt time.Time) *api.StoredWorkflowCheckEvidence {
	evidence := &api.StoredWorkflowCheckEvidence{
		DefinitionHash: report.DefinitionHash, CheckedVersion: report.CheckedVersion, CheckedAt: checkedAt,
		Scenarios: []api.StoredWorkflowCheckScenario{}, Exclusions: []api.StoredWorkflowCheckExclusion{},
		CoverageRemaining: len(unexcludedStoredWorkflowCoverageHints(report.Checks)),
	}
	evidence.CoveragePassed = evidence.CoverageRemaining == 0
	if report.Checks.Coverage != nil {
		evidence.CoverageRequired = report.Checks.Coverage.Required
		for _, x := range report.Checks.Coverage.Exclusions {
			evidence.Exclusions = append(evidence.Exclusions, api.StoredWorkflowCheckExclusion{Step: x.Step, Loop: x.Loop, Code: x.Code, Reason: x.Reason})
		}
	}
	for _, s := range report.Checks.Scenarios {
		evidence.Scenarios = append(evidence.Scenarios, api.StoredWorkflowCheckScenario{Name: s.Name, Passed: s.Passed, DefinitionValid: s.DefinitionValid, Complete: s.Complete})
	}
	return evidence
}
