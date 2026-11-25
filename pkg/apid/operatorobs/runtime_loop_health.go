package operatorobs

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

// runtimeLoopHealthBudget bounds one collection. Only tests that check
// coverage rather than timing lengthen it, so a loaded CI runner does not
// read as an exhausted budget.
var runtimeLoopHealthBudget = api.ObsRuntimeBuildBudget

func RuntimeBuildsWithLoopHealth(ctx context.Context, client *promql.Client, registry RuntimeBuildRegistry, selected RuntimeBuildSelection, now time.Time) (out RuntimeBuildsLoopResponse) {
	ctx, cancel := context.WithTimeoutCause(ctx, runtimeLoopHealthBudget, errRuntimeCollectionBudget)
	defer cancel()
	observation := runtimeObservation(ctx)
	if observation.metrics != nil {
		started := time.Now()
		defer func() { observation.metrics.recordReport(ctx, out, started) }()
	}
	base, results := collectRuntimeBuilds(ctx, client, registry, selected, now)
	out = RuntimeBuildsLoopResponse{ObsRuntimeBuilds: base, Diagnostics: CloneRuntimeDiagnostics(selected.diagnostics)}
	specs := runtimeBuildJobSpecs(selected.role)
	if len(results) == 0 || len(results) != len(specs) {
		return out
	}
	diagnosticsStarted := time.Now()
	rows := make([][]RuntimeLoopItem, len(specs))
	snapshotRows := make([][]RuntimeSnapshotPrimeItem, len(specs))
	appTaskRows := make([][]RuntimeAppTaskItem, len(specs))
	commandCronRows := make([][]RuntimeCommandCronItem, len(specs))
	delayedDispatchRows := make([][]RuntimeDelayedTaskDispatchItem, len(specs))
	httpCronRows := make([][]RuntimeHTTPCronDeliveryItem, len(specs))
	cronTimingRows := make([][]RuntimeCommandCronTimingItem, len(specs))
	cronOutcomeRows := make([][]RuntimeCommandCronOutcomeItem, len(specs))
	resultRows := make([][]RuntimeAppTaskResultItem, len(specs))
	operationRows := make([][]RuntimeAppTaskOperationItem, len(specs))
	postCaptureRows := make([][]RuntimePostCaptureItem, len(specs))
	var wg sync.WaitGroup
	var loopReads sync.WaitGroup
	loopReads.Add(len(specs))
	for i, spec := range specs {
		wg.Go(func() {
			defer loopReads.Done()
			rows[i] = readRuntimeLoopsJob(runtimeQueryContext(ctx, "loop_health"), client, spec, results[i], selected, now)
		})
		if spec.job == "apid" && runtimeBuildRecoverySelected(selected.role) && selected.diagnosticSelected("build_recovery_health") {
			wg.Go(func() {
				out.BuildRecoveryHealth = readRuntimeBuildRecoveryHealth(runtimeQueryContext(ctx, "build_recovery_health"), client, results[i], selected, now)
			})
		}
		if slices.Contains(runtimeSnapshotPrimeJobs(selected.role), spec.job) && selected.diagnosticSelected("snapshot_prime_health") {
			wg.Go(func() {
				snapshotRows[i] = readRuntimeSnapshotPrimeJob(runtimeQueryContext(ctx, "snapshot_prime_health"), client, results[i], selected, now)
			})
		}
		if spec.daemon == "builderd" && selected.diagnosticSelected("builder_health") {
			wg.Go(func() {
				out.BuilderHealth = readRuntimeBuilderHealth(runtimeQueryContext(ctx, "builder_health"), client, results[i], selected, now)
			})
		}
		if slices.Contains(runtimeAppTaskJobs(selected.role), spec.job) {
			if selected.diagnosticSelected("command_cron_outcomes") {
				wg.Go(func() {
					// Existing loop evidence has priority in the shared source budget.
					// New advisory counter reads use only the remaining deadline.
					loopReads.Wait()
					cronOutcomeRows[i] = readRuntimeCommandCronOutcomesJob(runtimeQueryContext(ctx, "command_cron_outcomes"), client, results[i], selected, now)
				})
			}
			if selected.diagnosticSelected("command_cron_timing") {
				wg.Go(func() {
					loopReads.Wait()
					cronTimingRows[i] = readRuntimeCommandCronTimingJob(runtimeQueryContext(ctx, "command_cron_timing"), client, results[i], selected, now)
				})
			}
			if selected.diagnosticSelected("http_cron_delivery") {
				wg.Go(func() {
					loopReads.Wait()
					httpCronRows[i] = readRuntimeHTTPCronDeliveryJob(runtimeQueryContext(ctx, "http_cron_delivery"), client, results[i], selected, now)
				})
			}
			if selected.diagnosticSelected("delayed_task_dispatch") {
				wg.Go(func() {
					loopReads.Wait()
					delayedDispatchRows[i] = readRuntimeDelayedTaskDispatchJob(runtimeQueryContext(ctx, "delayed_task_dispatch"), client, results[i], selected, now)
				})
			}
			if selected.diagnosticSelected("app_task_results") {
				wg.Go(func() {
					resultRows[i] = readRuntimeAppTaskResultsJob(runtimeQueryContext(ctx, "app_task_results"), client, results[i], selected, now)
				})
			}
			if selected.diagnosticSelected("app_task_operations") {
				wg.Go(func() {
					operationRows[i] = readRuntimeAppTaskOperationsJob(runtimeQueryContext(ctx, "app_task_operations"), client, results[i], selected, now)
				})
			}
			if selected.diagnosticSelected("app_task_health") {
				wg.Go(func() {
					appTaskRows[i] = readRuntimeAppTaskJob(runtimeQueryContext(ctx, "app_task_health"), client, results[i], selected, now)
				})
			}
		}
		if slices.Contains(runtimeCommandCronJobs(selected.role), spec.job) && selected.diagnosticSelected("command_cron_health") {
			wg.Go(func() {
				commandCronRows[i] = readRuntimeCommandCronJob(runtimeQueryContext(ctx, "command_cron_health"), client, results[i], selected, now)
			})
		}
		if spec.daemon == "imaged" {
			if selected.diagnosticSelected("post_capture_health") {
				wg.Go(func() {
					postCaptureRows[i] = readRuntimePostCaptureJob(runtimeQueryContext(ctx, "post_capture_health"), client, results[i], selected, now)
				})
			}
			if selected.diagnosticSelected("handoff_health") {
				wg.Go(func() {
					out.HandoffHealth = readRuntimeHandoffHealth(runtimeQueryContext(ctx, "handoff_health"), client, results[i], selected, now)
				})
			}
			if selected.diagnosticSelected("preparation_health") {
				wg.Go(func() {
					out.PreparationHealth = readRuntimePreparationHealth(runtimeQueryContext(ctx, "preparation_health"), client, results[i], selected, now)
				})
			}
		}
	}
	wg.Wait()
	observation.observePhase(ctx, runtimePhaseAdvisoryReads, diagnosticsStarted)
	assemblyStarted := time.Now()
	if selected.diagnosticSelected("snapshot_prime_health") {
		out.SnapshotPrimeHealth = aggregateRuntimeSnapshotPrime(base, snapshotRows)
	}
	if selected.diagnosticSelected("app_task_health") {
		out.AppTaskHealth = aggregateRuntimeAppTask(base, appTaskRows)
	}
	if selected.diagnosticSelected("command_cron_health") {
		out.CommandCronHealth = aggregateRuntimeCommandCron(base, commandCronRows)
	}
	if selected.diagnosticSelected("command_cron_outcomes") {
		out.CommandCronOutcomes = aggregateRuntimeCommandCronOutcomes(base, cronOutcomeRows)
	}
	if selected.diagnosticSelected("http_cron_delivery") {
		out.HTTPCronDelivery = aggregateRuntimeHTTPCronDelivery(base, httpCronRows)
	}
	if selected.diagnosticSelected("delayed_task_dispatch") {
		out.DelayedTaskDispatch = aggregateRuntimeDelayedTaskDispatch(base, delayedDispatchRows)
	}
	if selected.diagnosticSelected("command_cron_timing") {
		out.CommandCronTiming = aggregateRuntimeCommandCronTiming(base, cronTimingRows)
	}
	if selected.diagnosticSelected("app_task_results") {
		out.AppTaskResults = aggregateRuntimeAppTaskResults(base, resultRows)
	}
	if selected.diagnosticSelected("app_task_operations") {
		out.AppTaskOperations = aggregateRuntimeAppTaskOperations(base, operationRows)
	}
	if selected.diagnosticSelected("post_capture_health") {
		out.PostCaptureHealth = aggregateRuntimePostCapture(base, postCaptureRows)
	}
	health := &RuntimeLoopHealth{Items: []RuntimeLoopItem{}}
	for i, job := range base.Jobs {
		health.Counts.Expected += job.Expected * len(runtimeLoopsForDaemon(job.Daemon))
		health.Incomplete = health.Incomplete || job.State != "available"
		for _, item := range rows[i] {
			accountRuntimeLoopItem(health, item)
		}
		health.Items = append(health.Items, rows[i]...)
	}
	health.Incomplete = health.Incomplete || health.Counts.Missing+health.Counts.Stale+health.Counts.Conflicting+health.Counts.Unavailable+health.Counts.Unexpected > 0
	health.State, health.Reason = runtimeLoopState(health.Counts, health.Incomplete)
	slices.SortStableFunc(health.Items, func(a, b RuntimeLoopItem) int { return runtimeLoopPriority(a) - runtimeLoopPriority(b) })
	if len(health.Items) > api.ObsRuntimeBuildPreviewMax {
		health.OmittedItems = len(health.Items) - api.ObsRuntimeBuildPreviewMax
		health.Items = health.Items[:api.ObsRuntimeBuildPreviewMax]
	}
	out.LoopHealth = health
	observation.observePhase(ctx, runtimePhaseAssembly, assemblyStarted)
	previewStarted := time.Now()
	out = fitRuntimeLoopResponse(out)
	observation.observePhase(ctx, runtimePhasePreview, previewStarted)
	return out
}

func readRuntimeLoopsJob(ctx context.Context, client *promql.Client, spec runtimeBuildJobSpec, result runtimeBuildJobResult, selected RuntimeBuildSelection, now time.Time) []RuntimeLoopItem {
	out := []RuntimeLoopItem{}
	keys := map[string]string{}
	if result.samples != nil {
		for key := range result.samples.up {
			keys[fmt.Sprintf("%x", sha256.Sum256([]byte(key)))] = key
		}
	}
	for _, loop := range runtimeLoopsForDaemon(spec.daemon) {
		var stall, age runtimeLoopPair
		if result.samples != nil && len(keys) > 0 {
			stall = readRuntimeLoopPair(ctx, client, spec, loop, "stalled", *result.samples, now)
			age = readRuntimeLoopPair(ctx, client, spec, loop, "last_beat_age_seconds", *result.samples, now)
		}
		for _, item := range result.items {
			out = append(out, assessRuntimeLoopItem(item, loop, keys[item.TargetID], stall, age, result.samples, selected, now))
		}
	}
	return out
}

func accountRuntimeLoopItem(out *RuntimeLoopHealth, item RuntimeLoopItem) {
	c := &out.Counts
	if item.TargetID != "" {
		c.Observed++
	}
	if item.Unexpected {
		c.Unexpected++
	}
	if item.ReportedStalled != nil && *item.ReportedStalled {
		c.ReportedStalls++
	}
	switch item.State {
	case "no_reported_stall":
		c.NoReportedStall++
	case "stalled":
		c.Stalled++
	case "missing":
		c.Missing++
	case "stale":
		c.Stale++
	case "conflicting":
		c.Conflicting++
	default:
		c.Unavailable++
	}
}

func runtimeLoopState(c RuntimeLoopCounts, incomplete bool) (string, string) {
	switch {
	case c.ReportedStalls > 0:
		return "stalled", "reported_loop_stall"
	case incomplete:
		return "incomplete", "incomplete_loop_evidence"
	case c.NoReportedStall > 0:
		return "no_reported_stall", ""
	default:
		return "unknown", "loop_evidence_unavailable"
	}
}

func runtimeLoopPriority(item RuntimeLoopItem) int {
	if item.ReportedStalled != nil && *item.ReportedStalled {
		return 0
	}
	if item.State != "no_reported_stall" {
		return 1
	}
	return 2
}
