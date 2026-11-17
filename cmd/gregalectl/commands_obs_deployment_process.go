package main

import (
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type obsDeploymentProcessProgress struct {
	PollsWithComparison        int        `json:"polls_with_comparison"`
	ComparedPairs              int        `json:"compared_pairs"`
	ObservedChanges            int        `json:"observed_changes"`
	SameRevisionChanges        int        `json:"same_revision_changes"`
	DifferentRevisionChanges   int        `json:"different_revision_changes"`
	UnknownRevisionChanges     int        `json:"unknown_revision_changes"`
	NewTargets                 int        `json:"new_targets"`
	RemovedTargets             int        `json:"removed_targets"`
	FirstChangeAt              *time.Time `json:"first_change_at,omitempty"`
	LastChangeAt               *time.Time `json:"last_change_at,omitempty"`
	ObservedIncompleteEvidence bool       `json:"observed_incomplete_evidence"`
}

func (r *obsDeploymentBuildRecorder) observeProcess(current *obsDeploymentRuntimeBuilds, at time.Time) {
	p := r.progress.Process
	if p == nil {
		p = &obsDeploymentProcessProgress{}
		r.progress.Process = p
	}
	if current.Evidence == nil || current.Evidence.Process == nil {
		p.ObservedIncompleteEvidence = true
		return
	}
	process := current.Evidence.Process
	c := process.Counts
	p.ObservedIncompleteEvidence = p.ObservedIncompleteEvidence || process.Incomplete
	end := current.Evidence.GeneratedAt
	if !r.lastProcessAt.IsZero() && (!end.After(r.lastProcessAt) || process.ComparisonAt != nil && process.ComparisonAt.Before(r.lastProcessAt)) {
		p.ObservedIncompleteEvidence = true
		return
	}
	r.lastProcessAt = end
	if c.Compared > 0 {
		p.PollsWithComparison++
	}
	p.ComparedPairs += c.Compared
	p.ObservedChanges += c.Changed
	p.SameRevisionChanges += c.SameRevisionChanges
	p.DifferentRevisionChanges += c.DifferentRevisionChanges
	p.UnknownRevisionChanges += c.UnknownRevisionChanges
	p.NewTargets += c.NewTargets
	p.RemovedTargets += c.RemovedTargets
	if c.Changed > 0 {
		if p.FirstChangeAt == nil {
			p.FirstChangeAt = obsDeploymentEvidencePtr(&at)
		}
		p.LastChangeAt = obsDeploymentEvidencePtr(&at)
	}
}

func cloneObsDeploymentProcessProgress(in *obsDeploymentProcessProgress) *obsDeploymentProcessProgress {
	if in == nil {
		return nil
	}
	out := *in
	out.FirstChangeAt = obsDeploymentEvidencePtr(in.FirstChangeAt)
	out.LastChangeAt = obsDeploymentEvidencePtr(in.LastChangeAt)
	return &out
}

func cloneObsDeploymentProcess(in *api.ObsRuntimeProcess) *api.ObsRuntimeProcess {
	if in == nil {
		return nil
	}
	out := *in
	out.ComparisonAt = obsDeploymentEvidencePtr(in.ComparisonAt)
	return &out
}

func writeObsDeploymentProcess(w io.Writer, p *api.ObsRuntimeProcess) {
	if p == nil {
		_, _ = fmt.Fprintln(w, "runtime_process advisory=true state=unknown reason=process_evidence_not_returned")
		return
	}
	c := p.Counts
	_, _ = fmt.Fprintf(w, "runtime_process advisory=true state=%s reason=%s comparison_at=%s incomplete=%t fresh=%d missing=%d stale=%d conflicting=%d unavailable=%d compared=%d changed=%d unchanged=%d same_revision_changes=%d different_revision_changes=%d unknown_revision_changes=%d new_targets=%d removed_targets=%d repeated_samples=%d comparison_unavailable=%d\n", p.State, p.Reason, obsDeploymentTimelineTime(p.ComparisonAt), p.Incomplete, c.Fresh, c.Missing, c.Stale, c.Conflicting, c.Unavailable, c.Compared, c.Changed, c.Unchanged, c.SameRevisionChanges, c.DifferentRevisionChanges, c.UnknownRevisionChanges, c.NewTargets, c.RemovedTargets, c.RepeatedSamples, c.ComparisonUnavailable)
}

func writeObsDeploymentProcessItem(w io.Writer, item api.ObsRuntimeBuildItem) {
	p := item.Process
	_, _ = fmt.Fprintf(w, "  process job=%s daemon=%s node=%s state=%s reason=%s comparison=%s comparison_reason=%s revision_relation=%s observed_at=%s previous_observed_at=%s", item.Job, item.Daemon, item.Node, p.State, p.Reason, p.Comparison, p.ComparisonReason, p.RevisionRelation, obsDeploymentTimelineTime(p.ObservedAt), obsDeploymentTimelineTime(p.PreviousObservedAt))
	if p.UptimeSeconds != nil {
		_, _ = fmt.Fprintf(w, " uptime_seconds=%g", *p.UptimeSeconds)
	}
	if p.PreviousUptimeSeconds != nil {
		_, _ = fmt.Fprintf(w, " previous_uptime_seconds=%g", *p.PreviousUptimeSeconds)
	}
	_, _ = fmt.Fprintln(w)
}

func writeObsDeploymentProcessProgress(w io.Writer, p *obsDeploymentProcessProgress) {
	if p == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "process_progress polls_with_comparison=%d compared_pairs=%d observed_changes=%d same_revision_changes=%d different_revision_changes=%d unknown_revision_changes=%d new_targets=%d removed_targets=%d observed_incomplete_evidence=%t first_change_at=%s last_change_at=%s\n", p.PollsWithComparison, p.ComparedPairs, p.ObservedChanges, p.SameRevisionChanges, p.DifferentRevisionChanges, p.UnknownRevisionChanges, p.NewTargets, p.RemovedTargets, p.ObservedIncompleteEvidence, obsDeploymentTimelineTime(p.FirstChangeAt), obsDeploymentTimelineTime(p.LastChangeAt))
}
