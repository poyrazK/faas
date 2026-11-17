package operatorobs

import (
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func RuntimeProcessValid(out api.ObsRuntimeBuilds) bool {
	p := out.Process
	if p == nil {
		return false
	}
	c := p.Counts
	values := []int{c.Fresh, c.Missing, c.Stale, c.Conflicting, c.Unavailable, c.Compared, c.Changed, c.Unchanged, c.SameRevisionChanges, c.DifferentRevisionChanges, c.UnknownRevisionChanges, c.NewTargets, c.RemovedTargets, c.RepeatedSamples, c.ComparisonUnavailable}
	for _, n := range values {
		if n < 0 || n > len(out.Jobs)*(api.ObsRuntimeBuildSamplesMax+api.ObsRuntimeBuildNodesMax) {
			return false
		}
	}
	if c.Fresh+c.Missing+c.Stale+c.Conflicting+c.Unavailable != len(out.Items)+out.OmittedItems || c.Compared != c.Changed+c.Unchanged || c.Changed != c.SameRevisionChanges+c.DifferentRevisionChanges+c.UnknownRevisionChanges {
		return false
	}
	comparison := p.ComparisonAt != nil
	if comparison {
		at := p.ComparisonAt
		if at.IsZero() || !at.Before(out.GeneratedAt) || at.Before(out.GeneratedAt.Add(-api.ObsRuntimeProcessLookback)) || at.Before(out.EvidenceAfter.Add(-api.ObsHealthClockSkewTolerance)) || c.Fresh != c.Compared+c.NewTargets+c.RepeatedSamples+c.ComparisonUnavailable {
			return false
		}
	} else if c.Compared+c.NewTargets+c.RemovedTargets+c.RepeatedSamples+c.ComparisonUnavailable != 0 {
		return false
	}
	incomplete := c.Missing+c.Stale+c.Conflicting+c.Unavailable+c.NewTargets+c.RemovedTargets+c.RepeatedSamples+c.ComparisonUnavailable+out.Counts.Unexpected > 0
	for _, job := range out.Jobs {
		incomplete = incomplete || job.State != "available"
	}
	if incomplete && !p.Incomplete {
		return false
	}
	state, reason := runtimeProcessState(c, p.Incomplete, comparison)
	if p.State != state || p.Reason != reason {
		return false
	}
	counts := map[string]int{"fresh": c.Fresh, "missing": c.Missing, "stale": c.Stale, "conflicting": c.Conflicting, "unavailable": c.Unavailable}
	previews := map[string]int{"changed": c.Changed, "unchanged": c.Unchanged, "new_target": c.NewTargets, "repeated_sample": c.RepeatedSamples, "unavailable": c.ComparisonUnavailable}
	relations := map[string]int{"same": c.SameRevisionChanges, "different": c.DifferentRevisionChanges, "unknown": c.UnknownRevisionChanges}
	for _, item := range out.Items {
		if !runtimeProcessItemValid(item, out) {
			return false
		}
		r := item.Process
		counts[r.State]--
		if counts[r.State] < 0 {
			return false
		}
		if comparison && r.State == "fresh" {
			previews[r.Comparison]--
			if previews[r.Comparison] < 0 {
				return false
			}
		}
		if r.Comparison == "changed" {
			relations[r.RevisionRelation]--
			if relations[r.RevisionRelation] < 0 {
				return false
			}
		}
	}
	return true
}

func runtimeProcessItemValid(item api.ObsRuntimeBuildItem, out api.ObsRuntimeBuilds) bool {
	p := item.Process
	if p == nil {
		return false
	}
	if p.State != "fresh" {
		if p.UptimeSeconds != nil || p.ObservedAt != nil || p.PreviousUptimeSeconds != nil || p.PreviousObservedAt != nil || p.Comparison != "" || p.ComparisonReason != "" || p.RevisionRelation != "" {
			return false
		}
		switch p.State {
		case "missing":
			return slices.Contains([]string{"expected_target_missing", "uptime_missing"}, p.Reason)
		case "stale":
			return slices.Contains([]string{"scrape_stale", "uptime_stale"}, p.Reason)
		case "conflicting":
			return slices.Contains([]string{"conflicting_targets", "conflicting_uptime"}, p.Reason)
		case "unavailable":
			return slices.Contains([]string{"source_unavailable", "invalid_evidence", "invalid_sample_time", "scrape_before_boundary", "scrape_down", "uptime_before_boundary", "invalid_uptime"}, p.Reason)
		default:
			return false
		}
	}
	if p.Reason != "" || p.UptimeSeconds == nil || !runtimeUptimeValid(*p.UptimeSeconds) || !runtimeProcessTimeValid(p.ObservedAt, out.GeneratedAt, out.EvidenceAfter) || !runtimeProcessTimeValid(item.ScrapeObservedAt, out.GeneratedAt, out.EvidenceAfter) || item.TargetID == "" {
		return false
	}
	if out.Process.ComparisonAt == nil {
		return p.Comparison == "baseline" && p.ComparisonReason == "" && p.RevisionRelation == "" && p.PreviousObservedAt == nil && p.PreviousUptimeSeconds == nil
	}
	if item.Unexpected && (p.Comparison != "unavailable" || p.ComparisonReason != "unexpected_target" || p.PreviousObservedAt != nil || p.PreviousUptimeSeconds != nil) {
		return false
	}
	if (p.PreviousObservedAt == nil) != (p.PreviousUptimeSeconds == nil) {
		return false
	}
	if p.PreviousObservedAt == nil {
		return p.RevisionRelation == "" && (p.Comparison == "new_target" && p.ComparisonReason == "" || p.Comparison == "unavailable" && slices.Contains([]string{"previous_evidence_unavailable", "unexpected_target"}, p.ComparisonReason))
	}
	if !runtimeUptimeValid(*p.PreviousUptimeSeconds) || !runtimeProcessTimeValid(p.PreviousObservedAt, *out.Process.ComparisonAt, out.EvidenceAfter) {
		return false
	}
	state, reason := runtimeProcessComparison(*p.UptimeSeconds, *p.ObservedAt, *p.PreviousUptimeSeconds, *p.PreviousObservedAt)
	if p.Comparison != state || p.ComparisonReason != reason {
		return false
	}
	return state == "changed" && slices.Contains([]string{"same", "different", "unknown"}, p.RevisionRelation) || state != "changed" && p.RevisionRelation == ""
}

func runtimeProcessTimeValid(at *time.Time, now, after time.Time) bool {
	return at != nil && !at.IsZero() && !at.After(now.Add(api.ObsHealthClockSkewTolerance)) && !at.Before(now.Add(-api.ObsInputHealthMaxAge)) && !at.Before(after.Add(-api.ObsHealthClockSkewTolerance))
}

// SanitizeRuntimeProcess preserves identity and readiness during release skew.
func SanitizeRuntimeProcess(out api.ObsRuntimeBuilds) api.ObsRuntimeBuilds {
	if RuntimeProcessValid(out) {
		return out
	}
	out.Process = nil
	out.Items = slices.Clone(out.Items)
	for i := range out.Items {
		out.Items[i].Process = nil
	}
	return out
}
