package main

import (
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

type obsDeploymentHandoffProgress struct {
	FirstWaitingAt               *time.Time `json:"first_waiting_at,omitempty"`
	LastWaitingAt                *time.Time `json:"last_waiting_at,omitempty"`
	FirstEmptyAt                 *time.Time `json:"first_empty_at,omitempty"`
	LastEmptyAt                  *time.Time `json:"last_empty_at,omitempty"`
	WaitingTransitions           int        `json:"waiting_transitions"`
	EmptyThroughoutObservedPolls bool       `json:"empty_throughout_observed_polls"`
	ObservedIncompleteEvidence   bool       `json:"observed_incomplete_evidence"`
}

func (r *obsDeploymentBuildRecorder) observeHandoff(current *obsDeploymentRuntimeBuilds, at time.Time) {
	if r.skipRuntimeDiagnostic(current, "handoff_health") {
		r.lastHandoffEmpty = false
		if p := r.progress.HandoffHealth; p != nil {
			p.EmptyThroughoutObservedPolls = false
		}
		return
	}

	selected := current.pipelineSelected || current.HandoffHealth != nil || r.progress.HandoffHealth != nil
	if current.Evidence != nil {
		selected = selected || current.Evidence.ExpectedRole == "single-box-v2" || current.Evidence.ExpectedRole == "control-plane-v2"
	}
	if !selected {
		return
	}
	p := r.progress.HandoffHealth
	if p == nil {
		p = &obsDeploymentHandoffProgress{ObservedIncompleteEvidence: r.diagnosticMissingBeforeFirst("handoff_health")}
		r.progress.HandoffHealth = p
	}
	h := current.HandoffHealth
	empty := h != nil && h.State == "empty"
	p.EmptyThroughoutObservedPolls = empty && (r.progress.Observations == 1 || p.EmptyThroughoutObservedPolls)
	p.ObservedIncompleteEvidence = p.ObservedIncompleteEvidence || h == nil || h.Incomplete || h.State == "unknown"
	if empty {
		if p.FirstEmptyAt == nil {
			p.FirstEmptyAt = obsDeploymentEvidencePtr(&at)
		}
		p.LastEmptyAt = obsDeploymentEvidencePtr(&at)
		r.lastHandoffEmpty = true
	}
	if h != nil && h.Counts.WaitingScopes > 0 {
		if p.FirstWaitingAt == nil {
			p.FirstWaitingAt = obsDeploymentEvidencePtr(&at)
		}
		p.LastWaitingAt = obsDeploymentEvidencePtr(&at)
		if r.lastHandoffEmpty {
			p.WaitingTransitions++
		}
		r.lastHandoffEmpty = false
	}
}

func cloneObsDeploymentHandoffProgress(in *obsDeploymentHandoffProgress) *obsDeploymentHandoffProgress {
	if in == nil {
		return nil
	}
	out := *in
	out.FirstWaitingAt = obsDeploymentEvidencePtr(in.FirstWaitingAt)
	out.LastWaitingAt = obsDeploymentEvidencePtr(in.LastWaitingAt)
	out.FirstEmptyAt = obsDeploymentEvidencePtr(in.FirstEmptyAt)
	out.LastEmptyAt = obsDeploymentEvidencePtr(in.LastEmptyAt)
	return &out
}

func writeObsDeploymentHandoffProgress(w io.Writer, p *obsDeploymentHandoffProgress) {
	if p == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "handoff_progress waiting_transitions=%d empty_throughout_observed_polls=%t observed_incomplete_evidence=%t first_waiting_at=%s last_waiting_at=%s first_empty_at=%s last_empty_at=%s\n", p.WaitingTransitions, p.EmptyThroughoutObservedPolls, p.ObservedIncompleteEvidence, obsDeploymentTimelineTime(p.FirstWaitingAt), obsDeploymentTimelineTime(p.LastWaitingAt), obsDeploymentTimelineTime(p.FirstEmptyAt), obsDeploymentTimelineTime(p.LastEmptyAt))
}

func writeObsDeploymentHandoffHealth(w io.Writer, h *operatorobs.RuntimeHandoffHealth) {
	if h == nil {
		return
	}
	c := h.Counts
	_, _ = fmt.Fprintf(w, "handoff_health advisory=true state=%s reason=%s incomplete=%t expected=%d observed=%d complete=%d missing=%d stale=%d conflicting=%d unavailable=%d unexpected=%d waiting_scopes=%d empty_scopes=%d unsuccessful_collections=%d working_recovery_scopes=%d waiting_recovery_scopes=%d stopping_recovery_scopes=%d not_started_recovery_scopes=%d omitted_items=%d\n", h.State, h.Reason, h.Incomplete, c.Expected, c.Observed, c.Complete, c.Missing, c.Stale, c.Conflicting, c.Unavailable, c.Unexpected, c.WaitingScopes, c.EmptyScopes, c.UnsuccessfulCollections, c.WorkingRecoveryScopes, c.WaitingRecoveryScopes, c.StoppingRecoveryScopes, c.NotStartedRecoveryScopes, h.OmittedItems)
	for _, item := range h.Items {
		_, _ = fmt.Fprintf(w, "  handoff node=%s target_id=%s state=%s collection_state=%s collection_reason=%s collection_observed_at=%s", item.Node, item.TargetID, item.State, item.Collection.State, item.Collection.Reason, obsDeploymentTimelineTime(item.Collection.ObservedAt))
		if v := item.Collection.Values; v != nil {
			_, _ = fmt.Fprintf(w, " collection_success=%t collection_last_success_at=%s", v.Success, obsDeploymentTimelineTime(v.LastSuccessAt))
		}
		_, _ = fmt.Fprintf(w, " backlog_state=%s backlog_reason=%s backlog_observed_at=%s", item.Backlog.State, item.Backlog.Reason, obsDeploymentTimelineTime(item.Backlog.ObservedAt))
		if v := item.Backlog.Values; v != nil {
			_, _ = fmt.Fprintf(w, " pending=%d oldest_finished_at=%s oldest_wait_seconds=%g", v.Pending, obsDeploymentTimelineTime(v.OldestFinishedAt), v.OldestWaitSeconds)
		}
		_, _ = fmt.Fprintf(w, " recovery_state=%s recovery_reason=%s recovery_observed_at=%s", item.Recovery.State, item.Recovery.Reason, obsDeploymentTimelineTime(item.Recovery.ObservedAt))
		if v := item.Recovery.Values; v != nil {
			_, _ = fmt.Fprintf(w, " recovery_enabled=%t recovery_inflight=%t recovery_last_attempt_at=%s recovery_last_completed_at=%s", v.Enabled, v.Inflight, obsDeploymentTimelineTime(v.LastAttemptAt), obsDeploymentTimelineTime(v.LastCompletedAt))
		}
		_, _ = fmt.Fprintln(w)
	}
}
