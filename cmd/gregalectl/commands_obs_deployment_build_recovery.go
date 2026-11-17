package main

import (
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

type obsDeploymentBuildRecoveryProgress struct {
	FirstNonemptyAt              *time.Time `json:"first_nonempty_at,omitempty"`
	LastNonemptyAt               *time.Time `json:"last_nonempty_at,omitempty"`
	FirstEmptyAt                 *time.Time `json:"first_empty_at,omitempty"`
	LastEmptyAt                  *time.Time `json:"last_empty_at,omitempty"`
	NonemptyTransitions          int        `json:"nonempty_transitions"`
	EmptyThroughoutObservedPolls bool       `json:"empty_throughout_observed_polls"`
	ObservedIncompleteEvidence   bool       `json:"observed_incomplete_evidence"`
}

func (r *obsDeploymentBuildRecorder) observeBuildRecovery(current *obsDeploymentRuntimeBuilds, at time.Time) {
	if r.skipRuntimeDiagnostic(current, "build_recovery_health") {
		r.lastBuildRecoveryEmpty = false
		if p := r.progress.BuildRecoveryHealth; p != nil {
			p.EmptyThroughoutObservedPolls = false
		}
		return
	}

	selected := current.pipelineSelected || current.BuildRecoveryHealth != nil || r.progress.BuildRecoveryHealth != nil
	if current.Evidence != nil {
		selected = selected || current.Evidence.ExpectedRole == "single-box-v2" || current.Evidence.ExpectedRole == "control-plane-v2"
	}
	if !selected {
		return
	}
	p := r.progress.BuildRecoveryHealth
	if p == nil {
		p = &obsDeploymentBuildRecoveryProgress{ObservedIncompleteEvidence: r.diagnosticMissingBeforeFirst("build_recovery_health")}
		r.progress.BuildRecoveryHealth = p
	}
	h := current.BuildRecoveryHealth
	empty := h != nil && h.State == "empty"
	p.EmptyThroughoutObservedPolls = empty && (r.progress.Observations == 1 || p.EmptyThroughoutObservedPolls)
	p.ObservedIncompleteEvidence = p.ObservedIncompleteEvidence || h == nil || h.Incomplete || h.State == "unknown"
	if empty {
		if p.FirstEmptyAt == nil {
			p.FirstEmptyAt = obsDeploymentEvidencePtr(&at)
		}
		p.LastEmptyAt = obsDeploymentEvidencePtr(&at)
		r.lastBuildRecoveryEmpty = true
	}
	if h != nil && h.Counts.NonemptyViews > 0 {
		if p.FirstNonemptyAt == nil {
			p.FirstNonemptyAt = obsDeploymentEvidencePtr(&at)
		}
		p.LastNonemptyAt = obsDeploymentEvidencePtr(&at)
		if r.lastBuildRecoveryEmpty {
			p.NonemptyTransitions++
		}
		r.lastBuildRecoveryEmpty = false
	}
}

func cloneObsDeploymentBuildRecoveryProgress(in *obsDeploymentBuildRecoveryProgress) *obsDeploymentBuildRecoveryProgress {
	if in == nil {
		return nil
	}
	out := *in
	out.FirstNonemptyAt = obsDeploymentEvidencePtr(in.FirstNonemptyAt)
	out.LastNonemptyAt = obsDeploymentEvidencePtr(in.LastNonemptyAt)
	out.FirstEmptyAt = obsDeploymentEvidencePtr(in.FirstEmptyAt)
	out.LastEmptyAt = obsDeploymentEvidencePtr(in.LastEmptyAt)
	return &out
}

func writeObsDeploymentBuildRecoveryProgress(w io.Writer, p *obsDeploymentBuildRecoveryProgress) {
	if p == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "build_recovery_progress nonempty_transitions=%d empty_throughout_observed_polls=%t observed_incomplete_evidence=%t first_nonempty_at=%s last_nonempty_at=%s first_empty_at=%s last_empty_at=%s\n", p.NonemptyTransitions, p.EmptyThroughoutObservedPolls, p.ObservedIncompleteEvidence, obsDeploymentTimelineTime(p.FirstNonemptyAt), obsDeploymentTimelineTime(p.LastNonemptyAt), obsDeploymentTimelineTime(p.FirstEmptyAt), obsDeploymentTimelineTime(p.LastEmptyAt))
}

func writeObsDeploymentBuildRecoveryHealth(w io.Writer, h *operatorobs.RuntimeBuildRecoveryHealth) {
	if h == nil {
		return
	}
	c := h.Counts
	_, _ = fmt.Fprintf(w, "build_recovery_health advisory=true scope=%s state=%s reason=%s incomplete=%t expected=%d observed=%d complete=%d missing=%d stale=%d conflicting=%d unavailable=%d unexpected=%d nonempty_views=%d empty_views=%d running_views=%d cleanup_views=%d retried_cleanup_views=%d unsuccessful_collections=%d omitted_items=%d\n", h.Scope, h.State, h.Reason, h.Incomplete, c.Expected, c.Observed, c.Complete, c.Missing, c.Stale, c.Conflicting, c.Unavailable, c.Unexpected, c.NonemptyViews, c.EmptyViews, c.RunningViews, c.CleanupViews, c.RetriedCleanupViews, c.UnsuccessfulCollections, h.OmittedItems)
	for _, item := range h.Items {
		_, _ = fmt.Fprintf(w, "  build_recovery job=%s node=%s target_id=%s state=%s collection_state=%s collection_reason=%s collection_observed_at=%s", item.Job, item.Node, item.TargetID, item.State, item.Collection.State, item.Collection.Reason, obsDeploymentTimelineTime(item.Collection.ObservedAt))
		if v := item.Collection.Values; v != nil {
			_, _ = fmt.Fprintf(w, " collection_success=%t collection_last_success_at=%s", v.Success, obsDeploymentTimelineTime(v.LastSuccessAt))
		}
		_, _ = fmt.Fprintf(w, " running_state=%s running_reason=%s running_observed_at=%s", item.Running.State, item.Running.Reason, obsDeploymentTimelineTime(item.Running.ObservedAt))
		if v := item.Running.Values; v != nil {
			_, _ = fmt.Fprintf(w, " running_count=%d oldest_started_at=%s", v.Count, obsDeploymentTimelineTime(v.OldestStartedAt))
			if v.OldestAgeSeconds != nil {
				_, _ = fmt.Fprintf(w, " running_oldest_age_seconds=%g", *v.OldestAgeSeconds)
			}
		}
		_, _ = fmt.Fprintf(w, " cleanup_state=%s cleanup_reason=%s cleanup_observed_at=%s", item.Cleanup.State, item.Cleanup.Reason, obsDeploymentTimelineTime(item.Cleanup.ObservedAt))
		if v := item.Cleanup.Values; v != nil {
			_, _ = fmt.Fprintf(w, " cleanup_pending=%d cleanup_retried=%d oldest_created_at=%s", v.Pending, v.Retried, obsDeploymentTimelineTime(v.OldestCreatedAt))
			if v.OldestAgeSeconds != nil {
				_, _ = fmt.Fprintf(w, " cleanup_oldest_age_seconds=%g", *v.OldestAgeSeconds)
			}
		}
		_, _ = fmt.Fprintln(w)
	}
}
