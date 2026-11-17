package main

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

const obsDeploymentBuildHistoryScope = "observed_build_state_and_coverage_counts"

type obsDeploymentBuildProgress struct {
	BuildRecoveryHealth             *obsDeploymentBuildRecoveryProgress       `json:"build_recovery_health,omitempty"`
	AppTaskHealth                   *obsDeploymentAppTaskProgress             `json:"app_task_health,omitempty"`
	CommandCronHealth               *obsDeploymentCommandCronProgress         `json:"command_cron_health,omitempty"`
	CommandCronOutcomes             *obsDeploymentCommandCronOutcomesProgress `json:"command_cron_outcomes,omitempty"`
	HTTPCronDelivery                *obsDeploymentHTTPCronDeliveryProgress    `json:"http_cron_delivery,omitempty"`
	DelayedTaskDispatch             *obsDeploymentDelayedTaskDispatchProgress `json:"delayed_task_dispatch,omitempty"`
	CommandCronTiming               *obsDeploymentCommandCronTimingProgress   `json:"command_cron_timing,omitempty"`
	AppTaskResults                  *obsDeploymentAppTaskResultsProgress      `json:"app_task_results,omitempty"`
	AppTaskOperations               *obsDeploymentAppTaskOperationsProgress   `json:"app_task_operations,omitempty"`
	PostCaptureHealth               *obsDeploymentPostCaptureProgress         `json:"post_capture_health,omitempty"`
	SnapshotPrimeHealth             *obsDeploymentSnapshotPrimeProgress       `json:"snapshot_prime_health,omitempty"`
	PreparationHealth               *obsDeploymentPreparationProgress         `json:"preparation_health,omitempty"`
	HandoffHealth                   *obsDeploymentHandoffProgress             `json:"handoff_health,omitempty"`
	BuilderHealth                   *obsDeploymentBuilderProgress             `json:"builder_health,omitempty"`
	LoopHealth                      *obsDeploymentLoopProgress                `json:"loop_health,omitempty"`
	Process                         *obsDeploymentProcessProgress             `json:"process,omitempty"`
	Readiness                       *obsDeploymentReadinessProgress           `json:"readiness,omitempty"`
	Observations                    int                                       `json:"observations"`
	FirstObservedAt                 *time.Time                                `json:"first_observed_at,omitempty"`
	LastObservedAt                  *time.Time                                `json:"last_observed_at,omitempty"`
	FirstMismatchAt                 *time.Time                                `json:"first_mismatch_at,omitempty"`
	LastMismatchAt                  *time.Time                                `json:"last_mismatch_at,omitempty"`
	FirstMatchingAt                 *time.Time                                `json:"first_matching_at,omitempty"`
	LastMatchingAt                  *time.Time                                `json:"last_matching_at,omitempty"`
	LastRecoveryAt                  *time.Time                                `json:"last_recovery_at,omitempty"`
	LastRegressionAt                *time.Time                                `json:"last_regression_at,omitempty"`
	Regressions                     int                                       `json:"regressions"`
	MatchingThroughoutObservedPolls bool                                      `json:"matching_throughout_observed_polls"`
	ObservedIncompleteEvidence      bool                                      `json:"observed_incomplete_evidence"`
	ObservedSourceUnavailable       bool                                      `json:"observed_source_unavailable"`
}

// Events intentionally describe aggregates, not per-target identity changes.
// Original metric clocks remain distinct from the coordinator observation time.
type obsDeploymentBuildEvent struct {
	Diagnostics         *operatorobs.RuntimeDiagnostics                `json:"diagnostics,omitempty"`
	DiagnosticsSource   string                                         `json:"diagnostics_source,omitempty"`
	CompactDetails      *obsDeploymentBuildCompactDetails              `json:"compact_details,omitempty"`
	OmittedSections     []string                                       `json:"omitted_sections,omitempty"`
	At                  time.Time                                      `json:"at"`
	Observation         int                                            `json:"observation"`
	State               string                                         `json:"state"`
	Reason              string                                         `json:"reason,omitempty"`
	SourceHTTPStatus    *int                                           `json:"source_http_status,omitempty"`
	EvidenceGeneratedAt *time.Time                                     `json:"evidence_generated_at,omitempty"`
	Counts              *api.ObsRuntimeBuildCounts                     `json:"counts,omitempty"`
	Jobs                []api.ObsRuntimeBuildJob                       `json:"jobs,omitempty"`
	Readiness           *api.ObsRuntimeReadiness                       `json:"readiness,omitempty"`
	Process             *api.ObsRuntimeProcess                         `json:"process,omitempty"`
	LoopHealth          *operatorobs.RuntimeLoopSummary                `json:"loop_health,omitempty"`
	BuilderHealth       *operatorobs.RuntimeBuilderSummary             `json:"builder_health,omitempty"`
	HandoffHealth       *operatorobs.RuntimeHandoffSummary             `json:"handoff_health,omitempty"`
	BuildRecoveryHealth *operatorobs.RuntimeBuildRecoverySummary       `json:"build_recovery_health,omitempty"`
	AppTaskHealth       *operatorobs.RuntimeAppTaskSummary             `json:"app_task_health,omitempty"`
	CommandCronHealth   *operatorobs.RuntimeCommandCronSummary         `json:"command_cron_health,omitempty"`
	CommandCronOutcomes *operatorobs.RuntimeCommandCronOutcomesSummary `json:"command_cron_outcomes,omitempty"`
	HTTPCronDelivery    *operatorobs.RuntimeHTTPCronDeliverySummary    `json:"http_cron_delivery,omitempty"`
	DelayedTaskDispatch *operatorobs.RuntimeDelayedTaskDispatchSummary `json:"delayed_task_dispatch,omitempty"`
	CommandCronTiming   *operatorobs.RuntimeCommandCronTimingSummary   `json:"command_cron_timing,omitempty"`
	AppTaskResults      *operatorobs.RuntimeAppTaskResultsSummary      `json:"app_task_results,omitempty"`
	AppTaskOperations   *operatorobs.RuntimeAppTaskOperationsSummary   `json:"app_task_operations,omitempty"`
	PostCaptureHealth   *operatorobs.RuntimePostCaptureSummary         `json:"post_capture_health,omitempty"`
	SnapshotPrimeHealth *operatorobs.RuntimeSnapshotPrimeSummary       `json:"snapshot_prime_health,omitempty"`
	PreparationHealth   *operatorobs.RuntimePreparationSummary         `json:"preparation_health,omitempty"`
}

type obsDeploymentBuildHistory struct {
	Scope                 string                    `json:"scope"`
	EventLimit            int                       `json:"event_limit"`
	ByteLimit             int                       `json:"byte_limit"`
	TotalEvents           int                       `json:"total_events"`
	RetainedEvents        int                       `json:"retained_events"`
	RetainedBytes         int                       `json:"retained_bytes"`
	DroppedEvents         int                       `json:"dropped_events"`
	EnvelopeOmittedEvents int                       `json:"envelope_omitted_events"`
	DetailOmittedEvents   int                       `json:"detail_omitted_events,omitempty"`
	Truncated             bool                      `json:"truncated"`
	Events                []obsDeploymentBuildEvent `json:"events"`
}

type obsDeploymentBuildRecorder struct {
	diagnosticNotSelected  map[string]int
	progress               obsDeploymentBuildProgress
	history                obsDeploymentBuildHistory
	lastSignature          string
	pendingMismatch        bool
	lastIdentityMatching   bool
	pendingNotReady        bool
	lastReadinessReady     bool
	lastProcessAt          time.Time
	pendingLoopStall       bool
	lastLoopsClear         bool
	pendingBuilderOverdue  bool
	lastBuilderWithin      bool
	lastHandoffEmpty       bool
	lastBuildRecoveryEmpty bool
	lastAppTaskEmpty       bool
	lastCommandCronEmpty   bool
	lastPostCaptureEmpty   bool
	lastSnapshotPrimeEmpty bool
	lastPreparationEmpty   bool
}

func newObsDeploymentBuildRecorder() *obsDeploymentBuildRecorder {
	r := &obsDeploymentBuildRecorder{history: obsDeploymentBuildHistory{Scope: obsDeploymentBuildHistoryScope, EventLimit: api.ObsPostDeployBuildEventsMax, ByteLimit: api.ObsPostDeployBuildHistoryBytesMax, Events: []obsDeploymentBuildEvent{}}}
	r.history.account()
	return r
}

func (r *obsDeploymentBuildRecorder) observe(current *obsDeploymentRuntimeBuilds, at time.Time) *obsDeploymentRuntimeBuilds {
	at = at.UTC()
	if last := r.progress.LastObservedAt; last != nil && at.Before(*last) {
		at = *last
	}
	r.observeProgress(current, at)
	r.observeReadiness(current, at)
	r.observeProcess(current, at)
	r.observeLoops(current, at)
	r.observeBuilder(current, at)
	r.observeHandoff(current, at)
	r.observeBuildRecovery(current, at)
	r.observeAppTask(current, at)
	r.observeCommandCron(current, at)
	r.observeCommandCronOutcomes(current, at)
	r.observeHTTPCronDelivery(current, at)
	r.observeDelayedTaskDispatch(current, at)
	r.observeCommandCronTiming(current, at)
	r.observeAppTaskResults(current, at)
	r.observeAppTaskOperations(current, at)
	r.observePostCapture(current, at)
	r.observeSnapshotPrime(current, at)
	r.observePreparation(current, at)
	event := obsDeploymentBuildEvent{Diagnostics: operatorobs.CloneRuntimeDiagnostics(current.Diagnostics), DiagnosticsSource: current.DiagnosticsSource, At: at, Observation: r.progress.Observations, State: current.State, Reason: current.Reason, SourceHTTPStatus: obsDeploymentEvidencePtr(current.SourceHTTPStatus)}
	if current.Evidence != nil {
		event.EvidenceGeneratedAt = obsDeploymentEvidencePtr(&current.Evidence.GeneratedAt)
		event.Counts = obsDeploymentEvidencePtr(&current.Evidence.Counts)
		event.Jobs = slices.Clone(current.Evidence.Jobs)
		event.Readiness = cloneObsDeploymentReadiness(current.Evidence.Readiness)
		event.Process = cloneObsDeploymentProcess(current.Evidence.Process)
	}
	if current.LoopHealth != nil {
		event.LoopHealth = obsDeploymentEvidencePtr(&current.LoopHealth.RuntimeLoopSummary)
	}
	if current.BuilderHealth != nil {
		event.BuilderHealth = obsDeploymentEvidencePtr(&current.BuilderHealth.RuntimeBuilderSummary)
	}
	if current.HandoffHealth != nil {
		event.HandoffHealth = obsDeploymentEvidencePtr(&current.HandoffHealth.RuntimeHandoffSummary)
	}
	if current.BuildRecoveryHealth != nil {
		event.BuildRecoveryHealth = obsDeploymentEvidencePtr(&current.BuildRecoveryHealth.RuntimeBuildRecoverySummary)
	}
	if current.AppTaskHealth != nil {
		event.AppTaskHealth = operatorobs.CloneRuntimeAppTaskSummary(&current.AppTaskHealth.RuntimeAppTaskSummary)
	}
	if current.CommandCronHealth != nil {
		event.CommandCronHealth = operatorobs.CloneRuntimeCommandCronSummary(&current.CommandCronHealth.RuntimeCommandCronSummary)
	}
	if current.CommandCronOutcomes != nil {
		event.CommandCronOutcomes = operatorobs.CloneRuntimeCommandCronOutcomesSummary(&current.CommandCronOutcomes.RuntimeCommandCronOutcomesSummary)
	}
	if current.HTTPCronDelivery != nil {
		event.HTTPCronDelivery = operatorobs.CloneRuntimeHTTPCronDeliverySummary(&current.HTTPCronDelivery.RuntimeHTTPCronDeliverySummary)
	}
	if current.DelayedTaskDispatch != nil {
		event.DelayedTaskDispatch = operatorobs.CloneRuntimeDelayedTaskDispatchSummary(&current.DelayedTaskDispatch.RuntimeDelayedTaskDispatchSummary)
	}
	if current.CommandCronTiming != nil {
		event.CommandCronTiming = operatorobs.CloneRuntimeCommandCronTimingSummary(&current.CommandCronTiming.RuntimeCommandCronTimingSummary)
	}
	if current.AppTaskResults != nil {
		event.AppTaskResults = operatorobs.CloneRuntimeAppTaskResultsSummary(&current.AppTaskResults.RuntimeAppTaskResultsSummary)
	}
	if current.AppTaskOperations != nil {
		event.AppTaskOperations = operatorobs.CloneRuntimeAppTaskOperationsSummary(&current.AppTaskOperations.RuntimeAppTaskOperationsSummary)
	}
	if current.PostCaptureHealth != nil {
		event.PostCaptureHealth = operatorobs.CloneRuntimePostCaptureSummary(&current.PostCaptureHealth.RuntimePostCaptureSummary)
	}
	if current.SnapshotPrimeHealth != nil {
		event.SnapshotPrimeHealth = operatorobs.CloneRuntimeSnapshotPrimeSummary(&current.SnapshotPrimeHealth.RuntimeSnapshotPrimeSummary)
	}
	if current.PreparationHealth != nil {
		event.PreparationHealth = operatorobs.CloneRuntimePreparationSummary(&current.PreparationHealth.RuntimePreparationSummary)
	}
	semantic := event
	semantic.At, semantic.Observation, semantic.EvidenceGeneratedAt = time.Time{}, 0, nil
	if semantic.Process != nil {
		semantic.Process = cloneObsDeploymentProcess(semantic.Process)
		c := semantic.Process.Counts
		if c.Changed+c.NewTargets+c.RemovedTargets == 0 {
			semantic.Process.ComparisonAt = nil
		}
	}
	body, _ := json.Marshal(semantic)
	if signature := string(body); signature != r.lastSignature {
		r.lastSignature = signature
		r.history.TotalEvents++
		event = compactObsDeploymentBuildEvent(event, r.history.ByteLimit)
		r.history.Events = append(r.history.Events, event)
		for len(r.history.Events) > r.history.EventLimit || obsDeploymentBuildHistoryBytes(r.history.Events) > r.history.ByteLimit {
			r.history.Events = r.history.Events[1:]
			r.history.DroppedEvents++
		}
		r.history.account()
	}
	return r.snapshot(current)
}

func (r *obsDeploymentBuildRecorder) observeProgress(current *obsDeploymentRuntimeBuilds, at time.Time) {
	p := &r.progress
	p.Observations++
	if p.FirstObservedAt == nil {
		p.FirstObservedAt = obsDeploymentEvidencePtr(&at)
	}
	p.LastObservedAt = obsDeploymentEvidencePtr(&at)
	matching := current.State == "matching"
	p.MatchingThroughoutObservedPolls = matching && (p.Observations == 1 || p.MatchingThroughoutObservedPolls)
	if current.Evidence == nil {
		p.ObservedIncompleteEvidence, p.ObservedSourceUnavailable = true, true
	} else {
		c := current.Evidence.Counts
		p.ObservedIncompleteEvidence = p.ObservedIncompleteEvidence || current.State == "incomplete" || current.State == "unknown" || c.Missing+c.Stale+c.Unavailable+c.Unexpected > 0
		for _, job := range current.Evidence.Jobs {
			if job.State != "available" {
				p.ObservedIncompleteEvidence, p.ObservedSourceUnavailable = true, true
			}
		}
	}
	if matching {
		if p.FirstMatchingAt == nil {
			p.FirstMatchingAt = obsDeploymentEvidencePtr(&at)
		}
		p.LastMatchingAt = obsDeploymentEvidencePtr(&at)
		if r.pendingMismatch {
			p.LastRecoveryAt = obsDeploymentEvidencePtr(&at)
			r.pendingMismatch = false
		}
		r.lastIdentityMatching = true
	}
	if current.State == "mixed" || current.State == "mismatched" {
		if p.FirstMismatchAt == nil {
			p.FirstMismatchAt = obsDeploymentEvidencePtr(&at)
		}
		p.LastMismatchAt = obsDeploymentEvidencePtr(&at)
		if r.lastIdentityMatching {
			p.Regressions++
			p.LastRegressionAt = obsDeploymentEvidencePtr(&at)
		}
		r.pendingMismatch, r.lastIdentityMatching = true, false
	}
}

func (r *obsDeploymentBuildRecorder) snapshot(current *obsDeploymentRuntimeBuilds) *obsDeploymentRuntimeBuilds {
	out := *current
	out.Diagnostics = operatorobs.CloneRuntimeDiagnostics(current.Diagnostics)
	progress := r.progress
	progress.BuilderHealth = cloneObsDeploymentBuilderProgress(progress.BuilderHealth)
	progress.HandoffHealth = cloneObsDeploymentHandoffProgress(progress.HandoffHealth)
	progress.BuildRecoveryHealth = cloneObsDeploymentBuildRecoveryProgress(progress.BuildRecoveryHealth)
	progress.AppTaskHealth = cloneObsDeploymentAppTaskProgress(progress.AppTaskHealth)
	progress.CommandCronHealth = cloneObsDeploymentCommandCronProgress(progress.CommandCronHealth)
	progress.CommandCronOutcomes = cloneObsDeploymentCommandCronOutcomesProgress(progress.CommandCronOutcomes)
	progress.HTTPCronDelivery = cloneObsDeploymentHTTPCronDeliveryProgress(progress.HTTPCronDelivery)
	progress.DelayedTaskDispatch = cloneObsDeploymentDelayedTaskDispatchProgress(progress.DelayedTaskDispatch)
	progress.CommandCronTiming = cloneObsDeploymentCommandCronTimingProgress(progress.CommandCronTiming)
	progress.AppTaskResults = cloneObsDeploymentAppTaskResultsProgress(progress.AppTaskResults)
	progress.AppTaskOperations = cloneObsDeploymentAppTaskOperationsProgress(progress.AppTaskOperations)
	progress.PostCaptureHealth = cloneObsDeploymentPostCaptureProgress(progress.PostCaptureHealth)
	progress.SnapshotPrimeHealth = cloneObsDeploymentSnapshotPrimeProgress(progress.SnapshotPrimeHealth)
	progress.PreparationHealth = cloneObsDeploymentPreparationProgress(progress.PreparationHealth)
	progress.LoopHealth = cloneObsDeploymentLoopProgress(progress.LoopHealth)
	progress.Readiness = cloneObsDeploymentReadinessProgress(progress.Readiness)
	progress.Process = cloneObsDeploymentProcessProgress(progress.Process)
	progress.FirstObservedAt = obsDeploymentEvidencePtr(progress.FirstObservedAt)
	progress.LastObservedAt = obsDeploymentEvidencePtr(progress.LastObservedAt)
	progress.FirstMismatchAt = obsDeploymentEvidencePtr(progress.FirstMismatchAt)
	progress.LastMismatchAt = obsDeploymentEvidencePtr(progress.LastMismatchAt)
	progress.FirstMatchingAt = obsDeploymentEvidencePtr(progress.FirstMatchingAt)
	progress.LastMatchingAt = obsDeploymentEvidencePtr(progress.LastMatchingAt)
	progress.LastRecoveryAt = obsDeploymentEvidencePtr(progress.LastRecoveryAt)
	progress.LastRegressionAt = obsDeploymentEvidencePtr(progress.LastRegressionAt)
	out.Progress, out.History = &progress, cloneObsDeploymentBuildHistory(&r.history)
	return &out
}

func (h *obsDeploymentBuildHistory) account() {
	h.RetainedEvents = len(h.Events)
	h.RetainedBytes = obsDeploymentBuildHistoryBytes(h.Events)
	h.DetailOmittedEvents = 0
	for _, event := range h.Events {
		if len(event.OmittedSections) > 0 {
			h.DetailOmittedEvents++
		}
	}
	h.Truncated = h.DroppedEvents > 0 || h.EnvelopeOmittedEvents > 0 || h.DetailOmittedEvents > 0
}

func obsDeploymentBuildHistoryBytes(events []obsDeploymentBuildEvent) int {
	body, err := json.MarshalIndent(events, "      ", "  ")
	if err != nil {
		return api.ObsPostDeployBuildHistoryBytesMax + 1
	}
	return len(body) + len("      ") + 1
}

func cloneObsDeploymentBuildHistory(in *obsDeploymentBuildHistory) *obsDeploymentBuildHistory {
	if in == nil {
		return nil
	}
	out := *in
	out.Events = slices.Clone(in.Events)
	for i := range out.Events {
		e := &out.Events[i]
		e.Diagnostics = operatorobs.CloneRuntimeDiagnostics(e.Diagnostics)
		e.CompactDetails = obsDeploymentEvidencePtr(e.CompactDetails)
		e.OmittedSections = slices.Clone(e.OmittedSections)
		e.SourceHTTPStatus = obsDeploymentEvidencePtr(e.SourceHTTPStatus)
		e.EvidenceGeneratedAt = obsDeploymentEvidencePtr(e.EvidenceGeneratedAt)
		e.Counts = obsDeploymentEvidencePtr(e.Counts)
		e.Jobs = slices.Clone(e.Jobs)
		e.Readiness = cloneObsDeploymentReadiness(e.Readiness)
		e.Process = cloneObsDeploymentProcess(e.Process)
		e.LoopHealth = obsDeploymentEvidencePtr(e.LoopHealth)
		e.BuilderHealth = obsDeploymentEvidencePtr(e.BuilderHealth)
		e.HandoffHealth = obsDeploymentEvidencePtr(e.HandoffHealth)
		e.BuildRecoveryHealth = obsDeploymentEvidencePtr(e.BuildRecoveryHealth)
		e.AppTaskHealth = operatorobs.CloneRuntimeAppTaskSummary(e.AppTaskHealth)
		e.CommandCronHealth = operatorobs.CloneRuntimeCommandCronSummary(e.CommandCronHealth)
		e.CommandCronOutcomes = operatorobs.CloneRuntimeCommandCronOutcomesSummary(e.CommandCronOutcomes)
		e.HTTPCronDelivery = operatorobs.CloneRuntimeHTTPCronDeliverySummary(e.HTTPCronDelivery)
		e.DelayedTaskDispatch = operatorobs.CloneRuntimeDelayedTaskDispatchSummary(e.DelayedTaskDispatch)
		e.CommandCronTiming = operatorobs.CloneRuntimeCommandCronTimingSummary(e.CommandCronTiming)
		e.AppTaskResults = operatorobs.CloneRuntimeAppTaskResultsSummary(e.AppTaskResults)
		e.AppTaskOperations = operatorobs.CloneRuntimeAppTaskOperationsSummary(e.AppTaskOperations)
		e.PostCaptureHealth = operatorobs.CloneRuntimePostCaptureSummary(e.PostCaptureHealth)
		e.SnapshotPrimeHealth = operatorobs.CloneRuntimeSnapshotPrimeSummary(e.SnapshotPrimeHealth)
		e.PreparationHealth = operatorobs.CloneRuntimePreparationSummary(e.PreparationHealth)
	}
	return &out
}
