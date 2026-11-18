package main

import (
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

type obsDeploymentDelayedTaskDispatchProgress struct {
	FirstIncreaseAt            *time.Time                                        `json:"first_increase_at,omitempty"`
	LastIncreaseAt             *time.Time                                        `json:"last_increase_at,omitempty"`
	ObservedIncreases          bool                                              `json:"observed_increases"`
	ObservedOutcomes           []operatorobs.RuntimeDelayedTaskDispatchDimension `json:"observed_outcomes"`
	ObservedBaseline           bool                                              `json:"observed_baseline"`
	ObservedIncompleteEvidence bool                                              `json:"observed_incomplete_evidence"`
}

func (r *obsDeploymentBuildRecorder) observeDelayedTaskDispatch(current *obsDeploymentRuntimeBuilds, at time.Time) {
	if r.skipRuntimeDiagnostic(current, "delayed_task_dispatch") {
		return
	}

	selected := current.pipelineSelected || current.DelayedTaskDispatch != nil || r.progress.DelayedTaskDispatch != nil
	if current.Evidence != nil {
		selected = selected || current.Evidence.ExpectedRole == "single-box-v2" || current.Evidence.ExpectedRole == "control-plane-v2"
	}
	if !selected {
		return
	}
	p := r.progress.DelayedTaskDispatch
	if p == nil {
		p = &obsDeploymentDelayedTaskDispatchProgress{ObservedOutcomes: []operatorobs.RuntimeDelayedTaskDispatchDimension{}, ObservedIncompleteEvidence: r.diagnosticMissingBeforeFirst("delayed_task_dispatch")}
		r.progress.DelayedTaskDispatch = p
	}
	h := current.DelayedTaskDispatch
	p.ObservedIncompleteEvidence = p.ObservedIncompleteEvidence || h == nil || h.Incomplete || h.State == "unknown"
	if h == nil {
		return
	}
	p.ObservedBaseline = p.ObservedBaseline || h.Counts.Baseline > 0
	if h.Counts.Increased > 0 {
		p.ObservedIncreases = true
		if p.FirstIncreaseAt == nil {
			p.FirstIncreaseAt = obsDeploymentEvidencePtr(&at)
		}
		p.LastIncreaseAt = obsDeploymentEvidencePtr(&at)
	}
	// Rebuild in the fixed outcome order, preserving previously observed
	// categories without summing overlapping poll differences or task counts.
	outcomes := []operatorobs.RuntimeDelayedTaskDispatchDimension{}
	for _, o := range h.Outcomes {
		d := o.RuntimeDelayedTaskDispatchDimension
		if o.Increased > 0 || slices.Contains(p.ObservedOutcomes, d) {
			outcomes = append(outcomes, d)
		}
	}
	p.ObservedOutcomes = outcomes
}

func cloneObsDeploymentDelayedTaskDispatchProgress(in *obsDeploymentDelayedTaskDispatchProgress) *obsDeploymentDelayedTaskDispatchProgress {
	if in == nil {
		return nil
	}
	out := *in
	out.FirstIncreaseAt, out.LastIncreaseAt = obsDeploymentEvidencePtr(in.FirstIncreaseAt), obsDeploymentEvidencePtr(in.LastIncreaseAt)
	out.ObservedOutcomes = append([]operatorobs.RuntimeDelayedTaskDispatchDimension{}, in.ObservedOutcomes...)
	return &out
}

func writeObsDeploymentDelayedTaskDispatchProgress(w io.Writer, p *obsDeploymentDelayedTaskDispatchProgress) {
	if p == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "delayed_task_dispatch_progress observed_increases=%t observed_baseline=%t observed_incomplete_evidence=%t first_increase_at=%s last_increase_at=%s\n", p.ObservedIncreases, p.ObservedBaseline, p.ObservedIncompleteEvidence, obsDeploymentTimelineTime(p.FirstIncreaseAt), obsDeploymentTimelineTime(p.LastIncreaseAt))
	for _, d := range p.ObservedOutcomes {
		_, _ = fmt.Fprintf(w, " observed_delayed_task_dispatch outcome=%s\n", d.Outcome)
	}
}

func writeObsDeploymentDelayedTaskDispatch(w io.Writer, h *operatorobs.RuntimeDelayedTaskDispatch) {
	if h == nil {
		return
	}
	c := h.Counts
	_, _ = fmt.Fprintf(w, "delayed_task_dispatch advisory=true scope=%s state=%s reason=%s incomplete=%t comparison_at=%s expected=%d observed=%d increased=%d unchanged=%d baseline=%d missing=%d stale=%d conflicting=%d unavailable=%d unexpected=%d omitted_items=%d\n", h.Scope, h.State, h.Reason, h.Incomplete, obsDeploymentTimelineTime(h.ComparisonAt), c.Expected, c.Observed, c.Increased, c.Unchanged, c.Baseline, c.Missing, c.Stale, c.Conflicting, c.Unavailable, c.Unexpected, h.OmittedItems)
	for _, o := range h.Outcomes {
		_, _ = fmt.Fprintf(w, " delayed_task_dispatch outcome=%s increased=%d unchanged=%d baseline=%d unknown=%d\n", o.Outcome, o.Increased, o.Unchanged, o.Baseline, o.Unknown)
	}
	for _, item := range h.Items {
		for _, s := range item.Signals {
			_, _ = fmt.Fprintf(w, "  delayed_task_dispatch_target job=%s node=%s target_id=%s outcome=%s state=%s reason=%s", item.Job, item.Node, item.TargetID, s.Outcome, s.State, s.Reason)
			if s.Current != nil {
				_, _ = fmt.Fprintf(w, " current=%d observed_at=%s", s.Current.Value, s.Current.ObservedAt.Format(time.RFC3339Nano))
			}
			if s.Previous != nil {
				_, _ = fmt.Fprintf(w, " previous=%d previous_observed_at=%s", s.Previous.Value, s.Previous.ObservedAt.Format(time.RFC3339Nano))
			}
			if s.Delta != nil {
				_, _ = fmt.Fprintf(w, " observed_delta=%d", *s.Delta)
			}
			_, _ = fmt.Fprintln(w)
		}
	}
}
