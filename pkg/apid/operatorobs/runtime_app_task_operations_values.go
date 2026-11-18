package operatorobs

import (
	"github.com/onebox-faas/faas/pkg/api"
	"math"
	"slices"
	"time"
)

func runtimeAppTaskOperationPointValid(p *RuntimeAppTaskOperationPoint, now, after time.Time) bool {
	return p != nil && p.Value >= 0 && p.Value <= api.ObsRuntimeAppTaskOperationCounterMax && runtimeProcessTimeValid(&p.ObservedAt, now, after)
}
func runtimeAppTaskOperationComparison(current, previous *RuntimeAppTaskOperationPoint, process *api.ObsRuntimeProcessItem) (string, string) {
	if process == nil || process.State != "fresh" {
		return "unavailable", "process_evidence_unavailable"
	}
	if process.Comparison != "unchanged" {
		switch process.Comparison {
		case "changed":
			return "unavailable", "process_changed"
		case "new_target":
			return "unavailable", "new_target"
		case "repeated_sample":
			return "unavailable", "repeated_process_sample"
		}
		return "unavailable", "process_comparison_unavailable"
	}
	if previous == nil {
		return "unavailable", "previous_evidence_unavailable"
	}
	if process.ObservedAt == nil || process.PreviousObservedAt == nil || !current.ObservedAt.Equal(*process.ObservedAt) || !previous.ObservedAt.Equal(*process.PreviousObservedAt) || !current.ObservedAt.After(previous.ObservedAt) {
		return "unavailable", "sample_clock_mismatch"
	}
	if process.UptimeSeconds == nil || process.PreviousUptimeSeconds == nil || math.Abs(current.ObservedAt.Sub(previous.ObservedAt).Seconds()-(*process.UptimeSeconds-*process.PreviousUptimeSeconds)) > api.ObsRuntimeProcessResetTolerance.Seconds() {
		return "unavailable", "process_window_uncertain"
	}
	if current.Value < previous.Value {
		return "unavailable", "counter_reset"
	}
	if current.Value > previous.Value {
		return "increased", ""
	}
	return "unchanged", ""
}
func runtimeAppTaskOperationSourceReason(state, reason string) bool {
	switch state {
	case "missing":
		return slices.Contains([]string{"expected_target_missing", "metric_missing"}, reason)
	case "stale":
		return slices.Contains([]string{"scrape_stale", "metric_stale"}, reason)
	case "conflicting":
		return slices.Contains([]string{"conflicting_targets", "conflicting_series", "conflicting_sample_times"}, reason)
	case "unavailable":
		return slices.Contains([]string{"source_unavailable", "invalid_evidence", "invalid_sample_time", "scrape_before_boundary", "scrape_down", "metric_before_boundary", "invalid_value"}, reason)
	}
	return false
}
func runtimeAppTaskOperationUnknown(c RuntimeAppTaskOperationCounts) int {
	return c.Missing + c.Stale + c.Conflicting + c.Unavailable
}
func runtimeAppTaskOperationTotal(c RuntimeAppTaskOperationCounts) int {
	return c.Increased + c.Unchanged + c.Baseline + runtimeAppTaskOperationUnknown(c)
}
func runtimeAppTaskOperationsState(c RuntimeAppTaskOperationCounts, incomplete bool, comparison bool) (string, string) {
	if c.Increased > 0 {
		return "increased", "operation_increases_observed"
	}
	if !comparison && c.Baseline > 0 {
		return "baseline", "comparison_not_requested"
	}
	if incomplete {
		return "incomplete", "incomplete_operation_evidence"
	}
	if c.Unchanged > 0 {
		return "unchanged", ""
	}
	return "unknown", "operation_evidence_unavailable"
}
func accountRuntimeAppTaskOperation(h *RuntimeAppTaskOperationsSummary, item RuntimeAppTaskOperationItem) {
	for i, s := range item.Signals {
		c := &h.Counts
		o := &h.Operations[i/len(runtimeAppTaskOperationOutcomes())]
		if s.Current != nil {
			c.Observed++
		}
		if item.Unexpected {
			c.Unexpected++
		}
		switch s.State {
		case "increased":
			c.Increased++
			o.Increased++
			switch s.Outcome {
			case "error":
				o.ErrorIncreases++
			case "canceled":
				o.Cancellations++
			case "lease_lost":
				o.LeaseLosses++
			case "capacity":
				o.CapacityRefusals++
			}
		case "unchanged":
			c.Unchanged++
			o.Unchanged++
		case "baseline":
			c.Baseline++
			o.Baseline++
		default:
			o.Unknown++
			switch s.State {
			case "missing":
				c.Missing++
			case "stale":
				c.Stale++
			case "conflicting":
				c.Conflicting++
			default:
				c.Unavailable++
			}
		}
	}
}
