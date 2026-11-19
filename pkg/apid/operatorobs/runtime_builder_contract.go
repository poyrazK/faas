package operatorobs

import (
	"math"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func runtimeBuilderCountValues(c RuntimeBuilderCounts) []int {
	return []int{c.Expected, c.Observed, c.Complete, c.Missing, c.Stale, c.Conflicting, c.Unavailable, c.Unexpected, c.OverdueWorkers, c.WaitingWorkers, c.WorkingWorkers, c.DrainingWorkers, c.WaitingQueues, c.SaturatedSlots}
}

func runtimeBuilderTotal(c RuntimeBuilderCounts) int {
	return c.Complete + c.Missing + c.Stale + c.Conflicting + c.Unavailable
}

func RuntimeBuilderSummaryValid(base api.ObsRuntimeBuilds, summary RuntimeBuilderSummary) bool {
	job := slices.IndexFunc(base.Jobs, func(job api.ObsRuntimeBuildJob) bool { return job.Job == "builderd" && job.Daemon == "builderd" })
	if job < 0 || base.ExpectedRole != "single-box-v2" && base.ExpectedRole != "control-plane-v2" {
		return false
	}
	c := summary.Counts
	for _, n := range runtimeBuilderCountValues(c) {
		if n < 0 || n > (api.ObsRuntimeBuildSamplesMax+api.ObsRuntimeBuildNodesMax)*2 {
			return false
		}
	}
	total, workers := runtimeBuilderTotal(c), c.WaitingWorkers+c.WorkingWorkers+c.DrainingWorkers+c.OverdueWorkers
	if c.Expected != base.Jobs[job].Expected || total < c.Expected || total > api.ObsRuntimeBuildSamplesMax+api.ObsRuntimeBuildNodesMax || c.Observed > base.Counts.Observed || c.Observed > total || workers > c.Observed || c.Complete > workers || c.SaturatedSlots > c.Observed || c.WaitingQueues > 2*c.Observed || c.Unexpected > c.Observed {
		return false
	}
	incomplete := runtimeBuilderIncomplete(c, base.Jobs[job].State != "available")
	state, reason := runtimeBuilderState(c, incomplete)
	return summary.Incomplete == incomplete && summary.State == state && summary.Reason == reason
}

func RuntimeBuilderHealthValid(base api.ObsRuntimeBuilds, health *RuntimeBuilderHealth) bool {
	if health == nil || !RuntimeBuilderSummaryValid(base, health.RuntimeBuilderSummary) || len(health.Items) > api.ObsRuntimeBuildPreviewMax || health.OmittedItems < 0 || len(health.Items)+health.OmittedItems != runtimeBuilderTotal(health.Counts) {
		return false
	}
	preview, seen := RuntimeBuilderCounts{}, map[string]bool{}
	for _, item := range health.Items {
		key := item.TargetID
		if key == "" {
			key = "missing/" + item.Node
		}
		if seen[key] || !runtimeBuilderItemValid(base, item) {
			return false
		}
		seen[key] = true
		accountRuntimeBuilderItem(&preview, item)
	}
	full, part := runtimeBuilderCountValues(health.Counts), runtimeBuilderCountValues(preview)
	for i := 1; i < len(full); i++ {
		if part[i] > full[i] || health.OmittedItems == 0 && part[i] != full[i] {
			return false
		}
	}
	return true
}

func runtimeBuilderSignalValid(base api.ObsRuntimeBuilds, item RuntimeBuilderItem, signal RuntimeBuilderSignal, hasValues bool, worker bool) bool {
	valid := signal.State == "available" && !worker || worker && slices.Contains([]string{"waiting", "working", "draining", "overdue"}, signal.State)
	if valid {
		return hasValues && signal.Reason == "" && item.TargetID != "" && runtimeProcessTimeValid(item.ScrapeObservedAt, base.GeneratedAt, base.EvidenceAfter) && runtimeProcessTimeValid(signal.ObservedAt, base.GeneratedAt, base.EvidenceAfter)
	}
	if hasValues || signal.ObservedAt != nil {
		return false
	}
	switch signal.State {
	case "missing":
		return slices.Contains([]string{"expected_target_missing", "metric_missing"}, signal.Reason)
	case "stale":
		return slices.Contains([]string{"scrape_stale", "metric_stale"}, signal.Reason)
	case "conflicting":
		return slices.Contains([]string{"conflicting_targets", "conflicting_series"}, signal.Reason)
	case "unavailable":
		return slices.Contains([]string{"source_unavailable", "invalid_evidence", "invalid_sample_time", "scrape_before_boundary", "scrape_down", "metric_before_boundary", "invalid_value"}, signal.Reason)
	default:
		return false
	}
}

func runtimeBuilderItemValid(base api.ObsRuntimeBuilds, item RuntimeBuilderItem) bool {
	if item.Node != "" && !ServingProbeNodeValid(item.Node) || item.TargetID != "" && !runtimeTargetIDValid(item.TargetID) || item.Unexpected && item.TargetID == "" || len(item.Queues) != 2 || item.State != runtimeBuilderItemState(item) {
		return false
	}
	if at := item.ScrapeObservedAt; at != nil && (at.IsZero() || at.After(base.GeneratedAt.Add(api.ObsHealthClockSkewTolerance))) {
		return false
	}
	if !runtimeBuilderSignalValid(base, item, item.Worker.RuntimeBuilderSignal, item.Worker.Values != nil, true) || !runtimeBuilderSignalValid(base, item, item.Slots.RuntimeBuilderSignal, item.Slots.Values != nil, false) {
		return false
	}
	if v := item.Worker.Values; v != nil {
		state, age, allowance, ok := runtimeBuilderWorkerState(*v, base.GeneratedAt)
		if !ok || state != item.Worker.State || !runtimeUptimeValid(v.ProgressAgeSeconds) || math.Abs(v.ProgressAgeSeconds-age) > time.Microsecond.Seconds() || v.ProgressAllowanceSeconds != allowance {
			return false
		}
	}
	if v := item.Slots.Values; v != nil && (!runtimeBuilderCount(float64(v.Used)) || !runtimeBuilderCount(float64(v.Limit)) || v.Limit == 0 || v.Used > v.Limit) {
		return false
	}
	for i, q := range item.Queues {
		if q.Queue != []string{"build", "cancel"}[i] || !runtimeBuilderSignalValid(base, item, q.RuntimeBuilderSignal, q.Values != nil, false) || q.Values != nil && !runtimeBuilderQueueValid(*q.Values, base.GeneratedAt) {
			return false
		}
	}
	return true
}
