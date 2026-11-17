package operatorobs

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

func RuntimeBuildsWithLoopHealth(ctx context.Context, client *promql.Client, registry RuntimeBuildRegistry, selected RuntimeBuildSelection, now time.Time) RuntimeBuildsLoopResponse {
	ctx, cancel := context.WithTimeout(ctx, api.ObsRuntimeBuildBudget)
	defer cancel()
	base, results := collectRuntimeBuilds(ctx, client, registry, selected, now)
	out := RuntimeBuildsLoopResponse{ObsRuntimeBuilds: base, Diagnostics: CloneRuntimeDiagnostics(selected.diagnostics)}
	specs := runtimeBuildJobSpecs(selected.role)
	if len(results) == 0 || len(results) != len(specs) {
		return out
	}
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
			rows[i] = readRuntimeLoopsJob(ctx, client, spec, results[i], selected, now)
		})
		if spec.job == "apid" && runtimeBuildRecoverySelected(selected.role) && selected.diagnosticSelected("build_recovery_health") {
			wg.Go(func() {
				out.BuildRecoveryHealth = readRuntimeBuildRecoveryHealth(ctx, client, results[i], selected, now)
			})
		}
		if slices.Contains(runtimeSnapshotPrimeJobs(selected.role), spec.job) && selected.diagnosticSelected("snapshot_prime_health") {
			wg.Go(func() { snapshotRows[i] = readRuntimeSnapshotPrimeJob(ctx, client, results[i], selected, now) })
		}
		if spec.daemon == "builderd" && selected.diagnosticSelected("builder_health") {
			wg.Go(func() { out.BuilderHealth = readRuntimeBuilderHealth(ctx, client, results[i], selected, now) })
		}
		if slices.Contains(runtimeAppTaskJobs(selected.role), spec.job) {
			if selected.diagnosticSelected("command_cron_outcomes") {
				wg.Go(func() {
					// Existing loop evidence has priority in the shared source budget.
					// New advisory counter reads use only the remaining deadline.
					loopReads.Wait()
					cronOutcomeRows[i] = readRuntimeCommandCronOutcomesJob(ctx, client, results[i], selected, now)
				})
			}
			if selected.diagnosticSelected("command_cron_timing") {
				wg.Go(func() {
					loopReads.Wait()
					cronTimingRows[i] = readRuntimeCommandCronTimingJob(ctx, client, results[i], selected, now)
				})
			}
			if selected.diagnosticSelected("http_cron_delivery") {
				wg.Go(func() {
					loopReads.Wait()
					httpCronRows[i] = readRuntimeHTTPCronDeliveryJob(ctx, client, results[i], selected, now)
				})
			}
			if selected.diagnosticSelected("delayed_task_dispatch") {
				wg.Go(func() {
					loopReads.Wait()
					delayedDispatchRows[i] = readRuntimeDelayedTaskDispatchJob(ctx, client, results[i], selected, now)
				})
			}
			if selected.diagnosticSelected("app_task_results") {
				wg.Go(func() { resultRows[i] = readRuntimeAppTaskResultsJob(ctx, client, results[i], selected, now) })
			}
			if selected.diagnosticSelected("app_task_operations") {
				wg.Go(func() { operationRows[i] = readRuntimeAppTaskOperationsJob(ctx, client, results[i], selected, now) })
			}
			if selected.diagnosticSelected("app_task_health") {
				wg.Go(func() { appTaskRows[i] = readRuntimeAppTaskJob(ctx, client, results[i], selected, now) })
			}
		}
		if slices.Contains(runtimeCommandCronJobs(selected.role), spec.job) && selected.diagnosticSelected("command_cron_health") {
			wg.Go(func() { commandCronRows[i] = readRuntimeCommandCronJob(ctx, client, results[i], selected, now) })
		}
		if spec.daemon == "imaged" {
			if selected.diagnosticSelected("post_capture_health") {
				wg.Go(func() { postCaptureRows[i] = readRuntimePostCaptureJob(ctx, client, results[i], selected, now) })
			}
			if selected.diagnosticSelected("handoff_health") {
				wg.Go(func() { out.HandoffHealth = readRuntimeHandoffHealth(ctx, client, results[i], selected, now) })
			}
			if selected.diagnosticSelected("preparation_health") {
				wg.Go(func() { out.PreparationHealth = readRuntimePreparationHealth(ctx, client, results[i], selected, now) })
			}
		}
	}
	wg.Wait()
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
	return fitRuntimeLoopResponse(out)
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

func fitRuntimeLoopResponse(out RuntimeBuildsLoopResponse) RuntimeBuildsLoopResponse {
	for {
		body, err := json.MarshalIndent(out, "", "  ")
		if err != nil || len(body) < api.ObsRuntimeBuildResponseMaxBytes {
			return out
		}
		if out.DelayedTaskDispatch != nil && len(out.DelayedTaskDispatch.Items) > 0 {
			out.DelayedTaskDispatch.Items = out.DelayedTaskDispatch.Items[:len(out.DelayedTaskDispatch.Items)-1]
			out.DelayedTaskDispatch.OmittedItems++
			continue
		}
		if out.HTTPCronDelivery != nil && len(out.HTTPCronDelivery.Items) > 0 {
			out.HTTPCronDelivery.Items = out.HTTPCronDelivery.Items[:len(out.HTTPCronDelivery.Items)-1]
			out.HTTPCronDelivery.OmittedItems++
			continue
		}
		if out.CommandCronTiming != nil && len(out.CommandCronTiming.Items) > 0 {
			out.CommandCronTiming.Items = out.CommandCronTiming.Items[:len(out.CommandCronTiming.Items)-1]
			out.CommandCronTiming.OmittedItems++
			continue
		}
		if out.CommandCronOutcomes != nil && len(out.CommandCronOutcomes.Items) > 0 {
			out.CommandCronOutcomes.Items = out.CommandCronOutcomes.Items[:len(out.CommandCronOutcomes.Items)-1]
			out.CommandCronOutcomes.OmittedItems++
			continue
		}
		if out.AppTaskResults != nil && len(out.AppTaskResults.Items) > 0 {
			out.AppTaskResults.Items = out.AppTaskResults.Items[:len(out.AppTaskResults.Items)-1]
			out.AppTaskResults.OmittedItems++
			continue
		}
		if out.AppTaskOperations != nil && len(out.AppTaskOperations.Items) > 0 {
			out.AppTaskOperations.Items = out.AppTaskOperations.Items[:len(out.AppTaskOperations.Items)-1]
			out.AppTaskOperations.OmittedItems++
			continue
		}
		if out.CommandCronHealth != nil && len(out.CommandCronHealth.Items) > 0 {
			out.CommandCronHealth.Items = out.CommandCronHealth.Items[:len(out.CommandCronHealth.Items)-1]
			out.CommandCronHealth.OmittedItems++
			continue
		}
		if out.AppTaskHealth != nil && len(out.AppTaskHealth.Items) > 0 {
			out.AppTaskHealth.Items = out.AppTaskHealth.Items[:len(out.AppTaskHealth.Items)-1]
			out.AppTaskHealth.OmittedItems++
			continue
		}
		if out.BuildRecoveryHealth != nil && len(out.BuildRecoveryHealth.Items) > 0 {
			out.BuildRecoveryHealth.Items = out.BuildRecoveryHealth.Items[:len(out.BuildRecoveryHealth.Items)-1]
			out.BuildRecoveryHealth.OmittedItems++
			continue
		}

		if out.PostCaptureHealth != nil && len(out.PostCaptureHealth.Items) > 0 {
			out.PostCaptureHealth.Items = out.PostCaptureHealth.Items[:len(out.PostCaptureHealth.Items)-1]
			out.PostCaptureHealth.OmittedItems++
			continue
		}
		if out.SnapshotPrimeHealth != nil && len(out.SnapshotPrimeHealth.Items) > 0 {
			out.SnapshotPrimeHealth.Items = out.SnapshotPrimeHealth.Items[:len(out.SnapshotPrimeHealth.Items)-1]
			out.SnapshotPrimeHealth.OmittedItems++
			continue
		}
		if out.PreparationHealth != nil && len(out.PreparationHealth.Items) > 0 {
			out.PreparationHealth.Items = out.PreparationHealth.Items[:len(out.PreparationHealth.Items)-1]
			out.PreparationHealth.OmittedItems++
			continue
		}
		if out.HandoffHealth != nil && len(out.HandoffHealth.Items) > 0 {
			out.HandoffHealth.Items = out.HandoffHealth.Items[:len(out.HandoffHealth.Items)-1]
			out.HandoffHealth.OmittedItems++
			continue
		}
		if out.BuilderHealth != nil && len(out.BuilderHealth.Items) > 0 {
			out.BuilderHealth.Items = out.BuilderHealth.Items[:len(out.BuilderHealth.Items)-1]
			out.BuilderHealth.OmittedItems++
			continue
		}
		if out.LoopHealth != nil && len(out.LoopHealth.Items) > 0 {
			out.LoopHealth.Items = out.LoopHealth.Items[:len(out.LoopHealth.Items)-1]
			out.LoopHealth.OmittedItems++
			continue
		}
		if len(out.Items) > 0 {
			out.Items = out.Items[:len(out.Items)-1]
			out.OmittedItems++
			continue
		}
		return out
	}
}
