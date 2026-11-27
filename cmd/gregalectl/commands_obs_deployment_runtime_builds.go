package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

type obsDeploymentRuntimeBuilds struct {
	Diagnostics                             *operatorobs.RuntimeDiagnostics         `json:"diagnostics,omitempty"`
	DiagnosticsSource                       string                                  `json:"diagnostics_source,omitempty"`
	BuildRecoveryHealth                     *operatorobs.RuntimeBuildRecoveryHealth `json:"build_recovery_health,omitempty"`
	AppTaskHealth                           *operatorobs.RuntimeAppTaskHealth       `json:"app_task_health,omitempty"`
	CommandCronHealth                       *operatorobs.RuntimeCommandCronHealth   `json:"command_cron_health,omitempty"`
	CommandCronOutcomes                     *operatorobs.RuntimeCommandCronOutcomes `json:"command_cron_outcomes,omitempty"`
	HTTPCronDelivery                        *operatorobs.RuntimeHTTPCronDelivery    `json:"http_cron_delivery,omitempty"`
	DelayedTaskDispatch                     *operatorobs.RuntimeDelayedTaskDispatch `json:"delayed_task_dispatch,omitempty"`
	CommandCronTiming                       *operatorobs.RuntimeCommandCronTiming   `json:"command_cron_timing,omitempty"`
	AppTaskResults                          *operatorobs.RuntimeAppTaskResults      `json:"app_task_results,omitempty"`
	AppTaskOperations                       *operatorobs.RuntimeAppTaskOperations   `json:"app_task_operations,omitempty"`
	PostCaptureHealth                       *operatorobs.RuntimePostCaptureHealth   `json:"post_capture_health,omitempty"`
	SnapshotPrimeHealth                     *operatorobs.RuntimeSnapshotPrimeHealth `json:"snapshot_prime_health,omitempty"`
	PreparationHealth                       *operatorobs.RuntimePreparationHealth   `json:"preparation_health,omitempty"`
	BuildRecoveryEnvelopeOmittedItems       int                                     `json:"build_recovery_envelope_omitted_items,omitempty"`
	AppTaskEnvelopeOmittedItems             int                                     `json:"app_task_envelope_omitted_items,omitempty"`
	CommandCronEnvelopeOmittedItems         int                                     `json:"command_cron_envelope_omitted_items,omitempty"`
	CommandCronOutcomesEnvelopeOmittedItems int                                     `json:"command_cron_outcomes_envelope_omitted_items,omitempty"`
	HTTPCronDeliveryEnvelopeOmittedItems    int                                     `json:"http_cron_delivery_envelope_omitted_items,omitempty"`
	DelayedTaskDispatchEnvelopeOmittedItems int                                     `json:"delayed_task_dispatch_envelope_omitted_items,omitempty"`
	CommandCronTimingEnvelopeOmittedItems   int                                     `json:"command_cron_timing_envelope_omitted_items,omitempty"`
	AppTaskResultsEnvelopeOmittedItems      int                                     `json:"app_task_results_envelope_omitted_items,omitempty"`
	AppTaskOperationsEnvelopeOmittedItems   int                                     `json:"app_task_operations_envelope_omitted_items,omitempty"`
	PostCaptureEnvelopeOmittedItems         int                                     `json:"post_capture_envelope_omitted_items,omitempty"`
	SnapshotPrimeEnvelopeOmittedItems       int                                     `json:"snapshot_prime_envelope_omitted_items,omitempty"`
	PreparationEnvelopeOmittedItems         int                                     `json:"preparation_envelope_omitted_items,omitempty"`
	HandoffHealth                           *operatorobs.RuntimeHandoffHealth       `json:"handoff_health,omitempty"`
	HandoffEnvelopeOmittedItems             int                                     `json:"handoff_envelope_omitted_items,omitempty"`
	pipelineSelected                        bool
	BuilderHealth                           *operatorobs.RuntimeBuilderHealth `json:"builder_health,omitempty"`
	BuilderEnvelopeOmittedItems             int                               `json:"builder_envelope_omitted_items,omitempty"`
	LoopHealth                              *operatorobs.RuntimeLoopHealth    `json:"loop_health,omitempty"`
	LoopEnvelopeOmittedItems                int                               `json:"loop_envelope_omitted_items,omitempty"`
	Advisory                                bool                              `json:"advisory"`
	State                                   string                            `json:"state"`
	Reason                                  string                            `json:"reason,omitempty"`
	SourceHTTPStatus                        *int                              `json:"source_http_status,omitempty"`
	Evidence                                *api.ObsRuntimeBuilds             `json:"evidence,omitempty"`
	EnvelopeOmittedItems                    int                               `json:"envelope_omitted_items,omitempty"`
	Progress                                *obsDeploymentBuildProgress       `json:"progress,omitempty"`
	History                                 *obsDeploymentBuildHistory        `json:"history,omitempty"`
}

func initialObsDeploymentRuntimeBuilds(role string) *obsDeploymentRuntimeBuilds {
	reason := "expected_role_not_selected"
	if role != "" {
		reason = "awaiting_evidence"
	}
	return &obsDeploymentRuntimeBuilds{pipelineSelected: role == "single-box-v2" || role == "control-plane-v2", Advisory: true, State: "unknown", Reason: reason}
}

func fetchObsDeploymentWithBuilds(ctx context.Context, client *http.Client, cred obsCredential, opts obsDeploymentOptions) obsDeploymentSnapshot {
	if opts.expectedRole == "" {
		return fetchObsDeploymentSnapshot(ctx, client, cred)
	}
	type buildRead struct {
		diagnostics            *operatorobs.RuntimeDiagnostics
		diagnosticsUnsupported bool
		diagnosticsApplied     bool
		value                  api.ObsRuntimeBuilds
		loops                  *operatorobs.RuntimeLoopHealth
		builder                *operatorobs.RuntimeBuilderHealth
		handoff                *operatorobs.RuntimeHandoffHealth
		buildRecovery          *operatorobs.RuntimeBuildRecoveryHealth
		appTask                *operatorobs.RuntimeAppTaskHealth
		commandCron            *operatorobs.RuntimeCommandCronHealth
		commandCronOutcomes    *operatorobs.RuntimeCommandCronOutcomes
		httpCronDelivery       *operatorobs.RuntimeHTTPCronDelivery
		delayedTaskDispatch    *operatorobs.RuntimeDelayedTaskDispatch
		commandCronTiming      *operatorobs.RuntimeCommandCronTiming
		appTaskResults         *operatorobs.RuntimeAppTaskResults
		appTaskOperations      *operatorobs.RuntimeAppTaskOperations
		postCapture            *operatorobs.RuntimePostCaptureHealth
		snapshotPrime          *operatorobs.RuntimeSnapshotPrimeHealth
		preparation            *operatorobs.RuntimePreparationHealth
		err                    error
	}
	builds := make(chan buildRead, 1)
	go func() {
		q := url.Values{"expected_role": {opts.expectedRole}, "release": {opts.release}, "after": {opts.after.Format(time.RFC3339Nano)}}
		if !opts.processCompareAt.IsZero() {
			q.Set("compare_at", opts.processCompareAt.Format(time.RFC3339Nano))
		}
		if opts.runtimeDiagnostics != nil && !opts.runtimeDiagnosticsUnsupported {
			q.Set("diagnostics", opts.runtimeDiagnostics.QueryValue())
		}
		var value obsDeploymentRuntimeBuildResponse
		err := fetchObsBundleSection(ctx, client, apidBase()+operatorobs.RuntimeBuildsPath+"?"+q.Encode(), cred, "runtime-builds", &value)
		unsupported := opts.runtimeDiagnosticsUnsupported
		if err != nil && q.Has("diagnostics") {
			_, status := obsFetchFailureDetails(err)
			if status != nil && *status == http.StatusBadRequest {
				unsupported = true
				q.Del("diagnostics")
				value = obsDeploymentRuntimeBuildResponse{}
				err = fetchObsBundleSection(ctx, client, apidBase()+operatorobs.RuntimeBuildsPath+"?"+q.Encode(), cred, "runtime-builds", &value)
			}
		}
		applied := q.Has("diagnostics") && operatorobs.RuntimeDiagnosticsValid(opts.expectedRole, value.Diagnostics) && operatorobs.RuntimeDiagnosticsEqual(opts.runtimeDiagnostics, value.Diagnostics)
		builds <- buildRead{diagnostics: value.Diagnostics, diagnosticsUnsupported: unsupported, diagnosticsApplied: applied, value: value.ObsRuntimeBuilds, loops: value.LoopHealth, builder: value.BuilderHealth, handoff: value.HandoffHealth, preparation: value.PreparationHealth, snapshotPrime: value.SnapshotPrimeHealth, appTask: value.AppTaskHealth, commandCron: value.CommandCronHealth, appTaskOperations: value.AppTaskOperations, appTaskResults: value.AppTaskResults, commandCronOutcomes: value.CommandCronOutcomes, httpCronDelivery: value.HTTPCronDelivery, delayedTaskDispatch: value.DelayedTaskDispatch, commandCronTiming: value.CommandCronTiming, postCapture: value.PostCaptureHealth, buildRecovery: value.BuildRecoveryHealth, err: err}
	}()
	snapshot := fetchObsDeploymentSnapshot(ctx, client, cred)
	read := <-builds
	snapshot.runtimeDiagnostics = read.diagnostics
	snapshot.runtimeDiagnosticsUnsupported = read.diagnosticsUnsupported
	snapshot.runtimeDiagnosticsApplied = read.diagnosticsApplied
	snapshot.runtimeBuilds, snapshot.runtimeBuildErr = &read.value, read.err
	snapshot.runtimeProcessCompareAt = opts.processCompareAt
	snapshot.runtimeLoopHealth = read.loops
	snapshot.runtimeBuilderHealth = read.builder
	snapshot.runtimeHandoffHealth = read.handoff
	snapshot.runtimeBuildRecoveryHealth = read.buildRecovery
	snapshot.runtimeAppTaskHealth = read.appTask
	snapshot.runtimeCommandCronHealth = read.commandCron
	snapshot.runtimeCommandCronOutcomes = read.commandCronOutcomes
	snapshot.runtimeHTTPCronDelivery = read.httpCronDelivery
	snapshot.runtimeDelayedTaskDispatch = read.delayedTaskDispatch
	snapshot.runtimeCommandCronTiming = read.commandCronTiming
	snapshot.runtimeAppTaskResults = read.appTaskResults
	snapshot.runtimeAppTaskOperations = read.appTaskOperations
	snapshot.runtimePostCaptureHealth = read.postCapture
	snapshot.runtimeSnapshotPrimeHealth = read.snapshotPrime
	snapshot.runtimePreparationHealth = read.preparation
	return snapshot
}

func assessObsDeploymentRuntimeBuilds(snapshot obsDeploymentSnapshot, opts obsDeploymentOptions, now time.Time) *obsDeploymentRuntimeBuilds {
	out := initialObsDeploymentRuntimeBuilds(opts.expectedRole)
	applyObsDeploymentDiagnostics(out, opts.runtimeDiagnostics, "client")
	if snapshot.runtimeDiagnosticsApplied {
		applyObsDeploymentDiagnostics(out, opts.runtimeDiagnostics, "server")
	}
	if opts.expectedRole == "" {
		return out
	}
	if snapshot.runtimeBuildErr != nil {
		reason, status := obsFetchFailureDetails(snapshot.runtimeBuildErr)
		out.Reason, out.SourceHTTPStatus = "source_"+reason, status
		return out
	}
	value := snapshot.runtimeBuilds
	if value != nil {
		sanitized := sanitizeObsDeploymentProcess(*value, snapshot.runtimeProcessCompareAt)
		sanitized = operatorobs.SanitizeRuntimeReadiness(sanitized)
		value = &sanitized
	}
	if value == nil || !operatorobs.RuntimeBuildsValid(*value) || value.ExpectedRole != opts.expectedRole || value.RequestedRelease != opts.release || !value.EvidenceAfter.Equal(opts.after) {
		out.Reason = "source_invalid_response"
		return out
	}
	if value.GeneratedAt.After(now.Add(api.ObsHealthClockSkewTolerance)) || value.GeneratedAt.Before(now.Add(-api.ObsInputHealthMaxAge)) || value.GeneratedAt.Before(opts.after.Add(-api.ObsHealthClockSkewTolerance)) {
		out.Reason = "source_stale"
		return out
	}
	// The snapshot is owned by the reader. Clone before saving, including clocks,
	// so later fixture/poll mutations cannot rewrite already retained evidence.
	body, _ := json.Marshal(value)
	var owned api.ObsRuntimeBuilds
	_ = json.Unmarshal(body, &owned)
	out.State, out.Reason, out.Evidence = owned.State, owned.Reason, &owned
	if operatorobs.RuntimeLoopHealthValid(owned, snapshot.runtimeLoopHealth) {
		out.LoopHealth = operatorobs.CloneRuntimeLoopHealth(snapshot.runtimeLoopHealth)
	}
	if operatorobs.RuntimeBuilderHealthValid(owned, snapshot.runtimeBuilderHealth) {
		out.BuilderHealth = operatorobs.CloneRuntimeBuilderHealth(snapshot.runtimeBuilderHealth)
	}
	if operatorobs.RuntimeHandoffHealthValid(owned, snapshot.runtimeHandoffHealth) {
		out.HandoffHealth = operatorobs.CloneRuntimeHandoffHealth(snapshot.runtimeHandoffHealth)
	}
	if operatorobs.RuntimeBuildRecoveryHealthValid(owned, snapshot.runtimeBuildRecoveryHealth) {
		out.BuildRecoveryHealth = operatorobs.CloneRuntimeBuildRecoveryHealth(snapshot.runtimeBuildRecoveryHealth)
	}
	if operatorobs.RuntimeAppTaskHealthValid(owned, snapshot.runtimeAppTaskHealth) {
		out.AppTaskHealth = operatorobs.CloneRuntimeAppTaskHealth(snapshot.runtimeAppTaskHealth)
	}
	if operatorobs.RuntimeCommandCronHealthValid(owned, snapshot.runtimeCommandCronHealth) {
		out.CommandCronHealth = operatorobs.CloneRuntimeCommandCronHealth(snapshot.runtimeCommandCronHealth)
	}
	if operatorobs.RuntimeCommandCronOutcomesValid(owned, snapshot.runtimeCommandCronOutcomes) {
		out.CommandCronOutcomes = operatorobs.CloneRuntimeCommandCronOutcomes(snapshot.runtimeCommandCronOutcomes)
	}
	if operatorobs.RuntimeHTTPCronDeliveryValid(owned, snapshot.runtimeHTTPCronDelivery) {
		out.HTTPCronDelivery = operatorobs.CloneRuntimeHTTPCronDelivery(snapshot.runtimeHTTPCronDelivery)
	}
	if operatorobs.RuntimeDelayedTaskDispatchValid(owned, snapshot.runtimeDelayedTaskDispatch) {
		out.DelayedTaskDispatch = operatorobs.CloneRuntimeDelayedTaskDispatch(snapshot.runtimeDelayedTaskDispatch)
	}
	if operatorobs.RuntimeCommandCronTimingValid(owned, snapshot.runtimeCommandCronTiming) {
		out.CommandCronTiming = operatorobs.CloneRuntimeCommandCronTiming(snapshot.runtimeCommandCronTiming)
	}
	if operatorobs.RuntimeAppTaskResultsValid(owned, snapshot.runtimeAppTaskResults) {
		out.AppTaskResults = operatorobs.CloneRuntimeAppTaskResults(snapshot.runtimeAppTaskResults)
	}
	if operatorobs.RuntimeAppTaskOperationsValid(owned, snapshot.runtimeAppTaskOperations) {
		out.AppTaskOperations = operatorobs.CloneRuntimeAppTaskOperations(snapshot.runtimeAppTaskOperations)
	}
	if operatorobs.RuntimePostCaptureHealthValid(owned, snapshot.runtimePostCaptureHealth) {
		out.PostCaptureHealth = operatorobs.CloneRuntimePostCaptureHealth(snapshot.runtimePostCaptureHealth)
	}
	if operatorobs.RuntimeSnapshotPrimeHealthValid(owned, snapshot.runtimeSnapshotPrimeHealth) {
		out.SnapshotPrimeHealth = operatorobs.CloneRuntimeSnapshotPrimeHealth(snapshot.runtimeSnapshotPrimeHealth)
	}
	if operatorobs.RuntimePreparationHealthValid(owned, snapshot.runtimePreparationHealth) {
		out.PreparationHealth = operatorobs.CloneRuntimePreparationHealth(snapshot.runtimePreparationHealth)
	}
	filterObsDeploymentDiagnostics(out)
	return out
}

func writeObsDeploymentRuntimeBuilds(w io.Writer, builds *obsDeploymentRuntimeBuilds) {
	if builds == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "runtime_builds advisory=true state=%s reason=%s", builds.State, builds.Reason)
	if builds.SourceHTTPStatus != nil {
		_, _ = fmt.Fprintf(w, " source_http_status=%d", *builds.SourceHTTPStatus)
	}
	_, _ = fmt.Fprintln(w)
	if builds.Reason == "source_"+obsFetchRuntimeBusyReason {
		_, _ = fmt.Fprintln(w, "  Check for overlapping verifiers and review capacity in Gregale Runtime Diagnostics.")
	}
	writeObsDeploymentDiagnostics(w, builds)
	writeObsDeploymentBuildHistory(w, builds)
	writeObsDeploymentLoopHealth(w, builds.LoopHealth)
	writeObsDeploymentBuilderHealth(w, builds.BuilderHealth)
	writeObsDeploymentHandoffHealth(w, builds.HandoffHealth)
	writeObsDeploymentBuildRecoveryHealth(w, builds.BuildRecoveryHealth)
	writeObsDeploymentAppTaskHealth(w, builds.AppTaskHealth)
	writeObsDeploymentCommandCronHealth(w, builds.CommandCronHealth)
	writeObsDeploymentCommandCronOutcomes(w, builds.CommandCronOutcomes)
	writeObsDeploymentHTTPCronDelivery(w, builds.HTTPCronDelivery)
	writeObsDeploymentDelayedTaskDispatch(w, builds.DelayedTaskDispatch)
	writeObsDeploymentCommandCronTiming(w, builds.CommandCronTiming)
	writeObsDeploymentAppTaskResults(w, builds.AppTaskResults)
	writeObsDeploymentAppTaskOperations(w, builds.AppTaskOperations)
	writeObsDeploymentPostCaptureHealth(w, builds.PostCaptureHealth)
	writeObsDeploymentSnapshotPrimeHealth(w, builds.SnapshotPrimeHealth)
	writeObsDeploymentPreparationHealth(w, builds.PreparationHealth)
	if builds.EnvelopeOmittedItems > 0 {
		_, _ = fmt.Fprintf(w, "  envelope_omitted_items=%d\n", builds.EnvelopeOmittedItems)
	}
	if builds.Evidence == nil {
		return
	}
	e := builds.Evidence
	writeObsDeploymentReadiness(w, e.Readiness)
	writeObsDeploymentProcess(w, e.Process)
	_, _ = fmt.Fprintf(w, "  expected_role=%s expected=%d observed=%d matching=%d mismatched=%d missing=%d stale=%d unavailable=%d unexpected=%d distinct_revisions=%d omitted_items=%d\n", e.ExpectedRole, e.Counts.Expected, e.Counts.Observed, e.Counts.Matching, e.Counts.Mismatched, e.Counts.Missing, e.Counts.Stale, e.Counts.Unavailable, e.Counts.Unexpected, e.Counts.DistinctRevisions, e.OmittedItems)
	for _, job := range e.Jobs {
		if job.State != "available" {
			_, _ = fmt.Fprintf(w, "  job=%s inventory_source=%s state=%s reason=%s\n", job.Job, job.InventorySource, job.State, job.Reason)
		}
	}
	for _, item := range e.Items {
		if item.Process != nil {
			writeObsDeploymentProcessItem(w, item)
		}
		if item.Readiness != nil {
			_, _ = fmt.Fprintf(w, "  readiness job=%s daemon=%s node=%s state=%s reason=%s observed_at=%s reason_observed_at=%s\n", item.Job, item.Daemon, item.Node, item.Readiness.State, item.Readiness.Reason, obsDeploymentTimelineTime(item.Readiness.ObservedAt), obsDeploymentTimelineTime(item.Readiness.ReasonObservedAt))
		}
		_, _ = fmt.Fprintf(w, "  job=%s daemon=%s node=%s target_id=%s state=%s reason=%s unexpected=%t git_sha=%s version=%s observed_at=%s scrape_observed_at=%s\n", item.Job, item.Daemon, item.Node, item.TargetID, item.State, item.Reason, item.Unexpected, item.GitSHA, item.Version, obsDeploymentTimelineTime(item.ObservedAt), obsDeploymentTimelineTime(item.ScrapeObservedAt))
	}
}

func fitObsDeploymentRuntimeBuildsWithin(report obsDeploymentReport, limit int) obsDeploymentReport {
	if report.RuntimeBuilds == nil {
		return report
	}
	builds := *report.RuntimeBuilds
	builds.History = cloneObsDeploymentBuildHistory(builds.History)
	builds.Diagnostics = operatorobs.CloneRuntimeDiagnostics(builds.Diagnostics)
	builds.LoopHealth = operatorobs.CloneRuntimeLoopHealth(builds.LoopHealth)
	builds.BuilderHealth = operatorobs.CloneRuntimeBuilderHealth(builds.BuilderHealth)
	builds.HandoffHealth = operatorobs.CloneRuntimeHandoffHealth(builds.HandoffHealth)
	builds.BuildRecoveryHealth = operatorobs.CloneRuntimeBuildRecoveryHealth(builds.BuildRecoveryHealth)
	builds.AppTaskHealth = operatorobs.CloneRuntimeAppTaskHealth(builds.AppTaskHealth)
	builds.CommandCronHealth = operatorobs.CloneRuntimeCommandCronHealth(builds.CommandCronHealth)
	builds.CommandCronOutcomes = operatorobs.CloneRuntimeCommandCronOutcomes(builds.CommandCronOutcomes)
	builds.HTTPCronDelivery = operatorobs.CloneRuntimeHTTPCronDelivery(builds.HTTPCronDelivery)
	builds.DelayedTaskDispatch = operatorobs.CloneRuntimeDelayedTaskDispatch(builds.DelayedTaskDispatch)
	builds.CommandCronTiming = operatorobs.CloneRuntimeCommandCronTiming(builds.CommandCronTiming)
	builds.AppTaskResults = operatorobs.CloneRuntimeAppTaskResults(builds.AppTaskResults)
	builds.AppTaskOperations = operatorobs.CloneRuntimeAppTaskOperations(builds.AppTaskOperations)
	builds.PostCaptureHealth = operatorobs.CloneRuntimePostCaptureHealth(builds.PostCaptureHealth)
	builds.SnapshotPrimeHealth = operatorobs.CloneRuntimeSnapshotPrimeHealth(builds.SnapshotPrimeHealth)
	builds.PreparationHealth = operatorobs.CloneRuntimePreparationHealth(builds.PreparationHealth)
	report.RuntimeBuilds = &builds
	if builds.Evidence != nil {
		evidence := *builds.Evidence
		evidence.Items = slices.Clone(evidence.Items)
		builds.Evidence = &evidence
	}
	for builds.DelayedTaskDispatch != nil && len(builds.DelayedTaskDispatch.Items) > 0 {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil || len(body)+1 <= limit {
			break
		}
		builds.DelayedTaskDispatch.Items = builds.DelayedTaskDispatch.Items[:len(builds.DelayedTaskDispatch.Items)-1]
		builds.DelayedTaskDispatch.OmittedItems++
		builds.DelayedTaskDispatchEnvelopeOmittedItems++
	}
	for builds.HTTPCronDelivery != nil && len(builds.HTTPCronDelivery.Items) > 0 {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil || len(body)+1 <= limit {
			break
		}
		builds.HTTPCronDelivery.Items = builds.HTTPCronDelivery.Items[:len(builds.HTTPCronDelivery.Items)-1]
		builds.HTTPCronDelivery.OmittedItems++
		builds.HTTPCronDeliveryEnvelopeOmittedItems++
	}
	for builds.CommandCronTiming != nil && len(builds.CommandCronTiming.Items) > 0 {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil || len(body)+1 <= limit {
			break
		}
		builds.CommandCronTiming.Items = builds.CommandCronTiming.Items[:len(builds.CommandCronTiming.Items)-1]
		builds.CommandCronTiming.OmittedItems++
		builds.CommandCronTimingEnvelopeOmittedItems++
	}
	for builds.CommandCronOutcomes != nil && len(builds.CommandCronOutcomes.Items) > 0 {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil || len(body)+1 <= limit {
			break
		}
		builds.CommandCronOutcomes.Items = builds.CommandCronOutcomes.Items[:len(builds.CommandCronOutcomes.Items)-1]
		builds.CommandCronOutcomes.OmittedItems++
		builds.CommandCronOutcomesEnvelopeOmittedItems++
	}
	for builds.AppTaskResults != nil && len(builds.AppTaskResults.Items) > 0 {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil || len(body)+1 <= limit {
			break
		}
		builds.AppTaskResults.Items = builds.AppTaskResults.Items[:len(builds.AppTaskResults.Items)-1]
		builds.AppTaskResults.OmittedItems++
		builds.AppTaskResultsEnvelopeOmittedItems++
	}
	for builds.AppTaskOperations != nil && len(builds.AppTaskOperations.Items) > 0 {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil || len(body)+1 <= limit {
			break
		}
		builds.AppTaskOperations.Items = builds.AppTaskOperations.Items[:len(builds.AppTaskOperations.Items)-1]
		builds.AppTaskOperations.OmittedItems++
		builds.AppTaskOperationsEnvelopeOmittedItems++
	}
	for builds.CommandCronHealth != nil && len(builds.CommandCronHealth.Items) > 0 {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil || len(body)+1 <= limit {
			break
		}
		builds.CommandCronHealth.Items = builds.CommandCronHealth.Items[:len(builds.CommandCronHealth.Items)-1]
		builds.CommandCronHealth.OmittedItems++
		builds.CommandCronEnvelopeOmittedItems++
	}
	for builds.AppTaskHealth != nil && len(builds.AppTaskHealth.Items) > 0 {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil || len(body)+1 <= limit {
			break
		}
		builds.AppTaskHealth.Items = builds.AppTaskHealth.Items[:len(builds.AppTaskHealth.Items)-1]
		builds.AppTaskHealth.OmittedItems++
		builds.AppTaskEnvelopeOmittedItems++
	}
	for builds.BuildRecoveryHealth != nil && len(builds.BuildRecoveryHealth.Items) > 0 {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil || len(body)+1 <= limit {
			break
		}
		builds.BuildRecoveryHealth.Items = builds.BuildRecoveryHealth.Items[:len(builds.BuildRecoveryHealth.Items)-1]
		builds.BuildRecoveryHealth.OmittedItems++
		builds.BuildRecoveryEnvelopeOmittedItems++
	}

	for builds.PostCaptureHealth != nil && len(builds.PostCaptureHealth.Items) > 0 {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil || len(body)+1 <= limit {
			break
		}
		builds.PostCaptureHealth.Items = builds.PostCaptureHealth.Items[:len(builds.PostCaptureHealth.Items)-1]
		builds.PostCaptureHealth.OmittedItems++
		builds.PostCaptureEnvelopeOmittedItems++
	}
	for builds.SnapshotPrimeHealth != nil && len(builds.SnapshotPrimeHealth.Items) > 0 {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil || len(body)+1 <= limit {
			break
		}
		builds.SnapshotPrimeHealth.Items = builds.SnapshotPrimeHealth.Items[:len(builds.SnapshotPrimeHealth.Items)-1]
		builds.SnapshotPrimeHealth.OmittedItems++
		builds.SnapshotPrimeEnvelopeOmittedItems++
	}
	for builds.PreparationHealth != nil && len(builds.PreparationHealth.Items) > 0 {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil || len(body)+1 <= limit {
			break
		}
		builds.PreparationHealth.Items = builds.PreparationHealth.Items[:len(builds.PreparationHealth.Items)-1]
		builds.PreparationHealth.OmittedItems++
		builds.PreparationEnvelopeOmittedItems++
	}
	for builds.HandoffHealth != nil && len(builds.HandoffHealth.Items) > 0 {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil || len(body)+1 <= limit {
			break
		}
		builds.HandoffHealth.Items = builds.HandoffHealth.Items[:len(builds.HandoffHealth.Items)-1]
		builds.HandoffHealth.OmittedItems++
		builds.HandoffEnvelopeOmittedItems++
	}
	for builds.BuilderHealth != nil && len(builds.BuilderHealth.Items) > 0 {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil || len(body)+1 <= limit {
			break
		}
		builds.BuilderHealth.Items = builds.BuilderHealth.Items[:len(builds.BuilderHealth.Items)-1]
		builds.BuilderHealth.OmittedItems++
		builds.BuilderEnvelopeOmittedItems++
	}
	for builds.LoopHealth != nil && len(builds.LoopHealth.Items) > 0 {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil || len(body)+1 <= limit {
			break
		}
		builds.LoopHealth.Items = builds.LoopHealth.Items[:len(builds.LoopHealth.Items)-1]
		builds.LoopHealth.OmittedItems++
		builds.LoopEnvelopeOmittedItems++
	}
	for builds.Evidence != nil && len(builds.Evidence.Items) > 0 {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil || len(body)+1 <= limit {
			break
		}
		builds.Evidence.Items = builds.Evidence.Items[:len(builds.Evidence.Items)-1]
		builds.Evidence.OmittedItems++
		builds.EnvelopeOmittedItems++
	}
	for builds.History != nil && len(builds.History.Events) > 0 {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil || len(body)+1 <= limit {
			break
		}
		builds.History.Events = builds.History.Events[1:]
		builds.History.EnvelopeOmittedEvents++
		builds.History.account()
	}
	return report
}
