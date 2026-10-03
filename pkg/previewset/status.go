// Package previewset projects one PR's recorded workload set into readiness.
// Both the GitHub check and customer API use this evaluator so their answers
// cannot diverge when a sibling fails or a deployment is superseded.
package previewset

import (
	"fmt"
	"strings"
)

const (
	PhaseBuilding = "building"
	PhaseLive     = "live"
	PhaseFailed   = "failed"
)

type Member struct {
	AppID            string
	Slug             string
	PreviewOfSlug    string
	WorkloadName     string
	AppStatus        string
	PreviewState     string
	DeploymentID     string
	DeploymentStatus string
}

type Result struct {
	Phase          string
	Summary        string
	RootSlug       string
	RootParentSlug string
	LiveCount      int
	TotalCount     int
}

// Evaluate is fail-closed: only a live deployment for the recorded commit
// makes a member ready. The caller supplies the exact-head members.
func Evaluate(prNumber int, rootAppID string, members []Member) Result {
	result := Result{Phase: PhaseBuilding, TotalCount: len(members)}
	if len(members) == 0 {
		result.Phase = PhaseFailed
		result.Summary = fmt.Sprintf("Preview PR #%d has no recorded workloads.", prNumber)
		return result
	}
	waiting := make([]string, 0, 3)
	failed := ""
	for _, member := range members {
		if member.AppID == rootAppID {
			result.RootSlug = member.Slug
			result.RootParentSlug = member.PreviewOfSlug
		}
		name := strings.Join(strings.Fields(member.WorkloadName), " ")
		if name == "" {
			name = member.AppID
		}
		switch {
		case (member.AppStatus != "active" && member.AppStatus != "evicted_cold") || member.PreviewState != "open":
			if failed == "" {
				failed = fmt.Sprintf("%s is unavailable", name)
			}
		case member.DeploymentStatus == "failed" || member.DeploymentStatus == "cancelled" || member.DeploymentStatus == "superseded":
			if failed == "" {
				failed = fmt.Sprintf("%s deployment %s", name, member.DeploymentStatus)
			}
		case member.DeploymentStatus == PhaseLive:
			result.LiveCount++
		case member.DeploymentStatus == "missing" || member.DeploymentStatus == "pending" ||
			member.DeploymentStatus == PhaseBuilding || member.DeploymentStatus == "imaging" ||
			member.DeploymentStatus == "snapshotting":
			if len(waiting) < 3 {
				waiting = append(waiting, name)
			}
		default:
			if failed == "" {
				failed = fmt.Sprintf("%s has unknown deployment status", name)
			}
		}
	}
	if failed != "" {
		result.Phase = PhaseFailed
		result.Summary = fmt.Sprintf("Preview PR #%d failed: %s. %d/%d workloads live.", prNumber, failed, result.LiveCount, len(members))
		return result
	}
	if result.LiveCount == len(members) {
		result.Phase = PhaseLive
		result.Summary = fmt.Sprintf("Preview PR #%d live: all %d workloads reached this commit.", prNumber, result.LiveCount)
		return result
	}
	result.Summary = fmt.Sprintf("Preview PR #%d building: %d/%d workloads live; waiting for %s.",
		prNumber, result.LiveCount, len(members), strings.Join(waiting, ", "))
	return result
}
