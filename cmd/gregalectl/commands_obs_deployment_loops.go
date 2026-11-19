package main

import (
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

type obsDeploymentLoopProgress struct {
	FirstStallAt                           *time.Time `json:"first_stall_at,omitempty"`
	LastStallAt                            *time.Time `json:"last_stall_at,omitempty"`
	FirstNoReportedStallAt                 *time.Time `json:"first_no_reported_stall_at,omitempty"`
	LastNoReportedStallAt                  *time.Time `json:"last_no_reported_stall_at,omitempty"`
	LastRecoveryAt                         *time.Time `json:"last_recovery_at,omitempty"`
	LastRegressionAt                       *time.Time `json:"last_regression_at,omitempty"`
	StallRegressions                       int        `json:"stall_regressions"`
	NoReportedStallThroughoutObservedPolls bool       `json:"no_reported_stall_throughout_observed_polls"`
	ObservedIncompleteEvidence             bool       `json:"observed_incomplete_evidence"`
}

func (r *obsDeploymentBuildRecorder) observeLoops(current *obsDeploymentRuntimeBuilds, at time.Time) {
	p := r.progress.LoopHealth
	if p == nil {
		p = &obsDeploymentLoopProgress{}
		r.progress.LoopHealth = p
	}
	loops := current.LoopHealth
	clear := loops != nil && loops.State == "no_reported_stall"
	p.NoReportedStallThroughoutObservedPolls = clear && (r.progress.Observations == 1 || p.NoReportedStallThroughoutObservedPolls)
	p.ObservedIncompleteEvidence = p.ObservedIncompleteEvidence || loops == nil || loops.Incomplete || loops.State == "unknown"
	if clear {
		if p.FirstNoReportedStallAt == nil {
			p.FirstNoReportedStallAt = obsDeploymentEvidencePtr(&at)
		}
		p.LastNoReportedStallAt = obsDeploymentEvidencePtr(&at)
		if r.pendingLoopStall {
			p.LastRecoveryAt = obsDeploymentEvidencePtr(&at)
			r.pendingLoopStall = false
		}
		r.lastLoopsClear = true
	}
	if loops != nil && loops.Counts.ReportedStalls > 0 {
		if p.FirstStallAt == nil {
			p.FirstStallAt = obsDeploymentEvidencePtr(&at)
		}
		p.LastStallAt = obsDeploymentEvidencePtr(&at)
		if r.lastLoopsClear {
			p.StallRegressions++
			p.LastRegressionAt = obsDeploymentEvidencePtr(&at)
		}
		r.pendingLoopStall, r.lastLoopsClear = true, false
	}
}

func cloneObsDeploymentLoopProgress(in *obsDeploymentLoopProgress) *obsDeploymentLoopProgress {
	if in == nil {
		return nil
	}
	out := *in
	out.FirstStallAt = obsDeploymentEvidencePtr(in.FirstStallAt)
	out.LastStallAt = obsDeploymentEvidencePtr(in.LastStallAt)
	out.FirstNoReportedStallAt = obsDeploymentEvidencePtr(in.FirstNoReportedStallAt)
	out.LastNoReportedStallAt = obsDeploymentEvidencePtr(in.LastNoReportedStallAt)
	out.LastRecoveryAt = obsDeploymentEvidencePtr(in.LastRecoveryAt)
	out.LastRegressionAt = obsDeploymentEvidencePtr(in.LastRegressionAt)
	return &out
}

func writeObsDeploymentLoopHealth(w io.Writer, loops *operatorobs.RuntimeLoopHealth) {
	if loops == nil {
		_, _ = fmt.Fprintln(w, "loop_health advisory=true state=unknown reason=loop_evidence_not_returned")
		return
	}
	c := loops.Counts
	_, _ = fmt.Fprintf(w, "loop_health advisory=true state=%s reason=%s incomplete=%t expected=%d observed=%d no_reported_stall=%d stalled=%d missing=%d stale=%d conflicting=%d unavailable=%d reported_stalls=%d unexpected=%d omitted_items=%d\n", loops.State, loops.Reason, loops.Incomplete, c.Expected, c.Observed, c.NoReportedStall, c.Stalled, c.Missing, c.Stale, c.Conflicting, c.Unavailable, c.ReportedStalls, c.Unexpected, loops.OmittedItems)
	for _, item := range loops.Items {
		_, _ = fmt.Fprintf(w, "  loop job=%s daemon=%s node=%s target_id=%s loop=%s state=%s reason=%s scrape_observed_at=%s stall_observed_at=%s age_observed_at=%s", item.Job, item.Daemon, item.Node, item.TargetID, item.Loop, item.State, item.Reason, obsDeploymentTimelineTime(item.ScrapeObservedAt), obsDeploymentTimelineTime(item.StallObservedAt), obsDeploymentTimelineTime(item.AgeObservedAt))
		if item.ReportedStalled != nil {
			_, _ = fmt.Fprintf(w, " reported_stalled=%t", *item.ReportedStalled)
		}
		if item.AgeSeconds != nil {
			_, _ = fmt.Fprintf(w, " age_seconds=%g", *item.AgeSeconds)
		}
		_, _ = fmt.Fprintln(w)
	}
}

func writeObsDeploymentLoopProgress(w io.Writer, p *obsDeploymentLoopProgress) {
	if p == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "loop_progress stall_regressions=%d no_reported_stall_throughout_observed_polls=%t observed_incomplete_evidence=%t first_stall_at=%s last_stall_at=%s first_no_reported_stall_at=%s last_no_reported_stall_at=%s last_recovery_at=%s last_regression_at=%s\n", p.StallRegressions, p.NoReportedStallThroughoutObservedPolls, p.ObservedIncompleteEvidence, obsDeploymentTimelineTime(p.FirstStallAt), obsDeploymentTimelineTime(p.LastStallAt), obsDeploymentTimelineTime(p.FirstNoReportedStallAt), obsDeploymentTimelineTime(p.LastNoReportedStallAt), obsDeploymentTimelineTime(p.LastRecoveryAt), obsDeploymentTimelineTime(p.LastRegressionAt))
}
