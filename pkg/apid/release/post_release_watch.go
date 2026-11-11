package release

import (
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/canary"
)

// JudgePostRelease decides whether a completed release regressed against its
// predecessor's traffic from before the rollout (ADR-1445). With a full old
// sample it applies the canary's 5xx and p95 rules. A quiet predecessor
// cannot be compared, so only the ADR-625 absolute 5xx bar applies.
func JudgePostRelease(release, predecessor canary.HealthWindow) (rule, reason string, regressed bool) {
	if predecessor.Requests < canary.CircuitBreakerMinRequests {
		if release.ServerErrors >= api.RollbackOn5xxMinServerErrors &&
			release.ServerErrors*100 >= release.Requests*api.RollbackOn5xxMinErrorPct {
			return api.RolloutRuleAbortPostRelease5xx, fmt.Sprintf(
				"5xx after the rollout completed (%d/%d requests; predecessor sample %d too small to compare)",
				release.ServerErrors, release.Requests, predecessor.Requests), true
		}
		return "", "", false
	}
	if decision, ok := canary.ServerErrorRegression(release, predecessor); ok {
		return api.RolloutRuleAbortPostRelease5xx, "after the rollout completed: " + decision.Reason, true
	}
	if decision, ok := canary.LatencyRegression(release, predecessor); ok {
		return api.RolloutRuleAbortPostReleaseP95, "after the rollout completed: " + decision.Reason, true
	}
	return "", "", false
}

// ConfirmPostReleaseLatency re-judges a whole-app p95 regression over only the
// routes both releases served (ADR-1445 amendment 2026-11-10). A route the
// predecessor never served in its baseline, such as a new endpoint that is
// slow by design, cannot roll a release back by itself, and the shared sample
// must meet the canary request floor on both sides (Hunt 32 H32-33).
func ConfirmPostReleaseLatency(release, predecessor canary.HealthWindow) (rule, reason string, regressed bool) {
	decision, ok := canary.LatencyRegression(release, predecessor)
	if !ok {
		return "", "", false
	}
	return api.RolloutRuleAbortPostReleaseP95, "after the rollout completed: " + decision.Reason + " on routes both releases served", true
}

// PostReleaseDecision is the ADR-1447 record of a post-release rollback.
func PostReleaseDecision(rule, reason, predecessorID string, release, predecessor canary.HealthWindow, windowStart, windowEnd, now time.Time) api.RolloutDecision {
	sample := func(w canary.HealthWindow) *api.RolloutDecisionSample {
		return &api.RolloutDecisionSample{Requests: w.Requests, ServerErrors: w.ServerErrors, P95LatencyMS: w.P95LatencyMS,
			ColdBootRequests: w.ColdBootRequests, ColdBootP95LatencyMS: w.ColdBootP95LatencyMS}
	}
	return api.RolloutDecision{Kind: api.RolloutDecisionAbort, Rule: rule, Summary: reason, TrafficPercent: 100,
		WindowStart: windowStart.UTC().Format(time.RFC3339Nano), WindowEnd: windowEnd.UTC().Format(time.RFC3339Nano),
		StableDeploymentID: predecessorID, Candidate: sample(release), Stable: sample(predecessor),
		DecidedAt: now.UTC().Format(time.RFC3339Nano)}
}
