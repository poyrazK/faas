package main

import "fmt"

// These codes apply only to the cleanup gate, independently of policy and run
// verdicts. Invalid options and report I/O retain the ordinary CLI error codes.
const (
	testCampaignStatusResourcesPresent       = 4
	testCampaignStatusVerificationIncomplete = 5
)

func testCampaignStatusCleanGate(report testCampaignStatusReport) (int, string) {
	live := report.LiveVerification
	if !report.SnapshotStable || live == nil || !live.SnapshotStable || !live.AllVerified || len(live.Resources) == 0 || live.Issue != nil {
		return testCampaignStatusVerificationIncomplete, "Cleanup gate: verification incomplete; inspect the live verification report."
	}
	present := 0
	for _, resource := range live.Resources {
		if resource.VerifiedAt == nil || resource.Issue != nil {
			return testCampaignStatusVerificationIncomplete, "Cleanup gate: verification incomplete; inspect the live verification report."
		}
		switch resource.Kind {
		case "consumer":
			switch resource.State {
			case "absent", "revoked", "cancelled":
			case "active", "pending":
				present++
			default:
				return testCampaignStatusVerificationIncomplete, "Cleanup gate: consumer state is unverified."
			}
		case "session", "registration":
			switch resource.State {
			case "absent":
			case "present":
				present++
			default:
				return testCampaignStatusVerificationIncomplete, "Cleanup gate: resource state is unverified."
			}
		default:
			return testCampaignStatusVerificationIncomplete, "Cleanup gate: unknown resource kind."
		}
	}
	if present > 0 && live.State == "verified_resources_present" && !live.ResourcesClean {
		return testCampaignStatusResourcesPresent, fmt.Sprintf("Cleanup gate: %d of %d resources remain non-clean.", present, len(live.Resources))
	}
	if present == 0 && live.State == "verified_clean" && live.ResourcesClean {
		return 0, fmt.Sprintf("Cleanup gate: all %d resources verified clean.", len(live.Resources))
	}
	return testCampaignStatusVerificationIncomplete, "Cleanup gate: inconsistent verification result."
}
