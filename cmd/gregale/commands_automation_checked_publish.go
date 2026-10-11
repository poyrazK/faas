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

type automationCheckedPublishReport struct {
	PublishingPolicy      api.AutomationPublishPolicy `json:"publishing_policy"`
	Diff                  automationDefinitionDiff    `json:"diff"`
	Name                  string                      `json:"name"`
	CheckedVersion        int64                       `json:"checked_version"`
	DefinitionHash        string                      `json:"definition_hash"`
	Checks                automationCheckReport       `json:"checks"`
	Published             bool                        `json:"published"`
	PublicationAttempted  bool                        `json:"publication_attempted"`
	PublicationOutcome    string                      `json:"publication_outcome"`
	PublicationHTTPStatus int                         `json:"publication_http_status,omitempty"`
	PublicationErrorCode  string                      `json:"publication_error_code,omitempty"`
	Automation            *automationMutationSummary  `json:"automation,omitempty"`
	Error                 string                      `json:"error,omitempty"`
}

func publishAutomationWithScenarios(client *api.Client, app, name string, version int64, takeover bool, scenarios []loadedAutomationScenario, requireCoverage ...bool) int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	draft, err := client.GetAutomation(ctx, app, name)
	if err != nil {
		return printErr("Could not load automation draft", err)
	}
	if draft.Version != version || draft.Name != name || draft.Draft.Name != name {
		return printErr("Automation draft changed", errors.New("name and version must match the saved draft; reload it before publishing"))
	}
	snapshot, err := json.Marshal(draft.Draft)
	if err != nil {
		return printErr("Invalid saved automation draft", err)
	}
	for i := range scenarios {
		local, err := json.Marshal(scenarios[i].request.Definition)
		if err != nil || !equalAutomationScenarioJSON(local, snapshot) {
			return printErr("Scenario definition differs from saved draft", errors.New("apply the intended definition and use its returned version before publishing"))
		}
		// Simulation always receives the fetched snapshot and requires complete traces.
		scenarios[i].request.Definition = draft.Draft
		requireComplete := true
		scenarios[i].scenario.RequireComplete = &requireComplete
	}
	policy, err := client.GetAutomationPublishPolicy(ctx, app)
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
	diff, err := diffAutomationDefinition(draft)
	if err != nil {
		return printErr("Could not compare automation definitions", err)
	}
	if !jsonOutput {
		if code := printAutomationDefinitionDiff(diff); code != 0 {
			return code
		}
	}
	report := automationCheckedPublishReport{PublishingPolicy: policy, Diff: diff, Name: name, CheckedVersion: version, DefinitionHash: fmt.Sprintf("%x", sha256.Sum256(snapshot)), PublicationOutcome: "not_attempted", Checks: runAutomationScenarios(ctx, client, app, scenarios, coverageRequired)}
	if !report.Checks.Passed {
		report.Error = "scenario checks failed; publication was not requested"
		if report.Checks.Coverage != nil && report.Checks.Coverage.Required && !report.Checks.Coverage.Passed {
			report.Error = "coverage gate failed; publication was not requested"
		}
		return printAutomationCheckedPublishReport(report)
	}
	checkedAt := time.Now().UTC()
	evidence := automationPublicationEvidence(report, checkedAt)
	if err := evidence.Validate(draft.Draft, version, checkedAt); err != nil {
		report.Error = "check metadata cannot be recorded; review scenario names and evidence size before publishing"
		return printAutomationCheckedPublishReport(report)
	}
	current, err := client.GetAutomation(ctx, app, name)
	if err != nil {
		report.Error = "could not confirm the saved draft after scenario checks"
		return printAutomationCheckedPublishReport(report)
	}
	currentSnapshot, err := json.Marshal(current.Draft)
	if err != nil || current.Version != version || current.Name != name || !equalAutomationScenarioJSON(currentSnapshot, snapshot) {
		report.Error = "saved draft changed during scenario checks; reload it and rerun"
		return printAutomationCheckedPublishReport(report)
	}
	// Independently rerun assertions on the server. Never fall back after rejection.
	verified, err := client.CheckAutomationPublication(ctx, app, name, automationServerCheckRequest(version, scenarios, coverageRequired))
	if err != nil {
		report.Error = "server publishing checks failed; inspect the app publishing policy and rerun --scenarios (coverage policy requires all paths or reasoned exclusions)"
		return printAutomationCheckedPublishReport(report)
	}
	if verified.Receipt == "" || !time.Now().Before(verified.ExpiresAt) || !verified.Evidence.ServerVerified || verified.Evidence.Validate(draft.Draft, version, time.Now()) != nil {
		report.Error = "server returned an invalid or expired publishing receipt; publication was not requested"
		return printAutomationCheckedPublishReport(report)
	}
	evidence = &verified.Evidence
	// The server atomically enforces this version even if a mutation races the recheck.
	report.PublicationAttempted = true
	report.PublicationOutcome = "unknown"
	published, err := client.PublishAutomation(ctx, app, name, api.PublishAutomationRequest{ExpectedVersion: version, TakeOverManifest: takeover, CheckEvidence: evidence, CheckReceipt: verified.Receipt})
	if err != nil {
		report.Error = "publication was not confirmed; inspect the automation status before retrying"
		var apiError *api.APIError
		if errors.As(err, &apiError) {
			report.PublicationHTTPStatus = apiError.Problem.Status
			report.PublicationErrorCode = apiError.Problem.Code
			switch apiError.Problem.Status {
			case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity:
				report.PublicationOutcome = "rejected"
				report.Error = fmt.Sprintf("publication rejected (HTTP %d); resolve the rejection before retrying", apiError.Problem.Status)
				if apiError.Problem.Status == http.StatusConflict {
					if apiError.Problem.Code == "automation_publish_check_required" {
						report.Error = "publishing receipt is missing, expired or stale; inspect the app policy and rerun scenario checks"
					} else {
						report.Error = "publication rejected because the draft version or ownership conflicted; reload the draft and rerun checks"
					}
				}
			}
		}
		return printAutomationCheckedPublishReport(report)
	}
	summary := summarizeAutomation(published)
	report.Published, report.Automation = true, &summary
	report.PublicationOutcome = "confirmed"
	return printAutomationCheckedPublishReport(report)
}

func printAutomationCheckedPublishReport(report automationCheckedPublishReport) int {
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		if code := printAutomationCheckReport(report.Checks); code != 0 {
			if !report.Checks.Passed {
				printErr("Automation was not published", errors.New(report.Error))
			}
			return code
		}
		if report.Error != "" {
			title := "Automation was not published"
			if report.PublicationAttempted {
				title = "Publication was not confirmed"
			}
			if report.PublicationOutcome == "rejected" {
				title = "Publication was rejected"
			}
			return printErr(title, errors.New(report.Error))
		}
		if report.Automation != nil {
			if _, err := fmt.Fprintf(osStdout, "Published automation %q at version %d after checking draft version %d.\n", report.Name, report.Automation.Version, report.CheckedVersion); err != nil {
				return printErr("Could not write publish report", err)
			}
		}
	}
	if !report.Published {
		return 1
	}
	return 0
}

func automationPublicationEvidence(report automationCheckedPublishReport, checkedAt time.Time) *api.AutomationCheckEvidence {
	evidence := &api.AutomationCheckEvidence{
		DefinitionHash: report.DefinitionHash, CheckedVersion: report.CheckedVersion, CheckedAt: checkedAt,
		Scenarios: []api.AutomationCheckScenario{}, Exclusions: []api.AutomationCheckExclusion{},
		CoverageRemaining: len(unexcludedAutomationCoverageHints(report.Checks)),
	}
	evidence.CoveragePassed = evidence.CoverageRemaining == 0
	if report.Checks.Coverage != nil {
		evidence.CoverageRequired = report.Checks.Coverage.Required
		for _, x := range report.Checks.Coverage.Exclusions {
			evidence.Exclusions = append(evidence.Exclusions, api.AutomationCheckExclusion{Step: x.Step, Loop: x.Loop, Code: x.Code, Reason: x.Reason})
		}
	}
	for _, s := range report.Checks.Scenarios {
		evidence.Scenarios = append(evidence.Scenarios, api.AutomationCheckScenario{Name: s.Name, Passed: s.Passed, DefinitionValid: s.DefinitionValid, Complete: s.Complete})
	}
	return evidence
}
