package operatorobs

import (
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// RuntimeReadinessValid validates optional readiness independently of identity.
// Older servers may omit it; clients must never infer readiness from a SHA.
func RuntimeReadinessValid(out api.ObsRuntimeBuilds) bool {
	r := out.Readiness
	if r == nil {
		return false
	}
	c := r.Counts
	total := c.Ready + c.NotReady + c.Missing + c.Stale + c.Conflicting + c.Unavailable
	if total != len(out.Items)+out.OmittedItems || len(r.Reasons) > api.ObsRuntimeReadinessReasonsMax {
		return false
	}
	for _, n := range []int{c.Ready, c.NotReady, c.Missing, c.Stale, c.Conflicting, c.Unavailable} {
		if n < 0 || n > len(out.Jobs)*(api.ObsRuntimeBuildSamplesMax+api.ObsRuntimeBuildNodesMax) {
			return false
		}
	}
	incomplete := out.Counts.Unexpected > 0
	for _, job := range out.Jobs {
		incomplete = incomplete || job.State != "available"
	}
	state, reason := runtimeReadinessState(c, incomplete)
	if r.State != state || r.Reason != reason {
		return false
	}
	classes := runtimeReadinessReasons()
	previous, notReady, ready := -1, 0, 0
	for _, item := range r.Reasons {
		index := slices.Index(classes, item.Reason)
		if index <= previous || item.Count <= 0 || item.Count > total {
			return false
		}
		previous = index
		if item.Reason == "ready" {
			ready += item.Count
		} else {
			notReady += item.Count
		}
	}
	if ready != c.Ready || notReady != c.NotReady {
		return false
	}
	counts := map[string]int{"ready": c.Ready, "not_ready": c.NotReady, "missing": c.Missing, "stale": c.Stale, "conflicting": c.Conflicting, "unavailable": c.Unavailable}
	previewReasons := map[string]int{}
	for _, item := range out.Items {
		if !runtimeReadinessItemValid(item, out) {
			return false
		}
		counts[item.Readiness.State]--
		if counts[item.Readiness.State] < 0 {
			return false
		}
		if item.Readiness.State == "ready" || item.Readiness.State == "not_ready" {
			previewReasons[item.Readiness.Reason]++
		}
	}
	for class, n := range previewReasons {
		found := slices.IndexFunc(r.Reasons, func(item api.ObsRuntimeReadinessReason) bool { return item.Reason == class && item.Count >= n })
		if found < 0 {
			return false
		}
	}
	return true
}

func runtimeReadinessItemValid(item api.ObsRuntimeBuildItem, out api.ObsRuntimeBuilds) bool {
	r := item.Readiness
	if r == nil {
		return false
	}
	for _, at := range []*time.Time{r.ObservedAt, r.ReasonObservedAt} {
		if at != nil && (at.IsZero() || at.After(out.GeneratedAt.Add(api.ObsHealthClockSkewTolerance))) {
			return false
		}
	}
	switch r.State {
	case "ready", "not_ready":
		if item.TargetID == "" || !slices.Contains(runtimeReadinessReasons(), r.Reason) || (r.State == "ready") != (r.Reason == "ready") {
			return false
		}
		for _, at := range []*time.Time{r.ObservedAt, r.ReasonObservedAt, item.ScrapeObservedAt} {
			if at == nil || at.Before(out.GeneratedAt.Add(-api.ObsInputHealthMaxAge)) || at.Before(out.EvidenceAfter.Add(-api.ObsHealthClockSkewTolerance)) {
				return false
			}
		}
		return true
	case "missing":
		return slices.Contains([]string{"expected_target_missing", "readiness_missing"}, r.Reason)
	case "stale":
		return slices.Contains([]string{"scrape_stale", "readiness_stale"}, r.Reason)
	case "conflicting":
		return slices.Contains([]string{"conflicting_targets", "conflicting_readiness"}, r.Reason)
	case "unavailable":
		return slices.Contains([]string{"source_unavailable", "invalid_evidence", "invalid_sample_time", "scrape_before_boundary", "scrape_down", "readiness_before_boundary"}, r.Reason)
	default:
		return false
	}
}

// SanitizeRuntimeReadiness owns its item slice and drops only invalid optional
// readiness; valid identity evidence remains usable during version skew.
func SanitizeRuntimeReadiness(out api.ObsRuntimeBuilds) api.ObsRuntimeBuilds {
	if RuntimeReadinessValid(out) {
		return out
	}
	out.Readiness = nil
	out.Items = slices.Clone(out.Items)
	for i := range out.Items {
		out.Items[i].Readiness = nil
	}
	return out
}
