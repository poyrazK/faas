package operatorobs

import (
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

type runtimeReportGroup struct {
	name       string
	incomplete bool
}

// Record omitted previews and assess each evidence group using full-set
// summaries. Optional summaries are present only for the selected diagnostics
// supported by the collection profile.
func (m *RuntimeDiagnosticsMetrics) recordReportGroups(out RuntimeBuildsLoopResponse) (groups []runtimeReportGroup, incomplete bool) {
	record := func(group string, partial bool, omitted int) {
		groups = append(groups, runtimeReportGroup{name: group, incomplete: partial})
		incomplete = incomplete || partial
		if omitted > 0 {
			m.omitted.WithLabelValues(group).Add(float64(omitted))
		}
	}
	c := out.Counts
	identityIncomplete := c.Missing+c.Stale+c.Unavailable+c.Unexpected > 0 || runtimeEvidenceJobsUnavailable(out.Jobs)
	record("identity", identityIncomplete, out.OmittedItems)
	if len(out.Jobs) > 0 {
		readinessIncomplete := true
		if out.Readiness != nil {
			r := out.Readiness.Counts
			readinessIncomplete = r.Missing+r.Stale+r.Conflicting+r.Unavailable > 0
		}
		record("readiness", readinessIncomplete, 0)
		record("process", out.Process == nil || out.Process.Incomplete, 0)
	}
	if out.DelayedTaskDispatch != nil {
		record("delayed_task_dispatch", out.DelayedTaskDispatch.Incomplete, out.DelayedTaskDispatch.OmittedItems)
	}
	if out.HTTPCronDelivery != nil {
		record("http_cron_delivery", out.HTTPCronDelivery.Incomplete, out.HTTPCronDelivery.OmittedItems)
	}
	if out.CommandCronTiming != nil {
		record("command_cron_timing", out.CommandCronTiming.Incomplete, out.CommandCronTiming.OmittedItems)
	}
	if out.CommandCronOutcomes != nil {
		record("command_cron_outcomes", out.CommandCronOutcomes.Incomplete, out.CommandCronOutcomes.OmittedItems)
	}
	if out.AppTaskResults != nil {
		record("app_task_results", out.AppTaskResults.Incomplete, out.AppTaskResults.OmittedItems)
	}
	if out.AppTaskOperations != nil {
		record("app_task_operations", out.AppTaskOperations.Incomplete, out.AppTaskOperations.OmittedItems)
	}
	if out.CommandCronHealth != nil {
		record("command_cron_health", out.CommandCronHealth.Incomplete, out.CommandCronHealth.OmittedItems)
	}
	if out.AppTaskHealth != nil {
		record("app_task_health", out.AppTaskHealth.Incomplete, out.AppTaskHealth.OmittedItems)
	}
	if out.BuildRecoveryHealth != nil {
		record("build_recovery_health", out.BuildRecoveryHealth.Incomplete, out.BuildRecoveryHealth.OmittedItems)
	}
	if out.PostCaptureHealth != nil {
		record("post_capture_health", out.PostCaptureHealth.Incomplete, out.PostCaptureHealth.OmittedItems)
	}
	if out.SnapshotPrimeHealth != nil {
		record("snapshot_prime_health", out.SnapshotPrimeHealth.Incomplete, out.SnapshotPrimeHealth.OmittedItems)
	}
	if out.PreparationHealth != nil {
		record("preparation_health", out.PreparationHealth.Incomplete, out.PreparationHealth.OmittedItems)
	}
	if out.HandoffHealth != nil {
		record("handoff_health", out.HandoffHealth.Incomplete, out.HandoffHealth.OmittedItems)
	}
	if out.BuilderHealth != nil {
		record("builder_health", out.BuilderHealth.Incomplete, out.BuilderHealth.OmittedItems)
	}
	if out.LoopHealth != nil {
		record("loop_health", out.LoopHealth.Incomplete, out.LoopHealth.OmittedItems)
	}
	return groups, incomplete
}

type runtimeCoreEvidenceGap struct {
	reason  string
	present bool
}

func runtimeEvidenceJobsUnavailable(jobs []api.ObsRuntimeBuildJob) bool {
	return slices.ContainsFunc(jobs, func(job api.ObsRuntimeBuildJob) bool { return job.State != "available" })
}

// List the fixed reasons for the group and flag those found in the full-set
// summaries. The boolean collapses any number of affected targets to one event.
func runtimeCoreEvidenceGaps(group string, out RuntimeBuildsLoopResponse) []runtimeCoreEvidenceGap {
	switch group {
	case "identity":
		c := out.Counts
		return []runtimeCoreEvidenceGap{
			{"missing", c.Missing > 0},
			{"stale", c.Stale > 0},
			{"unavailable", c.Unavailable > 0 || runtimeEvidenceJobsUnavailable(out.Jobs)},
			{"unexpected", c.Unexpected > 0},
		}
	case "readiness":
		var c api.ObsRuntimeReadinessCounts
		if out.Readiness != nil {
			c = out.Readiness.Counts
		}
		return []runtimeCoreEvidenceGap{
			{"missing", c.Missing > 0},
			{"stale", c.Stale > 0},
			{"conflicting", c.Conflicting > 0},
			{"unavailable", c.Unavailable > 0 || out.Readiness == nil},
		}
	case "process":
		var c api.ObsRuntimeProcessCounts
		if out.Process != nil {
			c = out.Process.Counts
		}
		return []runtimeCoreEvidenceGap{
			{"missing", c.Missing > 0},
			{"stale", c.Stale > 0},
			{"conflicting", c.Conflicting > 0},
			{"unavailable", c.Unavailable > 0 || out.Process == nil || runtimeEvidenceJobsUnavailable(out.Jobs)},
			{"unexpected", out.Counts.Unexpected > 0},
			{"target_set_changed", c.NewTargets > 0 || c.RemovedTargets > 0},
			{"repeated_samples", c.RepeatedSamples > 0},
			{"comparison_unavailable", c.ComparisonUnavailable > 0},
		}
	case "loop_health":
		var c RuntimeLoopCounts
		if out.LoopHealth != nil {
			c = out.LoopHealth.Counts
		}
		return []runtimeCoreEvidenceGap{
			{"missing", c.Missing > 0},
			{"stale", c.Stale > 0},
			{"conflicting", c.Conflicting > 0},
			{"unavailable", c.Unavailable > 0 || runtimeEvidenceJobsUnavailable(out.Jobs)},
			{"unexpected", c.Unexpected > 0},
		}
	default:
		return nil
	}
}

func (m *RuntimeDiagnosticsMetrics) recordCoreEvidenceGaps(group string, out RuntimeBuildsLoopResponse) {
	gaps := runtimeCoreEvidenceGaps(group, out)
	if len(gaps) == 0 {
		return
	}
	recorded := false
	for _, gap := range gaps {
		if gap.present {
			m.coreGaps.WithLabelValues(group, gap.reason).Inc()
			recorded = true
		}
	}
	if !recorded {
		// An incomplete summary can lack a more specific breakdown.
		m.coreGaps.WithLabelValues(group, "unavailable").Inc()
	}
}
