package main

import (
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

type obsDeploymentPreparationProgress struct {
	obsDeploymentHandoffProgress
	ObservedUnknownAges bool `json:"observed_unknown_ages"`
}

func (r *obsDeploymentBuildRecorder) observePreparation(current *obsDeploymentRuntimeBuilds, at time.Time) {
	if r.skipRuntimeDiagnostic(current, "preparation_health") {
		r.lastPreparationEmpty = false
		if p := r.progress.PreparationHealth; p != nil {
			p.EmptyThroughoutObservedPolls = false
		}
		return
	}

	selected := current.pipelineSelected || current.PreparationHealth != nil || r.progress.PreparationHealth != nil
	if current.Evidence != nil {
		selected = selected || current.Evidence.ExpectedRole == "single-box-v2" || current.Evidence.ExpectedRole == "control-plane-v2"
	}
	if !selected {
		return
	}
	p := r.progress.PreparationHealth
	if p == nil {
		p = &obsDeploymentPreparationProgress{obsDeploymentHandoffProgress: obsDeploymentHandoffProgress{ObservedIncompleteEvidence: r.diagnosticMissingBeforeFirst("preparation_health")}}
		r.progress.PreparationHealth = p
	}
	h := current.PreparationHealth
	empty := h != nil && h.State == "empty"
	p.EmptyThroughoutObservedPolls = empty && (r.progress.Observations == 1 || p.EmptyThroughoutObservedPolls)
	p.ObservedIncompleteEvidence = p.ObservedIncompleteEvidence || h == nil || h.Incomplete || h.State == "unknown"
	p.ObservedUnknownAges = p.ObservedUnknownAges || h != nil && h.Counts.UnknownAgeScopes > 0
	if empty {
		if p.FirstEmptyAt == nil {
			p.FirstEmptyAt = obsDeploymentEvidencePtr(&at)
		}
		p.LastEmptyAt = obsDeploymentEvidencePtr(&at)
		r.lastPreparationEmpty = true
	}
	if h != nil && h.Counts.WaitingScopes > 0 {
		if p.FirstWaitingAt == nil {
			p.FirstWaitingAt = obsDeploymentEvidencePtr(&at)
		}
		p.LastWaitingAt = obsDeploymentEvidencePtr(&at)
		if r.lastPreparationEmpty {
			p.WaitingTransitions++
		}
		r.lastPreparationEmpty = false
	}
}

func cloneObsDeploymentPreparationProgress(in *obsDeploymentPreparationProgress) *obsDeploymentPreparationProgress {
	if in == nil {
		return nil
	}
	out := *in
	out.obsDeploymentHandoffProgress = *cloneObsDeploymentHandoffProgress(&in.obsDeploymentHandoffProgress)
	return &out
}

func writeObsDeploymentPreparationProgress(w io.Writer, p *obsDeploymentPreparationProgress) {
	if p == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "preparation_progress waiting_transitions=%d empty_throughout_observed_polls=%t observed_incomplete_evidence=%t observed_unknown_ages=%t first_waiting_at=%s last_waiting_at=%s first_empty_at=%s last_empty_at=%s\n", p.WaitingTransitions, p.EmptyThroughoutObservedPolls, p.ObservedIncompleteEvidence, p.ObservedUnknownAges, obsDeploymentTimelineTime(p.FirstWaitingAt), obsDeploymentTimelineTime(p.LastWaitingAt), obsDeploymentTimelineTime(p.FirstEmptyAt), obsDeploymentTimelineTime(p.LastEmptyAt))
}

func writeObsDeploymentPreparationHealth(w io.Writer, h *operatorobs.RuntimePreparationHealth) {
	if h == nil {
		return
	}
	c := h.Counts
	_, _ = fmt.Fprintf(w, "preparation_health advisory=true state=%s reason=%s incomplete=%t expected=%d observed=%d complete=%d missing=%d stale=%d conflicting=%d unavailable=%d unexpected=%d waiting_scopes=%d empty_scopes=%d unknown_age_scopes=%d unsuccessful_collections=%d enabled_scopes=%d disabled_scopes=%d omitted_items=%d\n", h.State, h.Reason, h.Incomplete, c.Expected, c.Observed, c.Complete, c.Missing, c.Stale, c.Conflicting, c.Unavailable, c.Unexpected, c.WaitingScopes, c.EmptyScopes, c.UnknownAgeScopes, c.UnsuccessfulCollections, c.EnabledScopes, c.DisabledScopes, h.OmittedItems)
	for _, item := range h.Items {
		_, _ = fmt.Fprintf(w, "  preparation node=%s target_id=%s state=%s collection_state=%s collection_reason=%s collection_observed_at=%s observation_state=%s observation_reason=%s observation_observed_at=%s", item.Node, item.TargetID, item.State, item.Collection.State, item.Collection.Reason, obsDeploymentTimelineTime(item.Collection.ObservedAt), item.Observation.State, item.Observation.Reason, obsDeploymentTimelineTime(item.Observation.ObservedAt))
		if v := item.Collection.Values; v != nil {
			_, _ = fmt.Fprintf(w, " collection_success=%t collection_last_success_at=%s", v.Success, obsDeploymentTimelineTime(v.LastSuccessAt))
		}
		if v := item.Observation.Values; v != nil {
			_, _ = fmt.Fprintf(w, " observation_enabled=%t", v.Enabled)
		}
		_, _ = fmt.Fprintln(w)
		for _, p := range item.Phases {
			_, _ = fmt.Fprintf(w, "    phase=%s state=%s reason=%s observed_at=%s", p.Phase, p.State, p.Reason, obsDeploymentTimelineTime(p.ObservedAt))
			if v := p.Values; v != nil {
				_, _ = fmt.Fprintf(w, " pending=%d unknown_age=%d oldest_phase_started_at=%s", v.Pending, v.UnknownAge, obsDeploymentTimelineTime(v.OldestPhaseStartedAt))
				if v.OldestKnownAgeSeconds != nil {
					_, _ = fmt.Fprintf(w, " oldest_known_age_seconds=%g", *v.OldestKnownAgeSeconds)
				}
			}
			_, _ = fmt.Fprintln(w)
		}
	}
}
