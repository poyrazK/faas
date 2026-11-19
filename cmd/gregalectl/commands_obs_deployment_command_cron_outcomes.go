package main

import (
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

type obsDeploymentCommandCronOutcomesProgress struct {
	FirstIncreaseAt            *time.Time                                       `json:"first_increase_at,omitempty"`
	LastIncreaseAt             *time.Time                                       `json:"last_increase_at,omitempty"`
	ObservedIncreases          bool                                             `json:"observed_increases"`
	ObservedOutcomes           []operatorobs.RuntimeCommandCronOutcomeDimension `json:"observed_outcomes"`
	ObservedBaseline           bool                                             `json:"observed_baseline"`
	ObservedIncompleteEvidence bool                                             `json:"observed_incomplete_evidence"`
}

func (r *obsDeploymentBuildRecorder) observeCommandCronOutcomes(current *obsDeploymentRuntimeBuilds, at time.Time) {
	if r.skipRuntimeDiagnostic(current, "command_cron_outcomes") {
		return
	}

	selected := current.pipelineSelected || current.CommandCronOutcomes != nil || r.progress.CommandCronOutcomes != nil
	if current.Evidence != nil {
		selected = selected || current.Evidence.ExpectedRole == "single-box-v2" || current.Evidence.ExpectedRole == "control-plane-v2"
	}
	if !selected {
		return
	}
	p := r.progress.CommandCronOutcomes
	if p == nil {
		p = &obsDeploymentCommandCronOutcomesProgress{ObservedOutcomes: []operatorobs.RuntimeCommandCronOutcomeDimension{}, ObservedIncompleteEvidence: r.diagnosticMissingBeforeFirst("command_cron_outcomes")}
		r.progress.CommandCronOutcomes = p
	}
	h := current.CommandCronOutcomes
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
	// Rebuild in the fixed source/outcome order, preserving previously observed
	// categories without summing overlapping poll differences or task counts.
	outcomes := []operatorobs.RuntimeCommandCronOutcomeDimension{}
	for _, o := range h.Outcomes {
		d := o.RuntimeCommandCronOutcomeDimension
		if o.Increased > 0 || slices.Contains(p.ObservedOutcomes, d) {
			outcomes = append(outcomes, d)
		}
	}
	p.ObservedOutcomes = outcomes
}

func cloneObsDeploymentCommandCronOutcomesProgress(in *obsDeploymentCommandCronOutcomesProgress) *obsDeploymentCommandCronOutcomesProgress {
	if in == nil {
		return nil
	}
	out := *in
	out.FirstIncreaseAt, out.LastIncreaseAt = obsDeploymentEvidencePtr(in.FirstIncreaseAt), obsDeploymentEvidencePtr(in.LastIncreaseAt)
	out.ObservedOutcomes = append([]operatorobs.RuntimeCommandCronOutcomeDimension{}, in.ObservedOutcomes...)
	return &out
}

func writeObsDeploymentCommandCronOutcomesProgress(w io.Writer, p *obsDeploymentCommandCronOutcomesProgress) {
	if p == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "command_cron_outcomes_progress observed_increases=%t observed_baseline=%t observed_incomplete_evidence=%t first_increase_at=%s last_increase_at=%s\n", p.ObservedIncreases, p.ObservedBaseline, p.ObservedIncompleteEvidence, obsDeploymentTimelineTime(p.FirstIncreaseAt), obsDeploymentTimelineTime(p.LastIncreaseAt))
	for _, d := range p.ObservedOutcomes {
		_, _ = fmt.Fprintf(w, " observed_cron_outcome source=%s outcome=%s\n", d.Source, d.Outcome)
	}
}

func writeObsDeploymentCommandCronOutcomes(w io.Writer, h *operatorobs.RuntimeCommandCronOutcomes) {
	if h == nil {
		return
	}
	c := h.Counts
	_, _ = fmt.Fprintf(w, "command_cron_outcomes advisory=true scope=%s state=%s reason=%s incomplete=%t comparison_at=%s expected=%d observed=%d increased=%d unchanged=%d baseline=%d missing=%d stale=%d conflicting=%d unavailable=%d unexpected=%d omitted_items=%d\n", h.Scope, h.State, h.Reason, h.Incomplete, obsDeploymentTimelineTime(h.ComparisonAt), c.Expected, c.Observed, c.Increased, c.Unchanged, c.Baseline, c.Missing, c.Stale, c.Conflicting, c.Unavailable, c.Unexpected, h.OmittedItems)
	for _, o := range h.Outcomes {
		_, _ = fmt.Fprintf(w, " cron_outcome source=%s outcome=%s increased=%d unchanged=%d baseline=%d unknown=%d\n", o.Source, o.Outcome, o.Increased, o.Unchanged, o.Baseline, o.Unknown)
	}
	for _, item := range h.Items {
		for _, s := range item.Signals {
			_, _ = fmt.Fprintf(w, "  cron_outcome_target job=%s node=%s target_id=%s source=%s outcome=%s state=%s reason=%s", item.Job, item.Node, item.TargetID, s.Source, s.Outcome, s.State, s.Reason)
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
