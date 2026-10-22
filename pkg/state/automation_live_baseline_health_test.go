// adr: 1304
package state_test

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/automationgate"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBaselineHealthSeparatesHistoricalEvidenceFromRenewal(t *testing.T) {
	for _, mode := range []string{"passed", "stale", "outdated", "invalidated", "missing-run", "missing-suite", "failed", "future"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
			age := int64(60)
			policy := automationgate.CoveragePolicy{Version: 2, RequiredCases: []string{"happy"}, MaxCandidateAgeSeconds: &age}
			raw, _ := json.Marshal(policy)
			summary := api.AutomationLiveSuiteSummary{ID: uuid.NewString(), Name: "report", Version: 3, PublishedVersion: 2, DeploymentID: uuid.NewString(), CaseCount: 1, LinkedRuns: 1, CreatedAt: now.Add(-30 * time.Second), UpdatedAt: now.Add(-30 * time.Second)}
			receipt := api.AutomationLiveBaseline{Suite: summary, Name: "report", Policy: raw, PolicyHash: policy.Hash()}
			view := api.AutomationLiveSuite{AutomationLiveSuiteSummary: summary, Cases: []api.AutomationLiveSuiteCase{{Name: "happy", RunID: uuid.NewString(), Verification: &api.AutomationLiveVerification{RunStatus: "succeeded", InputsHash: "frozen", CreatedAt: now.Add(-30 * time.Second), ReportCurrent: true, Report: &api.AutomationLiveReport{Passed: true}}}}}
			expectedEvidence, expectedRenewal, reason := "usable", "passed", ""
			switch mode {
			case "stale":
				now = now.Add(61 * time.Second)
				expectedRenewal = "inconclusive"
				reason = "candidate_evidence_stale"
			case "outdated":
				view.Outdated = true
				expectedRenewal = "inconclusive"
				reason = "candidate_outdated"
			case "invalidated":
				view.Cases[0].Verification.ReportCurrent = false
				expectedEvidence = "unavailable"
				expectedRenewal = "inconclusive"
				reason = "unknown_results"
			case "missing-run":
				view.Cases[0].Verification = nil
				expectedEvidence = "unavailable"
				expectedRenewal = "inconclusive"
				reason = "candidate_evidence_age_unavailable"
			case "failed":
				view.Cases[0].Verification.Report.Passed = false
				expectedEvidence = "failed"
				expectedRenewal = "failed"
				reason = "candidate_failed"
			case "future":
				view.Cases[0].Verification.CreatedAt = now.Add(time.Second)
				expectedRenewal = "inconclusive"
				reason = "candidate_evidence_future"
			case "missing-suite":
				expectedEvidence = "unavailable"
				expectedRenewal = "inconclusive"
				reason = "suite_unavailable"
			}
			var selected *api.AutomationLiveSuite = &view
			if mode == "missing-suite" {
				selected = nil
			}
			result := state.BaselineHealth(receipt, selected, now)
			if result.EvidenceStatus != expectedEvidence || result.RenewalStatus != expectedRenewal || (reason != "" && !slices.Contains(result.Reasons, reason)) {
				t.Fatalf("health %+v", result)
			}
			if mode != "missing-suite" && (len(result.Cases) != 1 || result.Cases[0].RunID != view.Cases[0].RunID) {
				t.Fatal("lost inspection identity")
			}
		})
	}
}
