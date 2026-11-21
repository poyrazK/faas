package main

import (
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

type obsDeploymentPostCaptureProgress struct {
	FirstNonemptyAt              *time.Time `json:"first_nonempty_at,omitempty"`
	LastNonemptyAt               *time.Time `json:"last_nonempty_at,omitempty"`
	FirstEmptyAt                 *time.Time `json:"first_empty_at,omitempty"`
	LastEmptyAt                  *time.Time `json:"last_empty_at,omitempty"`
	NonemptyTransitions          int        `json:"nonempty_transitions"`
	EmptyThroughoutObservedPolls bool       `json:"empty_throughout_observed_polls"`
	ObservedIncompleteEvidence   bool       `json:"observed_incomplete_evidence"`
	ObservedUnknownAges          bool       `json:"observed_unknown_ages"`
}

func (r *obsDeploymentBuildRecorder) observePostCapture(current *obsDeploymentRuntimeBuilds, at time.Time) {
	if r.skipRuntimeDiagnostic(current, "post_capture_health") {
		r.lastPostCaptureEmpty = false
		if p := r.progress.PostCaptureHealth; p != nil {
			p.EmptyThroughoutObservedPolls = false
		}
		return
	}

	selected := current.pipelineSelected || current.PostCaptureHealth != nil || r.progress.PostCaptureHealth != nil
	if current.Evidence != nil {
		selected = selected || current.Evidence.ExpectedRole == "single-box-v2" || current.Evidence.ExpectedRole == "control-plane-v2"
	}
	if !selected {
		return
	}
	p := r.progress.PostCaptureHealth
	if p == nil {
		p = &obsDeploymentPostCaptureProgress{ObservedIncompleteEvidence: r.diagnosticMissingBeforeFirst("post_capture_health")}
		r.progress.PostCaptureHealth = p
	}
	h := current.PostCaptureHealth
	empty := h != nil && h.State == "empty"
	p.EmptyThroughoutObservedPolls = empty && (r.progress.Observations == 1 || p.EmptyThroughoutObservedPolls)
	p.ObservedIncompleteEvidence = p.ObservedIncompleteEvidence || h == nil || h.Incomplete || h.State == "unknown"
	p.ObservedUnknownAges = p.ObservedUnknownAges || h != nil && h.Counts.UnknownAgeViews > 0
	if empty {
		if p.FirstEmptyAt == nil {
			p.FirstEmptyAt = obsDeploymentEvidencePtr(&at)
		}
		p.LastEmptyAt = obsDeploymentEvidencePtr(&at)
		r.lastPostCaptureEmpty = true
	}
	if h != nil && h.Counts.NonemptyViews > 0 {
		if p.FirstNonemptyAt == nil {
			p.FirstNonemptyAt = obsDeploymentEvidencePtr(&at)
		}
		p.LastNonemptyAt = obsDeploymentEvidencePtr(&at)
		if r.lastPostCaptureEmpty {
			p.NonemptyTransitions++
		}
		r.lastPostCaptureEmpty = false
	}
}

func cloneObsDeploymentPostCaptureProgress(in *obsDeploymentPostCaptureProgress) *obsDeploymentPostCaptureProgress {
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

func writeObsDeploymentPostCaptureProgress(w io.Writer, p *obsDeploymentPostCaptureProgress) {
	if p == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "post_capture_progress nonempty_transitions=%d empty_throughout_observed_polls=%t observed_incomplete_evidence=%t observed_unknown_ages=%t first_nonempty_at=%s last_nonempty_at=%s first_empty_at=%s last_empty_at=%s\n", p.NonemptyTransitions, p.EmptyThroughoutObservedPolls, p.ObservedIncompleteEvidence, p.ObservedUnknownAges, obsDeploymentTimelineTime(p.FirstNonemptyAt), obsDeploymentTimelineTime(p.LastNonemptyAt), obsDeploymentTimelineTime(p.FirstEmptyAt), obsDeploymentTimelineTime(p.LastEmptyAt))
}

func writeObsDeploymentPostCaptureHealth(w io.Writer, h *operatorobs.RuntimePostCaptureHealth) {
	if h == nil {
		return
	}
	c := h.Counts
	_, _ = fmt.Fprintf(w, "post_capture_health advisory=true scope=%s state=%s reason=%s incomplete=%t expected=%d observed=%d complete=%d missing=%d stale=%d conflicting=%d unavailable=%d unexpected=%d nonempty_views=%d empty_views=%d unknown_age_views=%d unsuccessful_collections=%d enabled_views=%d disabled_views=%d omitted_items=%d\n", h.Scope, h.State, h.Reason, h.Incomplete, c.Expected, c.Observed, c.Complete, c.Missing, c.Stale, c.Conflicting, c.Unavailable, c.Unexpected, c.NonemptyViews, c.EmptyViews, c.UnknownAgeViews, c.UnsuccessfulCollections, c.EnabledViews, c.DisabledViews, h.OmittedItems)
	for _, item := range h.Items {
		_, _ = fmt.Fprintf(w, "  post_capture job=%s node=%s target_id=%s state=%s collection_state=%s collection_reason=%s collection_observed_at=%s observation_state=%s observation_reason=%s observation_observed_at=%s", item.Job, item.Node, item.TargetID, item.State, item.Collection.State, item.Collection.Reason, obsDeploymentTimelineTime(item.Collection.ObservedAt), item.Observation.State, item.Observation.Reason, obsDeploymentTimelineTime(item.Observation.ObservedAt))
		if v := item.Collection.Values; v != nil {
			_, _ = fmt.Fprintf(w, " collection_success=%t collection_last_success_at=%s", v.Success, obsDeploymentTimelineTime(v.LastSuccessAt))
		}
		if v := item.Observation.Values; v != nil {
			_, _ = fmt.Fprintf(w, " observation_enabled=%t", v.Enabled)
		}
		_, _ = fmt.Fprintln(w)
		for _, p := range item.Classes {
			_, _ = fmt.Fprintf(w, "    class=%s state=%s reason=%s observed_at=%s", p.Class, p.State, p.Reason, obsDeploymentTimelineTime(p.ObservedAt))
			if v := p.Values; v != nil {
				_, _ = fmt.Fprintf(w, " pending=%d unknown_age=%d oldest_boundary_at=%s", v.Pending, v.UnknownAge, obsDeploymentTimelineTime(v.OldestBoundaryAt))
				if v.OldestKnownAgeSeconds != nil {
					_, _ = fmt.Fprintf(w, " oldest_known_age_seconds=%g", *v.OldestKnownAgeSeconds)
				}
			}
			_, _ = fmt.Fprintln(w)
		}
	}
}
