package main

import (
	"fmt"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"io"
	"time"
)

type obsDeploymentAppTaskOperationsProgress struct {
	FirstIncreaseAt            *time.Time `json:"first_increase_at,omitempty"`
	LastIncreaseAt             *time.Time `json:"last_increase_at,omitempty"`
	ObservedIncreases          bool       `json:"observed_increases"`
	ObservedErrors             bool       `json:"observed_errors"`
	ObservedCancellations      bool       `json:"observed_cancellations"`
	ObservedLeaseLosses        bool       `json:"observed_lease_losses"`
	ObservedCapacityRefusals   bool       `json:"observed_capacity_refusals"`
	ObservedBaseline           bool       `json:"observed_baseline"`
	ObservedIncompleteEvidence bool       `json:"observed_incomplete_evidence"`
}

func (r *obsDeploymentBuildRecorder) observeAppTaskOperations(current *obsDeploymentRuntimeBuilds, at time.Time) {
	if r.skipRuntimeDiagnostic(current, "app_task_operations") {
		return
	}

	selected := current.pipelineSelected || current.AppTaskOperations != nil || r.progress.AppTaskOperations != nil
	if current.Evidence != nil {
		selected = selected || current.Evidence.ExpectedRole == "single-box-v2" || current.Evidence.ExpectedRole == "control-plane-v2"
	}
	if !selected {
		return
	}
	p := r.progress.AppTaskOperations
	if p == nil {
		p = &obsDeploymentAppTaskOperationsProgress{ObservedIncompleteEvidence: r.diagnosticMissingBeforeFirst("app_task_operations")}
		r.progress.AppTaskOperations = p
	}
	h := current.AppTaskOperations
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
	for _, o := range h.Operations {
		p.ObservedErrors = p.ObservedErrors || o.ErrorIncreases > 0
		p.ObservedCancellations = p.ObservedCancellations || o.Cancellations > 0
		p.ObservedLeaseLosses = p.ObservedLeaseLosses || o.LeaseLosses > 0
		p.ObservedCapacityRefusals = p.ObservedCapacityRefusals || o.CapacityRefusals > 0
	}
}
func cloneObsDeploymentAppTaskOperationsProgress(in *obsDeploymentAppTaskOperationsProgress) *obsDeploymentAppTaskOperationsProgress {
	if in == nil {
		return nil
	}
	out := *in
	out.FirstIncreaseAt = obsDeploymentEvidencePtr(in.FirstIncreaseAt)
	out.LastIncreaseAt = obsDeploymentEvidencePtr(in.LastIncreaseAt)
	return &out
}
func writeObsDeploymentAppTaskOperationsProgress(w io.Writer, p *obsDeploymentAppTaskOperationsProgress) {
	if p == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "app_task_operations_progress observed_increases=%t observed_errors=%t observed_cancellations=%t observed_lease_losses=%t observed_capacity_refusals=%t observed_baseline=%t observed_incomplete_evidence=%t first_increase_at=%s last_increase_at=%s\n", p.ObservedIncreases, p.ObservedErrors, p.ObservedCancellations, p.ObservedLeaseLosses, p.ObservedCapacityRefusals, p.ObservedBaseline, p.ObservedIncompleteEvidence, obsDeploymentTimelineTime(p.FirstIncreaseAt), obsDeploymentTimelineTime(p.LastIncreaseAt))
}
func writeObsDeploymentAppTaskOperations(w io.Writer, h *operatorobs.RuntimeAppTaskOperations) {
	if h == nil {
		return
	}
	c := h.Counts
	_, _ = fmt.Fprintf(w, "app_task_operations advisory=true scope=%s state=%s reason=%s incomplete=%t comparison_at=%s expected=%d observed=%d increased=%d unchanged=%d baseline=%d missing=%d stale=%d conflicting=%d unavailable=%d unexpected=%d omitted_items=%d\n", h.Scope, h.State, h.Reason, h.Incomplete, obsDeploymentTimelineTime(h.ComparisonAt), c.Expected, c.Observed, c.Increased, c.Unchanged, c.Baseline, c.Missing, c.Stale, c.Conflicting, c.Unavailable, c.Unexpected, h.OmittedItems)
	for _, o := range h.Operations {
		_, _ = fmt.Fprintf(w, " operation=%s increased=%d unchanged=%d baseline=%d unknown=%d error_increases=%d cancellations=%d lease_losses=%d capacity_refusals=%d\n", o.Operation, o.Increased, o.Unchanged, o.Baseline, o.Unknown, o.ErrorIncreases, o.Cancellations, o.LeaseLosses, o.CapacityRefusals)
	}
	for _, item := range h.Items {
		for _, s := range item.Signals {
			_, _ = fmt.Fprintf(w, "  operation_target job=%s node=%s target_id=%s operation=%s outcome=%s state=%s reason=%s", item.Job, item.Node, item.TargetID, s.Operation, s.Outcome, s.State, s.Reason)
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
