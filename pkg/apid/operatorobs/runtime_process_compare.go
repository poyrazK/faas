package operatorobs

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func compareRuntimeProcessJob(current *runtimeBuildJobResult, previous *runtimeBuildJobResult, selected RuntimeBuildSelection) {
	current.processIncomplete = current.job.State != "available" || previous != nil && previous.job.State != "available"
	prior := map[string]api.ObsRuntimeBuildItem{}
	if previous != nil {
		for _, item := range previous.items {
			if item.TargetID != "" {
				prior[item.TargetID] = item
			}
		}
	}
	seen := map[string]bool{}
	for i := range current.items {
		item := &current.items[i]
		if item.Process == nil {
			item.Process = &api.ObsRuntimeProcessItem{State: "unavailable", Reason: "source_unavailable"}
		}
		p := item.Process
		c := &current.processCounts
		if item.TargetID != "" {
			seen[item.TargetID] = true
		}
		current.processIncomplete = current.processIncomplete || item.Unexpected
		switch p.State {
		case "fresh":
			c.Fresh++
		case "missing":
			c.Missing++
		case "stale":
			c.Stale++
		case "conflicting":
			c.Conflicting++
		default:
			c.Unavailable++
		}
		if p.State != "fresh" {
			continue
		}
		if selected.compareAt.IsZero() {
			p.Comparison = "baseline"
			continue
		}
		old, exists := prior[item.TargetID]
		switch {
		case item.Unexpected:
			p.Comparison, p.ComparisonReason = "unavailable", "unexpected_target"
		case previous == nil || previous.job.State != "available":
			p.Comparison, p.ComparisonReason = "unavailable", "previous_evidence_unavailable"
		case !exists:
			p.Comparison = "new_target"
		case old.Process == nil || old.Process.State != "fresh":
			p.Comparison, p.ComparisonReason = "unavailable", "previous_evidence_unavailable"
		default:
			compareRuntimeProcessItem(item, old)
		}
		switch p.Comparison {
		case "changed":
			c.Compared++
			c.Changed++
			switch p.RevisionRelation {
			case "same":
				c.SameRevisionChanges++
			case "different":
				c.DifferentRevisionChanges++
			default:
				c.UnknownRevisionChanges++
			}
		case "unchanged":
			c.Compared++
			c.Unchanged++
		case "new_target":
			c.NewTargets++
		case "repeated_sample":
			c.RepeatedSamples++
		default:
			c.ComparisonUnavailable++
		}
	}
	if previous != nil && previous.job.State == "available" && current.job.State == "available" {
		for id, item := range prior {
			if !seen[id] && item.Process != nil && item.Process.State == "fresh" {
				current.processCounts.RemovedTargets++
			}
		}
	}
	c := current.processCounts
	current.processIncomplete = current.processIncomplete || c.Missing+c.Stale+c.Conflicting+c.Unavailable+c.NewTargets+c.RemovedTargets+c.RepeatedSamples+c.ComparisonUnavailable > 0
}

func compareRuntimeProcessItem(item *api.ObsRuntimeBuildItem, previous api.ObsRuntimeBuildItem) {
	p, old := item.Process, previous.Process
	uptime, at := *old.UptimeSeconds, *old.ObservedAt
	p.PreviousUptimeSeconds, p.PreviousObservedAt = &uptime, &at
	p.Comparison, p.ComparisonReason = runtimeProcessComparison(*p.UptimeSeconds, *p.ObservedAt, uptime, at)
	if p.Comparison == "changed" {
		p.RevisionRelation = "unknown"
		if item.GitSHA != "" && previous.GitSHA != "" {
			p.RevisionRelation = "different"
			if item.GitSHA == previous.GitSHA {
				p.RevisionRelation = "same"
			}
		}
	}
}

func runtimeProcessComparison(uptime float64, at time.Time, priorUptime float64, priorAt time.Time) (string, string) {
	if at.Equal(priorAt) && uptime == priorUptime {
		return "repeated_sample", ""
	}
	if !at.After(priorAt) {
		return "unavailable", "sample_clock_regressed"
	}
	drift := at.Sub(priorAt).Seconds() - (uptime - priorUptime)
	switch {
	case uptime+api.ObsRuntimeProcessResetTolerance.Seconds() < priorUptime:
		return "changed", ""
	case drift > api.ObsRuntimeProcessStartTolerance.Seconds():
		return "changed", ""
	case drift < -api.ObsRuntimeProcessStartTolerance.Seconds():
		return "unavailable", "start_estimate_regressed"
	default:
		return "unchanged", ""
	}
}

func aggregateRuntimeProcess(out *api.ObsRuntimeBuilds, results []runtimeBuildJobResult, selected RuntimeBuildSelection) {
	process := &api.ObsRuntimeProcess{}
	if !selected.compareAt.IsZero() {
		at := selected.compareAt
		process.ComparisonAt = &at
	}
	for _, result := range results {
		process.Incomplete = process.Incomplete || result.processIncomplete
		addRuntimeProcessCounts(&process.Counts, result.processCounts)
	}
	process.State, process.Reason = runtimeProcessState(process.Counts, process.Incomplete, process.ComparisonAt != nil)
	out.Process = process
}

func runtimeProcessState(c api.ObsRuntimeProcessCounts, incomplete, comparison bool) (string, string) {
	switch {
	case c.Changed > 0:
		return "changed", "observed_process_change"
	case incomplete:
		return "incomplete", "incomplete_continuity_evidence"
	case !comparison && c.Fresh > 0:
		return "baseline", "awaiting_comparison"
	case c.Compared > 0:
		return "unchanged", ""
	default:
		return "unknown", "process_evidence_unavailable"
	}
}

func addRuntimeProcessCounts(out *api.ObsRuntimeProcessCounts, c api.ObsRuntimeProcessCounts) {
	out.Fresh += c.Fresh
	out.Missing += c.Missing
	out.Stale += c.Stale
	out.Conflicting += c.Conflicting
	out.Unavailable += c.Unavailable
	out.Compared += c.Compared
	out.Changed += c.Changed
	out.Unchanged += c.Unchanged
	out.SameRevisionChanges += c.SameRevisionChanges
	out.DifferentRevisionChanges += c.DifferentRevisionChanges
	out.UnknownRevisionChanges += c.UnknownRevisionChanges
	out.NewTargets += c.NewTargets
	out.RemovedTargets += c.RemovedTargets
	out.RepeatedSamples += c.RepeatedSamples
	out.ComparisonUnavailable += c.ComparisonUnavailable
}
