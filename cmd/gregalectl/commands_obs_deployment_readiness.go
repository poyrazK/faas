package main

import (
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type obsDeploymentReadinessProgress struct {
	FirstReadyAt                 *time.Time `json:"first_ready_at,omitempty"`
	LastReadyAt                  *time.Time `json:"last_ready_at,omitempty"`
	FirstNotReadyAt              *time.Time `json:"first_not_ready_at,omitempty"`
	LastNotReadyAt               *time.Time `json:"last_not_ready_at,omitempty"`
	LastRecoveryAt               *time.Time `json:"last_recovery_at,omitempty"`
	LastLossAt                   *time.Time `json:"last_loss_at,omitempty"`
	Losses                       int        `json:"losses"`
	ReadyThroughoutObservedPolls bool       `json:"ready_throughout_observed_polls"`
	ObservedIncompleteEvidence   bool       `json:"observed_incomplete_evidence"`
}

func (r *obsDeploymentBuildRecorder) observeReadiness(current *obsDeploymentRuntimeBuilds, at time.Time) {
	p := r.progress.Readiness
	if p == nil {
		p = &obsDeploymentReadinessProgress{}
		r.progress.Readiness = p
	}
	var readiness *api.ObsRuntimeReadiness
	if current.Evidence != nil {
		readiness = current.Evidence.Readiness
	}
	state := "unknown"
	if readiness != nil {
		state = readiness.State
	}
	p.ReadyThroughoutObservedPolls = state == "ready" && (r.progress.Observations == 1 || p.ReadyThroughoutObservedPolls)
	p.ObservedIncompleteEvidence = p.ObservedIncompleteEvidence || readiness == nil || state == "incomplete" || state == "unknown"
	if readiness != nil {
		c := readiness.Counts
		p.ObservedIncompleteEvidence = p.ObservedIncompleteEvidence || c.Missing+c.Stale+c.Conflicting+c.Unavailable > 0 || current.Evidence.Counts.Unexpected > 0
		for _, job := range current.Evidence.Jobs {
			p.ObservedIncompleteEvidence = p.ObservedIncompleteEvidence || job.State != "available"
		}
	}
	switch state {
	case "ready":
		if p.FirstReadyAt == nil {
			p.FirstReadyAt = obsDeploymentEvidencePtr(&at)
		}
		p.LastReadyAt = obsDeploymentEvidencePtr(&at)
		if r.pendingNotReady {
			p.LastRecoveryAt = obsDeploymentEvidencePtr(&at)
			r.pendingNotReady = false
		}
		r.lastReadinessReady = true
	case "not_ready":
		if p.FirstNotReadyAt == nil {
			p.FirstNotReadyAt = obsDeploymentEvidencePtr(&at)
		}
		p.LastNotReadyAt = obsDeploymentEvidencePtr(&at)
		if r.lastReadinessReady {
			p.Losses++
			p.LastLossAt = obsDeploymentEvidencePtr(&at)
		}
		r.lastReadinessReady = false
		r.pendingNotReady = true
	}
}

func cloneObsDeploymentReadinessProgress(in *obsDeploymentReadinessProgress) *obsDeploymentReadinessProgress {
	if in == nil {
		return nil
	}
	out := *in
	out.FirstReadyAt = obsDeploymentEvidencePtr(in.FirstReadyAt)
	out.LastReadyAt = obsDeploymentEvidencePtr(in.LastReadyAt)
	out.FirstNotReadyAt = obsDeploymentEvidencePtr(in.FirstNotReadyAt)
	out.LastNotReadyAt = obsDeploymentEvidencePtr(in.LastNotReadyAt)
	out.LastRecoveryAt = obsDeploymentEvidencePtr(in.LastRecoveryAt)
	out.LastLossAt = obsDeploymentEvidencePtr(in.LastLossAt)
	return &out
}

func cloneObsDeploymentReadiness(in *api.ObsRuntimeReadiness) *api.ObsRuntimeReadiness {
	if in == nil {
		return nil
	}
	out := *in
	out.Reasons = slices.Clone(in.Reasons)
	return &out
}

func writeObsDeploymentReadiness(w io.Writer, r *api.ObsRuntimeReadiness) {
	if r == nil {
		_, _ = fmt.Fprintln(w, "runtime_readiness advisory=true state=unknown reason=readiness_not_returned")
		return
	}
	c := r.Counts
	_, _ = fmt.Fprintf(w, "runtime_readiness advisory=true state=%s reason=%s ready=%d not_ready=%d missing=%d stale=%d conflicting=%d unavailable=%d\n", r.State, r.Reason, c.Ready, c.NotReady, c.Missing, c.Stale, c.Conflicting, c.Unavailable)
	for _, reason := range r.Reasons {
		_, _ = fmt.Fprintf(w, "  readiness_reason=%s count=%d\n", reason.Reason, reason.Count)
	}
}

func writeObsDeploymentReadinessProgress(w io.Writer, p *obsDeploymentReadinessProgress) {
	if p == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "readiness_progress losses=%d ready_throughout_observed_polls=%t observed_incomplete_evidence=%t first_ready_at=%s last_ready_at=%s first_not_ready_at=%s last_not_ready_at=%s last_recovery_at=%s last_loss_at=%s\n", p.Losses, p.ReadyThroughoutObservedPolls, p.ObservedIncompleteEvidence, obsDeploymentTimelineTime(p.FirstReadyAt), obsDeploymentTimelineTime(p.LastReadyAt), obsDeploymentTimelineTime(p.FirstNotReadyAt), obsDeploymentTimelineTime(p.LastNotReadyAt), obsDeploymentTimelineTime(p.LastRecoveryAt), obsDeploymentTimelineTime(p.LastLossAt))
}
