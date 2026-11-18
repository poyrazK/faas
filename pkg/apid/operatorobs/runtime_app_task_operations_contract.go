package operatorobs

import (
	"github.com/onebox-faas/faas/pkg/api"
	"slices"
	"strings"
	"time"
)

func runtimeAppTaskOperationCountValues(c RuntimeAppTaskOperationCounts) []int {
	return []int{c.Expected, c.Observed, c.Increased, c.Unchanged, c.Baseline, c.Missing, c.Stale, c.Conflicting, c.Unavailable, c.Unexpected}
}
func runtimeAppTaskOperationSummaryValues(o RuntimeAppTaskOperationSummary) []int {
	return []int{o.Increased, o.Unchanged, o.Baseline, o.Unknown, o.ErrorIncreases, o.Cancellations, o.LeaseLosses, o.CapacityRefusals}
}
func RuntimeAppTaskOperationsSummaryValid(base api.ObsRuntimeBuilds, h RuntimeAppTaskOperationsSummary) bool {
	jobs := runtimeAppTaskJobs(base.ExpectedRole)
	if len(jobs) == 0 || h.Scope != RuntimeAppTaskOperationsScope || len(h.Operations) != len(runtimeAppTaskOperationNames()) {
		return false
	}
	expected := 0
	inventoryUnknown := false
	for _, name := range jobs {
		i := slices.IndexFunc(base.Jobs, func(j api.ObsRuntimeBuildJob) bool { return j.Job == name && j.Daemon == "schedd" })
		if i < 0 {
			return false
		}
		expected += base.Jobs[i].Expected * runtimeAppTaskOperationPairs()
		inventoryUnknown = inventoryUnknown || base.Jobs[i].State != "available"
	}
	maxSignals := len(jobs) * (api.ObsRuntimeBuildSamplesMax + api.ObsRuntimeBuildNodesMax) * runtimeAppTaskOperationPairs()
	c := h.Counts
	total := runtimeAppTaskOperationTotal(c)
	for _, n := range runtimeAppTaskOperationCountValues(c) {
		if n < 0 || n > maxSignals {
			return false
		}
	}
	if c.Expected != expected || total < c.Expected || total > maxSignals || total%runtimeAppTaskOperationPairs() != 0 || c.Observed > min(total, base.Counts.Observed*runtimeAppTaskOperationPairs()) || c.Increased+c.Unchanged+c.Baseline > c.Observed || c.Unexpected > total || c.Unexpected%runtimeAppTaskOperationPairs() != 0 {
		return false
	}
	sums := [4]int{}
	for i, o := range h.Operations {
		if o.Operation != runtimeAppTaskOperationNames()[i] {
			return false
		}
		for _, n := range runtimeAppTaskOperationSummaryValues(o) {
			if n < 0 || n > maxSignals {
				return false
			}
		}
		if o.Increased+o.Unchanged+o.Baseline+o.Unknown != total/len(h.Operations) || o.ErrorIncreases+o.Cancellations+o.LeaseLosses+o.CapacityRefusals > o.Increased || max(o.ErrorIncreases, o.Cancellations, o.LeaseLosses, o.CapacityRefusals) > total/runtimeAppTaskOperationPairs() {
			return false
		}
		sums[0] += o.Increased
		sums[1] += o.Unchanged
		sums[2] += o.Baseline
		sums[3] += o.Unknown
	}
	if sums != [4]int{c.Increased, c.Unchanged, c.Baseline, runtimeAppTaskOperationUnknown(c)} {
		return false
	}
	incomplete := inventoryUnknown || runtimeAppTaskOperationUnknown(c) > 0 || c.Unexpected > 0
	state, reason := runtimeAppTaskOperationsState(c, incomplete, c.Baseline == 0)
	return h.Incomplete == incomplete && h.State == state && h.Reason == reason
}
func RuntimeAppTaskOperationsValid(base api.ObsRuntimeBuilds, h *RuntimeAppTaskOperations) bool {
	if h == nil || base.Process == nil || !RuntimeAppTaskOperationsSummaryValid(base, h.RuntimeAppTaskOperationsSummary) || !sameRuntimeOperationTime(h.ComparisonAt, base.Process.ComparisonAt) || len(h.Items) > api.ObsRuntimeBuildPreviewMax || h.OmittedItems < 0 || (len(h.Items)+h.OmittedItems)*runtimeAppTaskOperationPairs() != runtimeAppTaskOperationTotal(h.Counts) {
		return false
	}
	if h.ComparisonAt == nil && h.Counts.Increased+h.Counts.Unchanged > 0 || h.ComparisonAt != nil && h.Counts.Baseline > 0 {
		return false
	}
	part := newRuntimeAppTaskOperationsSummary()
	seen := map[string]bool{}
	for _, item := range h.Items {
		key := item.TargetID
		if key == "" {
			key = item.Job + "/missing/" + item.Node
		}
		if seen[key] || !runtimeAppTaskOperationItemValid(base, item) {
			return false
		}
		seen[key] = true
		accountRuntimeAppTaskOperation(&part, item)
	}
	full, preview := runtimeAppTaskOperationCountValues(h.Counts), runtimeAppTaskOperationCountValues(part.Counts)
	for i := 1; i < len(full); i++ {
		if preview[i] > full[i] || h.OmittedItems == 0 && preview[i] != full[i] {
			return false
		}
	}
	for i, o := range part.Operations {
		f, p := runtimeAppTaskOperationSummaryValues(h.Operations[i]), runtimeAppTaskOperationSummaryValues(o)
		for j := range f {
			if p[j] > f[j] || h.OmittedItems == 0 && p[j] != f[j] {
				return false
			}
		}
	}
	return true
}
func sameRuntimeOperationTime(a, b *time.Time) bool {
	return a == nil && b == nil || a != nil && b != nil && a.Equal(*b)
}
func runtimeAppTaskOperationItemValid(base api.ObsRuntimeBuilds, item RuntimeAppTaskOperationItem) bool {
	if !slices.Contains(runtimeAppTaskJobs(base.ExpectedRole), item.Job) || item.Node != "" && !ServingProbeNodeValid(item.Node) || item.TargetID != "" && !runtimeTargetIDValid(item.TargetID) || item.Unexpected && item.TargetID == "" || len(item.Signals) != runtimeAppTaskOperationPairs() {
		return false
	}
	if at := item.ScrapeObservedAt; at != nil && (at.IsZero() || at.After(base.GeneratedAt.Add(api.ObsHealthClockSkewTolerance))) {
		return false
	}
	bridge := api.ObsRuntimeBuildItem{Job: item.Job, Node: item.Node, TargetID: item.TargetID, Unexpected: item.Unexpected, ScrapeObservedAt: item.ScrapeObservedAt, Process: item.Process}
	if !runtimeProcessItemValid(bridge, base) {
		return false
	}
	for i, s := range item.Signals {
		if s.Operation != runtimeAppTaskOperationNames()[i/len(runtimeAppTaskOperationOutcomes())] || s.Outcome != runtimeAppTaskOperationOutcomes()[i%len(runtimeAppTaskOperationOutcomes())] {
			return false
		}
		if !runtimeAppTaskCounterSignalValid(base, item, s) {
			return false
		}
	}
	return true
}

// Shared comparison rules for process-local scheduler counters. Dimensions are validated by each caller.
func runtimeAppTaskCounterSignalValid(base api.ObsRuntimeBuilds, item RuntimeAppTaskOperationItem, s RuntimeAppTaskOperationSignal) bool {
	if s.Current == nil {
		if s.Previous != nil || s.Delta != nil || !runtimeAppTaskOperationSourceReason(s.State, s.Reason) {
			return false
		}
		return true
	}
	if item.TargetID == "" || !runtimeProcessTimeValid(item.ScrapeObservedAt, base.GeneratedAt, base.EvidenceAfter) || !runtimeAppTaskOperationPointValid(s.Current, base.GeneratedAt, base.EvidenceAfter) {
		return false
	}
	if base.Process.ComparisonAt == nil {
		if s.State != "baseline" || s.Reason != "" || s.Previous != nil || s.Delta != nil {
			return false
		}
		return true
	}
	if s.Previous == nil {
		if s.Delta != nil || !strings.HasPrefix(s.Reason, "previous_") || !runtimeAppTaskOperationSourceReason(s.State, strings.TrimPrefix(s.Reason, "previous_")) {
			return false
		}
		return true
	}
	if !runtimeAppTaskOperationPointValid(s.Previous, *base.Process.ComparisonAt, base.EvidenceAfter) {
		return false
	}
	state, reason := runtimeAppTaskOperationComparison(s.Current, s.Previous, item.Process)
	if s.State != state || s.Reason != reason {
		return false
	}
	if state == "increased" || state == "unchanged" {
		if s.Delta == nil || *s.Delta != s.Current.Value-s.Previous.Value {
			return false
		}
	} else if s.Delta != nil {
		return false
	}
	return true
}
