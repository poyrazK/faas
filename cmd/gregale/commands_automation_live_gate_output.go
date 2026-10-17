package main

import (
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/automationgate"
)

type automationLiveGateReport struct {
	BaselineName     string                         `json:"baseline_name,omitempty"`
	ResolvedBaseline *api.AutomationLiveBaseline    `json:"resolved_baseline,omitempty"`
	SchemaVersion    int                            `json:"schema_version"`
	CoveragePolicy   *automationgate.CoveragePolicy `json:"coverage_policy,omitempty"`
	PolicyHash       string                         `json:"policy_hash,omitempty"`
	Policy           string                         `json:"policy"`
	App              string                         `json:"app"`
	Name             string                         `json:"name"`
	BaselineID       string                         `json:"baseline_id"`
	CandidateID      string                         `json:"candidate_id"`
	Expected         automationgate.Target          `json:"expected_target"`
	Message          string                         `json:"message"`
	automationgate.Result
	Comparison *api.AutomationLiveSuiteComparison `json:"comparison,omitempty"`
}

func automationLiveGateMessage(outcome string) string {
	switch outcome {
	case "passed":
		return "Automation gate passed for the expected current target. Every selected candidate case passed, with no regressions or unresolved coverage gaps."
	case "failed":
		return "Automation gate failed: the intended current candidate contains failed assertions."
	default:
		return "Automation gate is inconclusive: target identity or retained evidence does not meet the strict policy."
	}
}

func renderAutomationLiveGate(o automationLiveGateOptions, view *api.AutomationLiveSuiteComparison, result automationgate.Result, message string) int {
	report := automationLiveGateReport{BaselineName: o.baselineName, ResolvedBaseline: o.resolvedBaseline, SchemaVersion: 1, Policy: automationgate.Policy, App: o.app, Name: o.Name, BaselineID: o.BaselineID, CandidateID: o.CandidateID, Expected: o.Expected, Message: message, Result: result, Comparison: view}
	if o.Coverage != nil {
		report.CoveragePolicy = o.Coverage
		report.PolicyHash = o.Coverage.Hash()
	}
	var err error
	if jsonOutput {
		err = writeJSON(report)
	} else {
		err = writeAutomationLiveGateText(report)
	}
	if err != nil {
		return printErr("Could not write automation gate report", &exitErr{msg: "The gate report could not be written.", code: 3})
	}
	return result.ExitCode
}

func writeAutomationLiveGateText(report automationLiveGateReport) error {
	if _, err := fmt.Fprintf(osStdout, "%s\nBaseline: %s\nCandidate: %s\nExpected target: saved version %d, published revision %d, deployment %s\n", report.Message, report.BaselineID, report.CandidateID, report.Expected.Version, report.Expected.PublishedVersion, report.Expected.DeploymentID); err != nil {
		return err
	}
	if report.ResolvedBaseline != nil {
		if _, err := fmt.Fprintf(osStdout, "Named baseline: %s revision %d (promotion %s)\n", report.BaselineName, report.ResolvedBaseline.Revision, report.ResolvedBaseline.PromotionID); err != nil {
			return err
		}
	}
	if report.CoveragePolicy != nil {
		if _, err := fmt.Fprintf(osStdout, "Coverage policy SHA-256: %s\nMissing required cases: %s\n", report.PolicyHash, strings.Join(report.MissingRequiredCases, ", ")); err != nil {
			return err
		}
	}
	if report.EvidenceAge != nil {
		age := report.EvidenceAge
		if _, err := fmt.Fprintf(osStdout, "Candidate evidence age: %.3f seconds (maximum %d seconds; evaluated %s; oldest run created %s)\n", age.AgeSeconds, age.MaxAgeSeconds, age.EvaluatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"), age.OldestRunCreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00")); err != nil {
			return err
		}
	}
	if len(report.Reasons) > 0 {
		if _, err := fmt.Fprintf(osStdout, "Reasons: %s\n", strings.Join(report.Reasons, ", ")); err != nil {
			return err
		}
	}
	if report.Comparison != nil {
		for _, entry := range report.Comparison.Cases {
			before, after := "not_selected", "not_selected"
			if entry.Baseline != nil {
				before = entry.Baseline.Result
			}
			if entry.Candidate != nil {
				after = entry.Candidate.Result
			}
			if _, err := fmt.Fprintf(osStdout, "%s: %s (baseline %s, candidate %s)\n", entry.Name, entry.Change, before, after); err != nil {
				return err
			}
		}
	}
	return nil
}

func failAutomationLiveGate(o automationLiveGateOptions, reason, message string, code, status int) int {
	problem := api.Problem{Status: status, Code: "automation_gate_" + reason, Title: "Automation gate could not be evaluated", Detail: message, DocsURL: cliDocsURL}
	if jsonOutput {
		if err := writeJSONProblemWithExit(osStderr, problem, code); err != nil {
			return 3
		}
	} else {
		renderAPIError(osStderr, &api.APIError{Problem: problem})
	}
	outcome := "error"
	if code == automationgate.ExitInconclusive {
		outcome = "inconclusive"
	}
	if reason == "interrupted" {
		outcome = "interrupted"
	}
	return renderAutomationLiveGate(o, nil, automationgate.Result{Outcome: outcome, ExitCode: code, Reasons: []string{reason}}, message)
}
