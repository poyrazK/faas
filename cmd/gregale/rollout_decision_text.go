package main

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

var rolloutNeedPerSide = regexp.MustCompile(`need (\d+) each`)

// describeRolloutDecision renders an ADR-1447 decision record as one line a
// person can act on. The numbers come from the record; the summary is the
// breaker's own aggregate-only reason and is the fallback for rules without a
// dedicated phrasing.
func describeRolloutDecision(d *api.RolloutDecision) string {
	if d == nil {
		return ""
	}
	c, s := d.Candidate, d.Stable
	switch d.Rule {
	case api.RolloutRuleAdvanceHealthy:
		if c != nil && s != nil {
			line := fmt.Sprintf("healthy: this release %d/%d 5xx, the current one %d/%d", c.ServerErrors, c.Requests, s.ServerErrors, s.Requests)
			if c.P95LatencyMS > 0 && s.P95LatencyMS > 0 {
				line += fmt.Sprintf("; p95 %.0f ms vs %.0f ms", c.P95LatencyMS, s.P95LatencyMS)
			}
			return line
		}
		return "healthy"
	case api.RolloutRuleAdvanceProbeEvidence:
		if c != nil && s != nil {
			return fmt.Sprintf("advanced on synthetic probes: this release %d/%d probe 5xx, the current one %d/%d",
				c.ProbeServerErrors, c.ProbeRequests, s.ProbeServerErrors, s.ProbeRequests)
		}
		return "advanced on synthetic probes"
	case api.RolloutRuleAdvanceLowTraffic:
		if c != nil && s != nil {
			return fmt.Sprintf("too little traffic to compare after the %s bound and no errors seen (this release %d requests, the current one %d)",
				api.CanaryLowTrafficMaxHold, c.Requests, s.Requests)
		}
		return fmt.Sprintf("too little traffic to compare after the %s bound and no errors seen", api.CanaryLowTrafficMaxHold)
	case api.RolloutRuleAdvanceNoPredecessor:
		return "no earlier release to compare with"
	case api.RolloutRuleAdvanceManual:
		return "advanced on request"
	case api.RolloutRuleAbortManual:
		return "aborted on request"
	case api.RolloutRuleHoldInsufficient:
		if c != nil && s != nil {
			line := fmt.Sprintf("waiting for enough traffic to compare: this release %d requests, the current one %d", c.Requests, s.Requests)
			if m := rolloutNeedPerSide.FindStringSubmatch(d.Summary); m != nil {
				line += " (" + m[1] + " each needed)"
			}
			if c.ProbeRequests > 0 || s.ProbeRequests > 0 {
				line += fmt.Sprintf("; probes %d and %d so far", c.ProbeRequests, s.ProbeRequests)
			}
			return line
		}
	case api.RolloutRuleHoldObservation:
		return "health data is unavailable; the rollout waits and retries"
	case api.RolloutRuleHoldAwaitingApproval:
		return strings.TrimSpace(d.Summary) + "; an eligible teammate approves with: gregale approvals approve <deployment>"
	case api.RolloutRuleAbortApprovalReject:
		return "aborted: " + strings.TrimSpace(d.Summary)
	case api.RolloutRuleAbortApprovalTimeout:
		return "aborted: the release " + strings.TrimSpace(d.Summary)
	}
	summary := strings.TrimSpace(d.Summary)
	if summary == "" {
		summary = strings.ReplaceAll(d.Rule, "_", " ")
	}
	if d.ProbesIncluded && !strings.Contains(summary, "probes") {
		summary += " (synthetic probes included)"
	}
	return summary
}

// renderRolloutDecisions prints the deployment's latest decision and current
// hold for `deployment summary` (ADR-1447).
func renderRolloutDecisions(w io.Writer, d api.DeploymentResponse) {
	if d.CanaryTotalSteps <= 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "rollout: %s at %d%% traffic (step %d/%d)\n",
		nonEmptyProgressState(d.RolloutState, d.Status), d.TrafficPercent, min(d.CanaryStep+1, d.CanaryTotalSteps), d.CanaryTotalSteps)
	if d.RolloutHold != nil {
		_, _ = fmt.Fprintf(w, "  held since %s: %s\n", d.RolloutHold.DecidedAt, describeRolloutDecision(d.RolloutHold))
	}
	if d.RolloutDecision != nil {
		_, _ = fmt.Fprintf(w, "  last decision (%s, %s): %s\n", d.RolloutDecision.Rule, d.RolloutDecision.DecidedAt, describeRolloutDecision(d.RolloutDecision))
	}
}
