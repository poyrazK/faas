package main

import (
	"fmt"
	"io"
	"strings"
	"time"
)

func writeObsDeploymentBuildHistory(w io.Writer, builds *obsDeploymentRuntimeBuilds) {
	if p := builds.Progress; p != nil {
		writeObsDeploymentReadinessProgress(w, p.Readiness)
		writeObsDeploymentProcessProgress(w, p.Process)
		writeObsDeploymentLoopProgress(w, p.LoopHealth)
		writeObsDeploymentBuilderProgress(w, p.BuilderHealth)
		writeObsDeploymentHandoffProgress(w, p.HandoffHealth)
		writeObsDeploymentBuildRecoveryProgress(w, p.BuildRecoveryHealth)
		writeObsDeploymentAppTaskProgress(w, p.AppTaskHealth)
		writeObsDeploymentCommandCronProgress(w, p.CommandCronHealth)
		writeObsDeploymentCommandCronOutcomesProgress(w, p.CommandCronOutcomes)
		writeObsDeploymentHTTPCronDeliveryProgress(w, p.HTTPCronDelivery)
		writeObsDeploymentDelayedTaskDispatchProgress(w, p.DelayedTaskDispatch)
		writeObsDeploymentCommandCronTimingProgress(w, p.CommandCronTiming)
		writeObsDeploymentAppTaskResultsProgress(w, p.AppTaskResults)
		writeObsDeploymentAppTaskOperationsProgress(w, p.AppTaskOperations)
		writeObsDeploymentPostCaptureProgress(w, p.PostCaptureHealth)
		writeObsDeploymentSnapshotPrimeProgress(w, p.SnapshotPrimeHealth)
		writeObsDeploymentPreparationProgress(w, p.PreparationHealth)
		_, _ = fmt.Fprintf(w, "runtime_build_progress observations=%d matching_throughout_observed_polls=%t observed_incomplete_evidence=%t observed_source_unavailable=%t regressions=%d\n", p.Observations, p.MatchingThroughoutObservedPolls, p.ObservedIncompleteEvidence, p.ObservedSourceUnavailable, p.Regressions)
		_, _ = fmt.Fprintf(w, "  first_observed_at=%s last_observed_at=%s first_mismatch_at=%s last_mismatch_at=%s first_matching_at=%s last_matching_at=%s last_recovery_at=%s last_regression_at=%s\n", obsDeploymentTimelineTime(p.FirstObservedAt), obsDeploymentTimelineTime(p.LastObservedAt), obsDeploymentTimelineTime(p.FirstMismatchAt), obsDeploymentTimelineTime(p.LastMismatchAt), obsDeploymentTimelineTime(p.FirstMatchingAt), obsDeploymentTimelineTime(p.LastMatchingAt), obsDeploymentTimelineTime(p.LastRecoveryAt), obsDeploymentTimelineTime(p.LastRegressionAt))
	}
	h := builds.History
	if h == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "runtime_build_history scope=%s total_events=%d retained_events=%d dropped_events=%d envelope_omitted_events=%d detail_omitted_events=%d retained_bytes=%d event_limit=%d byte_limit=%d truncated=%t\n", h.Scope, h.TotalEvents, h.RetainedEvents, h.DroppedEvents, h.EnvelopeOmittedEvents, h.DetailOmittedEvents, h.RetainedBytes, h.EventLimit, h.ByteLimit, h.Truncated)
	for _, e := range h.Events {
		if expanded, err := expandObsDeploymentBuildEvent(e); err == nil {
			e = expanded
		}
		_, _ = fmt.Fprintf(w, "  at=%s observation=%d state=%s reason=%s evidence_generated_at=%s", e.At.UTC().Format(time.RFC3339Nano), e.Observation, e.State, e.Reason, obsDeploymentTimelineTime(e.EvidenceGeneratedAt))
		if e.Diagnostics != nil {
			_, _ = fmt.Fprintf(w, " diagnostics_source=%s selected=%s not_selected=%s", e.DiagnosticsSource, strings.Join(e.Diagnostics.Selected, ","), strings.Join(e.Diagnostics.NotSelected, ","))
		}
		if e.SourceHTTPStatus != nil {
			_, _ = fmt.Fprintf(w, " source_http_status=%d", *e.SourceHTTPStatus)
		}
		if c := e.Counts; c != nil {
			_, _ = fmt.Fprintf(w, " expected=%d observed=%d matching=%d mismatched=%d missing=%d stale=%d unavailable=%d unexpected=%d distinct_revisions=%d", c.Expected, c.Observed, c.Matching, c.Mismatched, c.Missing, c.Stale, c.Unavailable, c.Unexpected, c.DistinctRevisions)
		}
		if e.Readiness != nil {
			_, _ = fmt.Fprintf(w, " readiness_state=%s readiness_not_ready=%d readiness_missing=%d readiness_stale=%d readiness_conflicting=%d readiness_unavailable=%d", e.Readiness.State, e.Readiness.Counts.NotReady, e.Readiness.Counts.Missing, e.Readiness.Counts.Stale, e.Readiness.Counts.Conflicting, e.Readiness.Counts.Unavailable)
		}
		if e.Process != nil {
			_, _ = fmt.Fprintf(w, " process_state=%s process_changed=%d process_compared=%d process_incomplete=%t", e.Process.State, e.Process.Counts.Changed, e.Process.Counts.Compared, e.Process.Incomplete)
		}
		if e.LoopHealth != nil {
			_, _ = fmt.Fprintf(w, " loop_state=%s reported_stalls=%d loop_incomplete=%t", e.LoopHealth.State, e.LoopHealth.Counts.ReportedStalls, e.LoopHealth.Incomplete)
		}
		if e.BuilderHealth != nil {
			c := e.BuilderHealth.Counts
			_, _ = fmt.Fprintf(w, " builder_state=%s overdue_workers=%d waiting_queues=%d saturated_slots=%d builder_incomplete=%t", e.BuilderHealth.State, c.OverdueWorkers, c.WaitingQueues, c.SaturatedSlots, e.BuilderHealth.Incomplete)
		}
		if e.HandoffHealth != nil {
			c := e.HandoffHealth.Counts
			_, _ = fmt.Fprintf(w, " handoff_state=%s waiting_scopes=%d empty_scopes=%d unsuccessful_collections=%d handoff_incomplete=%t", e.HandoffHealth.State, c.WaitingScopes, c.EmptyScopes, c.UnsuccessfulCollections, e.HandoffHealth.Incomplete)
		}
		if e.BuildRecoveryHealth != nil {
			c := e.BuildRecoveryHealth.Counts
			_, _ = fmt.Fprintf(w, " build_recovery_state=%s running_views=%d cleanup_views=%d retried_cleanup_views=%d build_recovery_incomplete=%t", e.BuildRecoveryHealth.State, c.RunningViews, c.CleanupViews, c.RetriedCleanupViews, e.BuildRecoveryHealth.Incomplete)
		}
		if e.AppTaskHealth != nil {
			c := e.AppTaskHealth.Counts
			_, _ = fmt.Fprintf(w, " app_task_state=%s app_task_nonempty_views=%d app_task_empty_views=%d app_task_unknown_age_views=%d app_task_incomplete=%t", e.AppTaskHealth.State, c.NonemptyViews, c.EmptyViews, c.UnknownAgeViews, e.AppTaskHealth.Incomplete)
			for _, p := range e.AppTaskHealth.Classes {
				_, _ = fmt.Fprintf(w, " %s_nonempty_views=%d %s_unknown_age_views=%d", p.Class, p.NonemptyViews, p.Class, p.UnknownAgeViews)
			}
		}
		if e.CommandCronHealth != nil {
			c := e.CommandCronHealth.Counts
			_, _ = fmt.Fprintf(w, " command_cron_due_scopes=%d command_cron_inactive_scopes=%d command_cron_sweep_running_scopes=%d command_cron_sweep_idle_scopes=%d", c.DueScopes, c.InactiveScopes, c.SweepRunningScopes, c.SweepIdleScopes)
			_, _ = fmt.Fprintf(w, " command_cron_state=%s command_cron_nonempty_scopes=%d command_cron_empty_scopes=%d command_cron_unknown_age_scopes=%d command_cron_incomplete=%t", e.CommandCronHealth.State, c.NonemptyScopes, c.EmptyScopes, c.UnknownAgeScopes, e.CommandCronHealth.Incomplete)
			for _, p := range e.CommandCronHealth.Classes {
				_, _ = fmt.Fprintf(w, " %s_nonempty_scopes=%d %s_unknown_age_scopes=%d", p.Class, p.NonemptyScopes, p.Class, p.UnknownAgeScopes)
			}
		}
		if e.CommandCronOutcomes != nil {
			c := e.CommandCronOutcomes.Counts
			_, _ = fmt.Fprintf(w, " command_cron_outcomes_state=%s increased_signals=%d unchanged_signals=%d baseline_signals=%d unknown_signals=%d", e.CommandCronOutcomes.State, c.Increased, c.Unchanged, c.Baseline, c.Missing+c.Stale+c.Conflicting+c.Unavailable)
		}
		if e.HTTPCronDelivery != nil {
			c := e.HTTPCronDelivery.Counts
			_, _ = fmt.Fprintf(w, " http_cron_delivery_state=%s increased_signals=%d unchanged_signals=%d baseline_signals=%d unknown_signals=%d", e.HTTPCronDelivery.State, c.Increased, c.Unchanged, c.Baseline, c.Missing+c.Stale+c.Conflicting+c.Unavailable)
		}
		if e.DelayedTaskDispatch != nil {
			c := e.DelayedTaskDispatch.Counts
			_, _ = fmt.Fprintf(w, " delayed_task_dispatch_state=%s increased_signals=%d unchanged_signals=%d baseline_signals=%d unknown_signals=%d", e.DelayedTaskDispatch.State, c.Increased, c.Unchanged, c.Baseline, c.Missing+c.Stale+c.Conflicting+c.Unavailable)
		}
		if e.CommandCronTiming != nil {
			c := e.CommandCronTiming.Counts
			_, _ = fmt.Fprintf(w, " command_cron_timing_state=%s increased_signals=%d unchanged_signals=%d baseline_signals=%d unknown_signals=%d", e.CommandCronTiming.State, c.Increased, c.Unchanged, c.Baseline, c.Missing+c.Stale+c.Conflicting+c.Unavailable)
		}
		if e.AppTaskResults != nil {
			c := e.AppTaskResults.Counts
			_, _ = fmt.Fprintf(w, " app_task_results_state=%s increased_signals=%d unchanged_signals=%d baseline_signals=%d unknown_signals=%d", e.AppTaskResults.State, c.Increased, c.Unchanged, c.Baseline, c.Missing+c.Stale+c.Conflicting+c.Unavailable)
		}
		if e.AppTaskOperations != nil {
			c := e.AppTaskOperations.Counts
			_, _ = fmt.Fprintf(w, " app_task_operations_state=%s increased_signals=%d unchanged_signals=%d baseline_signals=%d unknown_signals=%d", e.AppTaskOperations.State, c.Increased, c.Unchanged, c.Baseline, c.Missing+c.Stale+c.Conflicting+c.Unavailable)
		}
		if e.PostCaptureHealth != nil {
			c := e.PostCaptureHealth.Counts
			_, _ = fmt.Fprintf(w, " post_capture_state=%s post_capture_nonempty_views=%d post_capture_empty_views=%d post_capture_unknown_age_views=%d post_capture_incomplete=%t", e.PostCaptureHealth.State, c.NonemptyViews, c.EmptyViews, c.UnknownAgeViews, e.PostCaptureHealth.Incomplete)
			for _, p := range e.PostCaptureHealth.Classes {
				_, _ = fmt.Fprintf(w, " %s_nonempty_views=%d %s_unknown_age_views=%d", p.Class, p.NonemptyViews, p.Class, p.UnknownAgeViews)
			}
		}
		if e.SnapshotPrimeHealth != nil {
			c := e.SnapshotPrimeHealth.Counts
			_, _ = fmt.Fprintf(w, " snapshot_prime_state=%s snapshot_prime_nonempty_scopes=%d snapshot_prime_empty_scopes=%d snapshot_prime_unknown_age_scopes=%d snapshot_prime_incomplete=%t", e.SnapshotPrimeHealth.State, c.NonemptyScopes, c.EmptyScopes, c.UnknownAgeScopes, e.SnapshotPrimeHealth.Incomplete)
			for _, p := range e.SnapshotPrimeHealth.Classes {
				_, _ = fmt.Fprintf(w, " %s_nonempty_scopes=%d %s_unknown_age_scopes=%d", p.Class, p.NonemptyScopes, p.Class, p.UnknownAgeScopes)
			}
		}
		if e.PreparationHealth != nil {
			c := e.PreparationHealth.Counts
			_, _ = fmt.Fprintf(w, " preparation_state=%s preparation_waiting_scopes=%d preparation_empty_scopes=%d preparation_unknown_age_scopes=%d preparation_incomplete=%t", e.PreparationHealth.State, c.WaitingScopes, c.EmptyScopes, c.UnknownAgeScopes, e.PreparationHealth.Incomplete)
			for _, p := range e.PreparationHealth.Phases {
				_, _ = fmt.Fprintf(w, " %s_waiting_scopes=%d %s_unknown_age_scopes=%d", p.Phase, p.WaitingScopes, p.Phase, p.UnknownAgeScopes)
			}
		}
		if e.CompactDetails != nil {
			_, _ = fmt.Fprint(w, " details_compacted=true")
		}
		if len(e.OmittedSections) > 0 {
			_, _ = fmt.Fprintf(w, " omitted_sections=%v", e.OmittedSections)
		}
		_, _ = fmt.Fprintln(w)
		for _, job := range e.Jobs {
			if job.State != "available" {
				_, _ = fmt.Fprintf(w, "    job=%s state=%s reason=%s\n", job.Job, job.State, job.Reason)
			}
		}
	}
}
