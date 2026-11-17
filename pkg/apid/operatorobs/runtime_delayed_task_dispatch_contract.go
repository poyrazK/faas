package operatorobs

import (
	"github.com/onebox-faas/faas/pkg/api"
	"slices"
)

func runtimeDelayedTaskDispatchSummaryValues(o RuntimeDelayedTaskDispatchOutcomeSummary) []int {
	return []int{o.Increased, o.Unchanged, o.Baseline, o.Unknown}
}
func RuntimeDelayedTaskDispatchSummaryValid(base api.ObsRuntimeBuilds, h RuntimeDelayedTaskDispatchSummary) bool {
	jobs := runtimeAppTaskJobs(base.ExpectedRole)
	if len(jobs) == 0 || h.Scope != RuntimeAppTaskOperationsScope || len(h.Outcomes) != len(runtimeDelayedTaskDispatchDimensions()) {
		return false
	}
	expected := 0
	inventoryUnknown := false
	for _, name := range jobs {
		i := slices.IndexFunc(base.Jobs, func(j api.ObsRuntimeBuildJob) bool { return j.Job == name && j.Daemon == "schedd" })
		if i < 0 {
			return false
		}
		expected += base.Jobs[i].Expected * len(runtimeDelayedTaskDispatchDimensions())
		inventoryUnknown = inventoryUnknown || base.Jobs[i].State != "available"
	}
	maxSignals := len(jobs) * (api.ObsRuntimeBuildSamplesMax + api.ObsRuntimeBuildNodesMax) * len(runtimeDelayedTaskDispatchDimensions())
	c := h.Counts
	total := runtimeAppTaskOperationTotal(c)
	for _, n := range runtimeAppTaskOperationCountValues(c) {
		if n < 0 || n > maxSignals {
			return false
		}
	}
	if c.Expected != expected || total < c.Expected || total > maxSignals || total%len(runtimeDelayedTaskDispatchDimensions()) != 0 || c.Observed > min(total, base.Counts.Observed*len(runtimeDelayedTaskDispatchDimensions())) || c.Increased+c.Unchanged+c.Baseline > c.Observed || c.Unexpected > total || c.Unexpected%len(runtimeDelayedTaskDispatchDimensions()) != 0 {
		return false
	}
	sums := [4]int{}
	for i, o := range h.Outcomes {
		if o.RuntimeDelayedTaskDispatchDimension != runtimeDelayedTaskDispatchDimensions()[i] {
			return false
		}
		for _, n := range runtimeDelayedTaskDispatchSummaryValues(o) {
			if n < 0 || n > maxSignals {
				return false
			}
		}
		if o.Increased+o.Unchanged+o.Baseline+o.Unknown != total/len(h.Outcomes) {
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
	state, reason := runtimeDelayedTaskDispatchState(c, incomplete)
	return h.Incomplete == incomplete && h.State == state && h.Reason == reason
}
func RuntimeDelayedTaskDispatchValid(base api.ObsRuntimeBuilds, h *RuntimeDelayedTaskDispatch) bool {
	if h == nil || base.Process == nil || !RuntimeDelayedTaskDispatchSummaryValid(base, h.RuntimeDelayedTaskDispatchSummary) || !sameRuntimeOperationTime(h.ComparisonAt, base.Process.ComparisonAt) || len(h.Items) > api.ObsRuntimeBuildPreviewMax || h.OmittedItems < 0 || (len(h.Items)+h.OmittedItems)*len(runtimeDelayedTaskDispatchDimensions()) != runtimeAppTaskOperationTotal(h.Counts) {
		return false
	}
	if h.ComparisonAt == nil && h.Counts.Increased+h.Counts.Unchanged > 0 || h.ComparisonAt != nil && h.Counts.Baseline > 0 {
		return false
	}
	part := newRuntimeDelayedTaskDispatchSummary()
	seen := map[string]bool{}
	for _, item := range h.Items {
		key := item.TargetID
		if key == "" {
			key = item.Job + "/missing/" + item.Node
		}
		if seen[key] || !runtimeDelayedTaskDispatchItemValid(base, item) {
			return false
		}
		seen[key] = true
		accountRuntimeDelayedTaskDispatch(&part, item)
	}
	full, preview := runtimeAppTaskOperationCountValues(h.Counts), runtimeAppTaskOperationCountValues(part.Counts)
	for i := 1; i < len(full); i++ {
		if preview[i] > full[i] || h.OmittedItems == 0 && preview[i] != full[i] {
			return false
		}
	}
	for i, o := range part.Outcomes {
		f, p := runtimeDelayedTaskDispatchSummaryValues(h.Outcomes[i]), runtimeDelayedTaskDispatchSummaryValues(o)
		for j := range f {
			if p[j] > f[j] || h.OmittedItems == 0 && p[j] != f[j] {
				return false
			}
		}
	}
	return true
}
func runtimeDelayedTaskDispatchItemValid(base api.ObsRuntimeBuilds, item RuntimeDelayedTaskDispatchItem) bool {
	if !slices.Contains(runtimeAppTaskJobs(base.ExpectedRole), item.Job) || item.Node != "" && !ServingProbeNodeValid(item.Node) || item.TargetID != "" && !runtimeTargetIDValid(item.TargetID) || item.Unexpected && item.TargetID == "" || len(item.Signals) != len(runtimeDelayedTaskDispatchDimensions()) {
		return false
	}
	if at := item.ScrapeObservedAt; at != nil && (at.IsZero() || at.After(base.GeneratedAt.Add(api.ObsHealthClockSkewTolerance))) {
		return false
	}
	bridge := api.ObsRuntimeBuildItem{Job: item.Job, Node: item.Node, TargetID: item.TargetID, Unexpected: item.Unexpected, ScrapeObservedAt: item.ScrapeObservedAt, Process: item.Process}
	if !runtimeProcessItemValid(bridge, base) {
		return false
	}
	counterItem := RuntimeAppTaskOperationItem{TargetID: item.TargetID, ScrapeObservedAt: item.ScrapeObservedAt, Process: item.Process}
	for i, s := range item.Signals {
		if s.RuntimeDelayedTaskDispatchDimension != runtimeDelayedTaskDispatchDimensions()[i] || !runtimeAppTaskCounterSignalValid(base, counterItem, RuntimeAppTaskOperationSignal{State: s.State, Reason: s.Reason, Current: s.Current, Previous: s.Previous, Delta: s.Delta}) {
			return false
		}
	}
	return true
}
