package main

import (
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

type obsDeploymentAppTaskResultsProgress struct {
	FirstIncreaseAt               *time.Time `json:"first_increase_at,omitempty"`
	LastIncreaseAt                *time.Time `json:"last_increase_at,omitempty"`
	ObservedIncreases             bool       `json:"observed_increases"`
	ObservedSucceeded             bool       `json:"observed_succeeded"`
	ObservedFailed                bool       `json:"observed_failed"`
	ObservedTimedOut              bool       `json:"observed_timed_out"`
	ObservedCancelled             bool       `json:"observed_cancelled"`
	ObservedQueuedRetries         bool       `json:"observed_queued_retries"`
	ObservedRequeuedRestores      bool       `json:"observed_requeued_restores"`
	ObservedFailedRuns            bool       `json:"observed_failed_runs"`
	ObservedRecoveryCancellations bool       `json:"observed_recovery_cancellations"`
	ObservedBaseline              bool       `json:"observed_baseline"`
	ObservedIncompleteEvidence    bool       `json:"observed_incomplete_evidence"`
}

func (r *obsDeploymentBuildRecorder) observeAppTaskResults(current *obsDeploymentRuntimeBuilds, at time.Time) {
	if r.skipRuntimeDiagnostic(current, "app_task_results") {
		return
	}

	selected := current.pipelineSelected || current.AppTaskResults != nil || r.progress.AppTaskResults != nil
	if current.Evidence != nil {
		selected = selected || current.Evidence.ExpectedRole == "single-box-v2" || current.Evidence.ExpectedRole == "control-plane-v2"
	}
	if !selected {
		return
	}
	p := r.progress.AppTaskResults
	if p == nil {
		p = &obsDeploymentAppTaskResultsProgress{ObservedIncompleteEvidence: r.diagnosticMissingBeforeFirst("app_task_results")}
		r.progress.AppTaskResults = p
	}
	h := current.AppTaskResults
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
	for _, o := range h.Outcomes {
		if o.Increased == 0 {
			continue
		}
		if o.Source == "completion" {
			switch o.Outcome {
			case "succeeded":
				p.ObservedSucceeded = true
			case "failed":
				p.ObservedFailed = true
			case "timed_out":
				p.ObservedTimedOut = true
			case "cancelled":
				p.ObservedCancelled = true
			case "queued":
				p.ObservedQueuedRetries = true
			}
		} else {
			switch o.Outcome {
			case "requeued_restores":
				p.ObservedRequeuedRestores = true
			case "failed_runs":
				p.ObservedFailedRuns = true
			case "cancelled":
				p.ObservedRecoveryCancellations = true
			}
		}
	}
}

func cloneObsDeploymentAppTaskResultsProgress(in *obsDeploymentAppTaskResultsProgress) *obsDeploymentAppTaskResultsProgress {
	if in == nil {
		return nil
	}
	out := *in
	out.FirstIncreaseAt, out.LastIncreaseAt = obsDeploymentEvidencePtr(in.FirstIncreaseAt), obsDeploymentEvidencePtr(in.LastIncreaseAt)
	return &out
}

func writeObsDeploymentAppTaskResultsProgress(w io.Writer, p *obsDeploymentAppTaskResultsProgress) {
	if p == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "app_task_results_progress observed_increases=%t observed_succeeded=%t observed_failed=%t observed_timed_out=%t observed_cancelled=%t observed_queued_retries=%t observed_requeued_restores=%t observed_failed_runs=%t observed_recovery_cancellations=%t observed_baseline=%t observed_incomplete_evidence=%t first_increase_at=%s last_increase_at=%s\n", p.ObservedIncreases, p.ObservedSucceeded, p.ObservedFailed, p.ObservedTimedOut, p.ObservedCancelled, p.ObservedQueuedRetries, p.ObservedRequeuedRestores, p.ObservedFailedRuns, p.ObservedRecoveryCancellations, p.ObservedBaseline, p.ObservedIncompleteEvidence, obsDeploymentTimelineTime(p.FirstIncreaseAt), obsDeploymentTimelineTime(p.LastIncreaseAt))
}

func writeObsDeploymentAppTaskResults(w io.Writer, h *operatorobs.RuntimeAppTaskResults) {
	if h == nil {
		return
	}
	c := h.Counts
	_, _ = fmt.Fprintf(w, "app_task_results advisory=true scope=%s state=%s reason=%s incomplete=%t comparison_at=%s expected=%d observed=%d increased=%d unchanged=%d baseline=%d missing=%d stale=%d conflicting=%d unavailable=%d unexpected=%d omitted_items=%d\n", h.Scope, h.State, h.Reason, h.Incomplete, obsDeploymentTimelineTime(h.ComparisonAt), c.Expected, c.Observed, c.Increased, c.Unchanged, c.Baseline, c.Missing, c.Stale, c.Conflicting, c.Unavailable, c.Unexpected, h.OmittedItems)
	for _, o := range h.Outcomes {
		_, _ = fmt.Fprintf(w, " result source=%s kind=%s outcome=%s increased=%d unchanged=%d baseline=%d unknown=%d\n", o.Source, o.Kind, o.Outcome, o.Increased, o.Unchanged, o.Baseline, o.Unknown)
	}
	for _, item := range h.Items {
		for _, s := range item.Signals {
			_, _ = fmt.Fprintf(w, "  result_target job=%s node=%s target_id=%s source=%s kind=%s outcome=%s state=%s reason=%s", item.Job, item.Node, item.TargetID, s.Source, s.Kind, s.Outcome, s.State, s.Reason)
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
