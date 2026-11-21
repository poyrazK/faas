package main

import (
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

type obsDeploymentBuilderProgress struct {
	FirstOverdueAt                         *time.Time `json:"first_overdue_at,omitempty"`
	LastOverdueAt                          *time.Time `json:"last_overdue_at,omitempty"`
	FirstWithinAllowanceAt                 *time.Time `json:"first_within_allowance_at,omitempty"`
	LastWithinAllowanceAt                  *time.Time `json:"last_within_allowance_at,omitempty"`
	LastRecoveryAt                         *time.Time `json:"last_recovery_at,omitempty"`
	LastRegressionAt                       *time.Time `json:"last_regression_at,omitempty"`
	OverdueRegressions                     int        `json:"overdue_regressions"`
	WithinAllowanceThroughoutObservedPolls bool       `json:"within_allowance_throughout_observed_polls"`
	ObservedIncompleteEvidence             bool       `json:"observed_incomplete_evidence"`
}

func (r *obsDeploymentBuildRecorder) observeBuilder(current *obsDeploymentRuntimeBuilds, at time.Time) {
	if r.skipRuntimeDiagnostic(current, "builder_health") {
		r.pendingBuilderOverdue = false
		r.lastBuilderWithin = false
		if p := r.progress.BuilderHealth; p != nil {
			p.WithinAllowanceThroughoutObservedPolls = false
		}
		return
	}

	selected := current.pipelineSelected || current.BuilderHealth != nil || r.progress.BuilderHealth != nil
	if current.Evidence != nil {
		selected = selected || current.Evidence.ExpectedRole == "single-box-v2" || current.Evidence.ExpectedRole == "control-plane-v2"
	}
	if !selected {
		return
	}
	p := r.progress.BuilderHealth
	if p == nil {
		p = &obsDeploymentBuilderProgress{ObservedIncompleteEvidence: r.diagnosticMissingBeforeFirst("builder_health")}
		r.progress.BuilderHealth = p
	}
	h := current.BuilderHealth
	within := h != nil && !h.Incomplete && h.Counts.Complete > 0 && h.Counts.OverdueWorkers == 0 && h.Counts.DrainingWorkers == 0
	p.WithinAllowanceThroughoutObservedPolls = within && (r.progress.Observations == 1 || p.WithinAllowanceThroughoutObservedPolls)
	p.ObservedIncompleteEvidence = p.ObservedIncompleteEvidence || h == nil || h.Incomplete || h.State == "unknown"
	if within {
		if p.FirstWithinAllowanceAt == nil {
			p.FirstWithinAllowanceAt = obsDeploymentEvidencePtr(&at)
		}
		p.LastWithinAllowanceAt = obsDeploymentEvidencePtr(&at)
		if r.pendingBuilderOverdue {
			p.LastRecoveryAt = obsDeploymentEvidencePtr(&at)
			r.pendingBuilderOverdue = false
		}
		r.lastBuilderWithin = true
	}
	if h != nil && h.Counts.OverdueWorkers > 0 {
		if p.FirstOverdueAt == nil {
			p.FirstOverdueAt = obsDeploymentEvidencePtr(&at)
		}
		p.LastOverdueAt = obsDeploymentEvidencePtr(&at)
		if r.lastBuilderWithin {
			p.OverdueRegressions++
			p.LastRegressionAt = obsDeploymentEvidencePtr(&at)
		}
		r.pendingBuilderOverdue, r.lastBuilderWithin = true, false
	}
}

func cloneObsDeploymentBuilderProgress(in *obsDeploymentBuilderProgress) *obsDeploymentBuilderProgress {
	if in == nil {
		return nil
	}
	out := *in
	out.FirstOverdueAt = obsDeploymentEvidencePtr(in.FirstOverdueAt)
	out.LastOverdueAt = obsDeploymentEvidencePtr(in.LastOverdueAt)
	out.FirstWithinAllowanceAt = obsDeploymentEvidencePtr(in.FirstWithinAllowanceAt)
	out.LastWithinAllowanceAt = obsDeploymentEvidencePtr(in.LastWithinAllowanceAt)
	out.LastRecoveryAt = obsDeploymentEvidencePtr(in.LastRecoveryAt)
	out.LastRegressionAt = obsDeploymentEvidencePtr(in.LastRegressionAt)
	return &out
}

func writeObsDeploymentBuilderProgress(w io.Writer, p *obsDeploymentBuilderProgress) {
	if p == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "builder_progress overdue_regressions=%d within_allowance_throughout_observed_polls=%t observed_incomplete_evidence=%t first_overdue_at=%s last_overdue_at=%s first_within_allowance_at=%s last_within_allowance_at=%s last_recovery_at=%s last_regression_at=%s\n", p.OverdueRegressions, p.WithinAllowanceThroughoutObservedPolls, p.ObservedIncompleteEvidence, obsDeploymentTimelineTime(p.FirstOverdueAt), obsDeploymentTimelineTime(p.LastOverdueAt), obsDeploymentTimelineTime(p.FirstWithinAllowanceAt), obsDeploymentTimelineTime(p.LastWithinAllowanceAt), obsDeploymentTimelineTime(p.LastRecoveryAt), obsDeploymentTimelineTime(p.LastRegressionAt))
}

func writeObsDeploymentBuilderHealth(w io.Writer, h *operatorobs.RuntimeBuilderHealth) {
	if h == nil {
		return
	}
	c := h.Counts
	_, _ = fmt.Fprintf(w, "builder_health advisory=true state=%s reason=%s incomplete=%t expected=%d observed=%d complete=%d missing=%d stale=%d conflicting=%d unavailable=%d unexpected=%d overdue_workers=%d waiting_workers=%d working_workers=%d draining_workers=%d waiting_queues=%d saturated_slots=%d omitted_items=%d\n", h.State, h.Reason, h.Incomplete, c.Expected, c.Observed, c.Complete, c.Missing, c.Stale, c.Conflicting, c.Unavailable, c.Unexpected, c.OverdueWorkers, c.WaitingWorkers, c.WorkingWorkers, c.DrainingWorkers, c.WaitingQueues, c.SaturatedSlots, h.OmittedItems)
	for _, item := range h.Items {
		_, _ = fmt.Fprintf(w, "  builder node=%s target_id=%s state=%s worker_state=%s worker_reason=%s worker_observed_at=%s", item.Node, item.TargetID, item.State, item.Worker.State, item.Worker.Reason, obsDeploymentTimelineTime(item.Worker.ObservedAt))
		if v := item.Worker.Values; v != nil {
			_, _ = fmt.Fprintf(w, " enabled=%t inflight=%t progress_age_seconds=%g progress_allowance_seconds=%g started_at=%s last_attempt_at=%s last_completed_at=%s", v.Enabled, v.Inflight, v.ProgressAgeSeconds, v.ProgressAllowanceSeconds, v.StartedAt.Format(time.RFC3339Nano), obsDeploymentTimelineTime(v.LastAttemptAt), obsDeploymentTimelineTime(v.LastCompletedAt))
		}
		_, _ = fmt.Fprintf(w, " slots_state=%s slots_reason=%s", item.Slots.State, item.Slots.Reason)
		if v := item.Slots.Values; v != nil {
			_, _ = fmt.Fprintf(w, " slots_used=%d slots_limit=%d", v.Used, v.Limit)
		}
		_, _ = fmt.Fprintln(w)
		for _, q := range item.Queues {
			_, _ = fmt.Fprintf(w, "    queue=%s state=%s reason=%s observed_at=%s", q.Queue, q.State, q.Reason, obsDeploymentTimelineTime(q.ObservedAt))
			if v := q.Values; v != nil {
				_, _ = fmt.Fprintf(w, " enabled=%t waiting=%d active=%d capacity=%d workers=%d oldest_wait_seconds=%g", v.Enabled, v.Waiting, v.Active, v.Capacity, v.Workers, v.OldestWaitSeconds)
			}
			_, _ = fmt.Fprintln(w)
		}
	}
}
