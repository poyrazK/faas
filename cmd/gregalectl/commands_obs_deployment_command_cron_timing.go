package main

import (
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

type obsDeploymentCommandCronTimingProgress struct {
	FirstIncreaseAt            *time.Time                                      `json:"first_increase_at,omitempty"`
	LastIncreaseAt             *time.Time                                      `json:"last_increase_at,omitempty"`
	ObservedIncreases          bool                                            `json:"observed_increases"`
	ObservedSignals            []operatorobs.RuntimeCommandCronTimingDimension `json:"observed_signals"`
	ObservedBaseline           bool                                            `json:"observed_baseline"`
	ObservedIncompleteEvidence bool                                            `json:"observed_incomplete_evidence"`
}

func (r *obsDeploymentBuildRecorder) observeCommandCronTiming(current *obsDeploymentRuntimeBuilds, at time.Time) {
	if r.skipRuntimeDiagnostic(current, "command_cron_timing") {
		return
	}

	selected := current.pipelineSelected || current.CommandCronTiming != nil || r.progress.CommandCronTiming != nil
	if current.Evidence != nil {
		selected = selected || current.Evidence.ExpectedRole == "single-box-v2" || current.Evidence.ExpectedRole == "control-plane-v2"
	}
	if !selected {
		return
	}
	p := r.progress.CommandCronTiming
	if p == nil {
		p = &obsDeploymentCommandCronTimingProgress{ObservedSignals: []operatorobs.RuntimeCommandCronTimingDimension{}, ObservedIncompleteEvidence: r.diagnosticMissingBeforeFirst("command_cron_timing")}
		r.progress.CommandCronTiming = p
	}
	h := current.CommandCronTiming
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
	// Rebuild in the fixed signal order, preserving previously observed
	// categories without summing overlapping poll differences or task counts.
	outcomes := []operatorobs.RuntimeCommandCronTimingDimension{}
	for _, o := range h.Signals {
		d := o.RuntimeCommandCronTimingDimension
		if o.Increased > 0 || slices.Contains(p.ObservedSignals, d) {
			outcomes = append(outcomes, d)
		}
	}
	p.ObservedSignals = outcomes
}

func cloneObsDeploymentCommandCronTimingProgress(in *obsDeploymentCommandCronTimingProgress) *obsDeploymentCommandCronTimingProgress {
	if in == nil {
		return nil
	}
	out := *in
	out.FirstIncreaseAt, out.LastIncreaseAt = obsDeploymentEvidencePtr(in.FirstIncreaseAt), obsDeploymentEvidencePtr(in.LastIncreaseAt)
	out.ObservedSignals = append([]operatorobs.RuntimeCommandCronTimingDimension{}, in.ObservedSignals...)
	return &out
}

func writeObsDeploymentCommandCronTimingProgress(w io.Writer, p *obsDeploymentCommandCronTimingProgress) {
	if p == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "command_cron_timing_progress observed_increases=%t observed_baseline=%t observed_incomplete_evidence=%t first_increase_at=%s last_increase_at=%s\n", p.ObservedIncreases, p.ObservedBaseline, p.ObservedIncompleteEvidence, obsDeploymentTimelineTime(p.FirstIncreaseAt), obsDeploymentTimelineTime(p.LastIncreaseAt))
	for _, d := range p.ObservedSignals {
		_, _ = fmt.Fprintf(w, " observed_cron_timing signal=%s\n", d.Signal)
	}
}

func writeObsDeploymentCommandCronTiming(w io.Writer, h *operatorobs.RuntimeCommandCronTiming) {
	if h == nil {
		return
	}
	c := h.Counts
	_, _ = fmt.Fprintf(w, "command_cron_timing advisory=true scope=%s state=%s reason=%s incomplete=%t comparison_at=%s expected=%d observed=%d increased=%d unchanged=%d baseline=%d missing=%d stale=%d conflicting=%d unavailable=%d unexpected=%d omitted_items=%d\n", h.Scope, h.State, h.Reason, h.Incomplete, obsDeploymentTimelineTime(h.ComparisonAt), c.Expected, c.Observed, c.Increased, c.Unchanged, c.Baseline, c.Missing, c.Stale, c.Conflicting, c.Unavailable, c.Unexpected, h.OmittedItems)
	for _, o := range h.Signals {
		_, _ = fmt.Fprintf(w, " cron_timing signal=%s increased=%d unchanged=%d baseline=%d unknown=%d\n", o.Signal, o.Increased, o.Unchanged, o.Baseline, o.Unknown)
	}
	for _, item := range h.Items {
		for _, s := range item.Signals {
			_, _ = fmt.Fprintf(w, "  cron_timing_target job=%s node=%s target_id=%s signal=%s state=%s reason=%s", item.Job, item.Node, item.TargetID, s.Signal, s.State, s.Reason)
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
