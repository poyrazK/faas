package operatorobs

import (
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func RuntimeLoopHealthValid(base api.ObsRuntimeBuilds, health *RuntimeLoopHealth) bool {
	if health == nil || len(health.Items) > api.ObsRuntimeBuildPreviewMax || health.OmittedItems < 0 {
		return false
	}
	specs := runtimeBuildJobSpecs(base.ExpectedRole)
	if len(specs) == 0 || len(base.Jobs) != len(specs) {
		return false
	}
	expected, maximum, perTarget := 0, 0, 0
	incomplete := false
	for i, job := range base.Jobs {
		if job.Job != specs[i].job || job.Daemon != specs[i].daemon {
			return false
		}
		n := len(runtimeLoopsForDaemon(job.Daemon))
		perTarget = max(perTarget, n)
		expected += job.Expected * n
		maximum += (api.ObsRuntimeBuildSamplesMax + api.ObsRuntimeBuildNodesMax) * n
		incomplete = incomplete || job.State != "available"
	}
	c := health.Counts
	for _, n := range []int{c.Expected, c.Observed, c.NoReportedStall, c.Stalled, c.Missing, c.Stale, c.Conflicting, c.Unavailable, c.ReportedStalls, c.Unexpected, health.OmittedItems} {
		if n < 0 || n > maximum {
			return false
		}
	}
	total := c.NoReportedStall + c.Stalled + c.Missing + c.Stale + c.Conflicting + c.Unavailable
	if c.Expected != expected || total < expected || c.Observed < base.Counts.Observed || c.Observed > perTarget*base.Counts.Observed || total != len(health.Items)+health.OmittedItems || c.Observed > total || c.NoReportedStall+c.Stalled > c.Observed || c.ReportedStalls < c.Stalled || c.ReportedStalls > c.Observed || c.Unexpected > c.Observed {
		return false
	}
	incomplete = incomplete || c.Missing+c.Stale+c.Conflicting+c.Unavailable+c.Unexpected > 0
	if health.Incomplete != incomplete {
		return false
	}
	state, reason := runtimeLoopState(c, incomplete)
	if health.State != state || health.Reason != reason {
		return false
	}
	preview := RuntimeLoopHealth{}
	seen := map[string]bool{}
	for _, item := range health.Items {
		if !runtimeLoopItemValid(base, item, specs) {
			return false
		}
		key := item.Job + "/" + item.Loop + "/" + item.TargetID + "/" + item.Node
		if seen[key] {
			return false
		}
		seen[key] = true
		accountRuntimeLoopItem(&preview, item)
	}
	p := preview.Counts
	return p.Observed <= c.Observed && p.NoReportedStall <= c.NoReportedStall && p.Stalled <= c.Stalled && p.Missing <= c.Missing && p.Stale <= c.Stale && p.Conflicting <= c.Conflicting && p.Unavailable <= c.Unavailable && p.ReportedStalls <= c.ReportedStalls && p.Unexpected <= c.Unexpected
}

func runtimeLoopItemValid(base api.ObsRuntimeBuilds, item RuntimeLoopItem, specs []runtimeBuildJobSpec) bool {
	known := slices.ContainsFunc(specs, func(spec runtimeBuildJobSpec) bool {
		return spec.job == item.Job && spec.daemon == item.Daemon && slices.Contains(runtimeLoopsForDaemon(spec.daemon), item.Loop)
	})
	if !known || item.Node != "" && !ServingProbeNodeValid(item.Node) || item.TargetID != "" && !runtimeTargetIDValid(item.TargetID) || item.Unexpected && item.TargetID == "" {
		return false
	}
	for _, at := range []*time.Time{item.ScrapeObservedAt, item.StallObservedAt, item.AgeObservedAt} {
		if at != nil && (at.IsZero() || at.After(base.GeneratedAt.Add(api.ObsHealthClockSkewTolerance))) {
			return false
		}
	}
	freshUp := runtimeProcessTimeValid(item.ScrapeObservedAt, base.GeneratedAt, base.EvidenceAfter) && item.TargetID != ""
	if item.ReportedStalled != nil && (!freshUp || !runtimeProcessTimeValid(item.StallObservedAt, base.GeneratedAt, base.EvidenceAfter)) {
		return false
	}
	if item.AgeSeconds != nil && (!freshUp || !runtimeUptimeValid(*item.AgeSeconds) || !runtimeProcessTimeValid(item.AgeObservedAt, base.GeneratedAt, base.EvidenceAfter)) {
		return false
	}
	if item.ReportedStalled != nil && slices.Contains([]string{"expected_target_missing", "loop_stall_missing", "invalid_stall_value", "scrape_stale", "scrape_before_boundary", "scrape_down", "conflicting_targets"}, item.Reason) {
		return false
	}
	if item.AgeSeconds != nil && slices.Contains([]string{"expected_target_missing", "loop_age_missing", "invalid_loop_age", "scrape_stale", "scrape_before_boundary", "scrape_down", "conflicting_targets"}, item.Reason) {
		return false
	}
	switch item.State {
	case "stalled", "no_reported_stall":
		return item.Reason == "" && item.ReportedStalled != nil && item.AgeSeconds != nil && (item.State == "stalled") == *item.ReportedStalled
	case "missing":
		return slices.Contains([]string{"expected_target_missing", "loop_stall_missing", "loop_age_missing"}, item.Reason)
	case "stale":
		return slices.Contains([]string{"scrape_stale", "loop_sample_stale"}, item.Reason)
	case "conflicting":
		return slices.Contains([]string{"conflicting_targets", "conflicting_loop_series"}, item.Reason)
	case "unavailable":
		return slices.Contains([]string{"source_unavailable", "invalid_evidence", "invalid_sample_time", "scrape_before_boundary", "scrape_down", "loop_sample_before_boundary", "invalid_stall_value", "invalid_loop_age"}, item.Reason)
	default:
		return false
	}
}
