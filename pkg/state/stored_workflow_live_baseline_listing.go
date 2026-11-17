// adr: 1304
package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storedworkflowgate"
)

// Listing is bounded by the existing named-baseline quota and observes heads
// and current retained evidence together without checking or repairing reports.
type StoredWorkflowLiveBaselineListingStore interface {
	ListStoredWorkflowLiveBaselines(context.Context, string, string) (api.ListStoredWorkflowLiveBaselinesResponse, error)
}

func baselineHealth(receipt api.StoredWorkflowLiveBaseline, view *api.StoredWorkflowLiveSuite, now time.Time) api.StoredWorkflowLiveBaselineHealth {
	out := api.StoredWorkflowLiveBaselineHealth{Baseline: cloneBaseline(receipt), EvidenceStatus: "unavailable", RenewalStatus: "inconclusive", Reasons: []string{}, Cases: []api.StoredWorkflowLiveBaselineCaseHealth{}}
	if view == nil {
		out.Reasons = append(out.Reasons, "suite_unavailable")
		return out
	}
	out.Outdated = view.Outdated
	comparison := compareLiveSuites(*view, *view)
	unavailable, failed := len(view.Cases) == 0, false
	for _, entry := range comparison.Cases {
		observation := entry.Candidate
		if observation == nil {
			unavailable = true
			continue
		}
		out.Cases = append(out.Cases, api.StoredWorkflowLiveBaselineCaseHealth{Name: entry.Name, RunID: observation.RunID, Status: observation.Result})
		unavailable = unavailable || entry.Change == "unknown" || (observation.Result != "passed" && observation.Result != "failed")
		failed = failed || observation.Result == "failed"
	}
	if !unavailable {
		out.EvidenceStatus = "usable"
		if failed {
			out.EvidenceStatus = "failed"
		}
	}
	policy, err := storedworkflowgate.ParseCoveragePolicy(receipt.Policy)
	if err != nil || policy.Version != 2 || policy.Hash() != receipt.PolicyHash {
		out.Reasons = append(out.Reasons, "policy_unavailable")
		return out
	}
	out.MaxAgeSeconds = policy.MaxCandidateAgeSeconds
	selection := storedworkflowgate.Selection{Name: receipt.Name, BaselineID: receipt.Suite.ID, CandidateID: receipt.Suite.ID, Coverage: &policy, Expected: storedworkflowgate.Target{Version: receipt.Suite.Version, PublishedVersion: receipt.Suite.PublishedVersion, DeploymentID: receipt.Suite.DeploymentID}}
	result, err := storedworkflowgate.EvaluatePromotionAt(selection, comparison, now)
	if err != nil {
		out.EvidenceStatus = "unavailable"
		out.Reasons = append(out.Reasons, "evidence_invalid")
		return out
	}
	out.RenewalStatus = result.Outcome
	out.Reasons = result.Reasons
	if result.EvidenceAge != nil {
		age, oldest := result.EvidenceAge.AgeSeconds, result.EvidenceAge.OldestRunCreatedAt
		out.AgeSeconds = &age
		if !oldest.IsZero() {
			out.OldestRunCreatedAt = &oldest
		}
	}
	return out
}
