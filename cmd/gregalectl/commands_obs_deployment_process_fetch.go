package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

// Each successful response advances the comparison boundary. Transient source
// failures can leave a gap, but cannot make two accepted intervals overlap.
func newObsDeploymentProcessFetcher(client *http.Client, cred obsCredential, opts obsDeploymentOptions) func(context.Context) obsDeploymentSnapshot {
	var previous time.Time
	var diagnosticsUnsupported bool
	return func(ctx context.Context) obsDeploymentSnapshot {
		now := time.Now()
		if previous.Before(now.Add(-api.ObsRuntimeProcessLookback)) || previous.After(now.Add(api.ObsHealthClockSkewTolerance)) {
			previous = time.Time{}
		}
		options := opts
		options.runtimeDiagnosticsUnsupported = diagnosticsUnsupported
		options.processCompareAt = previous
		snapshot := fetchObsDeploymentWithBuilds(ctx, client, cred, options)
		diagnosticsUnsupported = diagnosticsUnsupported || snapshot.runtimeDiagnosticsUnsupported
		if snapshot.runtimeBuildErr != nil {
			_, status := obsFetchFailureDetails(snapshot.runtimeBuildErr)
			// An older API can reject the new optional query key after a rolling cutover.
			// The next existing poll reads its original identity contract without it.
			if status != nil && *status == http.StatusBadRequest {
				previous = time.Time{}
			}
			return snapshot
		}
		value := snapshot.runtimeBuilds
		if value == nil {
			previous = time.Time{}
			return snapshot
		}
		clean := sanitizeObsDeploymentProcess(*value, options.processCompareAt)
		clean = operatorobs.SanitizeRuntimeReadiness(clean)
		if clean.Process == nil || !operatorobs.RuntimeBuildsValid(clean) || clean.ExpectedRole != opts.expectedRole || clean.RequestedRelease != opts.release || !clean.EvidenceAfter.Equal(opts.after) || clean.GeneratedAt.Before(now.Add(-api.ObsInputHealthMaxAge)) || clean.GeneratedAt.After(time.Now().Add(api.ObsHealthClockSkewTolerance)) || clean.GeneratedAt.Before(opts.after.Add(-api.ObsHealthClockSkewTolerance)) {
			previous = time.Time{}
			return snapshot
		}
		previous = clean.GeneratedAt
		return snapshot
	}
}

func sanitizeObsDeploymentProcess(value api.ObsRuntimeBuilds, comparisonAt time.Time) api.ObsRuntimeBuilds {
	if value.Process != nil {
		actual := value.Process.ComparisonAt
		if actual == nil && !comparisonAt.IsZero() || actual != nil && !actual.Equal(comparisonAt) {
			value.Process = nil
		}
	}
	return operatorobs.SanitizeRuntimeProcess(value)
}

// Decode the new optional section independently, including JSON type errors.
// Unknown fields are discarded through the existing typed core contract.
type obsDeploymentRuntimeBuildResponse struct {
	Diagnostics *operatorobs.RuntimeDiagnostics `json:"diagnostics,omitempty"`
	api.ObsRuntimeBuilds
	LoopHealth          *operatorobs.RuntimeLoopHealth          `json:"loop_health,omitempty"`
	BuilderHealth       *operatorobs.RuntimeBuilderHealth       `json:"builder_health,omitempty"`
	HandoffHealth       *operatorobs.RuntimeHandoffHealth       `json:"handoff_health,omitempty"`
	BuildRecoveryHealth *operatorobs.RuntimeBuildRecoveryHealth `json:"build_recovery_health,omitempty"`
	AppTaskHealth       *operatorobs.RuntimeAppTaskHealth       `json:"app_task_health,omitempty"`
	CommandCronHealth   *operatorobs.RuntimeCommandCronHealth   `json:"command_cron_health,omitempty"`
	CommandCronOutcomes *operatorobs.RuntimeCommandCronOutcomes `json:"command_cron_outcomes,omitempty"`
	HTTPCronDelivery    *operatorobs.RuntimeHTTPCronDelivery    `json:"http_cron_delivery,omitempty"`
	DelayedTaskDispatch *operatorobs.RuntimeDelayedTaskDispatch `json:"delayed_task_dispatch,omitempty"`
	CommandCronTiming   *operatorobs.RuntimeCommandCronTiming   `json:"command_cron_timing,omitempty"`
	AppTaskResults      *operatorobs.RuntimeAppTaskResults      `json:"app_task_results,omitempty"`
	AppTaskOperations   *operatorobs.RuntimeAppTaskOperations   `json:"app_task_operations,omitempty"`
	PostCaptureHealth   *operatorobs.RuntimePostCaptureHealth   `json:"post_capture_health,omitempty"`
	SnapshotPrimeHealth *operatorobs.RuntimeSnapshotPrimeHealth `json:"snapshot_prime_health,omitempty"`
	PreparationHealth   *operatorobs.RuntimePreparationHealth   `json:"preparation_health,omitempty"`
}

func (out *obsDeploymentRuntimeBuildResponse) UnmarshalJSON(body []byte) error {
	type core api.ObsRuntimeBuilds
	type itemCore api.ObsRuntimeBuildItem
	var wire struct {
		Diagnostics json.RawMessage `json:"diagnostics"`
		core
		Process             json.RawMessage `json:"process"`
		LoopHealth          json.RawMessage `json:"loop_health"`
		BuilderHealth       json.RawMessage `json:"builder_health"`
		HandoffHealth       json.RawMessage `json:"handoff_health"`
		BuildRecoveryHealth json.RawMessage `json:"build_recovery_health"`
		AppTaskHealth       json.RawMessage `json:"app_task_health"`
		CommandCronHealth   json.RawMessage `json:"command_cron_health"`
		CommandCronOutcomes json.RawMessage `json:"command_cron_outcomes"`
		HTTPCronDelivery    json.RawMessage `json:"http_cron_delivery"`
		DelayedTaskDispatch json.RawMessage `json:"delayed_task_dispatch"`
		CommandCronTiming   json.RawMessage `json:"command_cron_timing"`
		AppTaskResults      json.RawMessage `json:"app_task_results"`
		AppTaskOperations   json.RawMessage `json:"app_task_operations"`
		PostCaptureHealth   json.RawMessage `json:"post_capture_health"`
		SnapshotPrimeHealth json.RawMessage `json:"snapshot_prime_health"`
		PreparationHealth   json.RawMessage `json:"preparation_health"`
		Items               []struct {
			itemCore
			Process json.RawMessage `json:"process"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return err
	}
	if len(wire.Items) > api.ObsRuntimeBuildPreviewMax {
		return errors.New("runtime build preview exceeds limit")
	}
	value := api.ObsRuntimeBuilds(wire.core)
	value.Items = make([]api.ObsRuntimeBuildItem, len(wire.Items))
	for i, item := range wire.Items {
		value.Items[i] = api.ObsRuntimeBuildItem(item.itemCore)
	}
	if len(wire.Process) > 0 && json.Unmarshal(wire.Process, &value.Process) == nil {
		for i, item := range wire.Items {
			if len(item.Process) > 0 && json.Unmarshal(item.Process, &value.Items[i].Process) != nil {
				value.Process = nil
				break
			}
		}
	} else {
		value.Process = nil
	}
	*out = obsDeploymentRuntimeBuildResponse{ObsRuntimeBuilds: operatorobs.SanitizeRuntimeProcess(value)}
	if len(wire.LoopHealth) > 0 {
		var loops *operatorobs.RuntimeLoopHealth
		if json.Unmarshal(wire.LoopHealth, &loops) == nil && operatorobs.RuntimeLoopHealthValid(out.ObsRuntimeBuilds, loops) {
			out.LoopHealth = loops
		}
	}
	if len(wire.BuilderHealth) > 0 {
		var builder *operatorobs.RuntimeBuilderHealth
		if json.Unmarshal(wire.BuilderHealth, &builder) == nil && operatorobs.RuntimeBuilderHealthValid(out.ObsRuntimeBuilds, builder) {
			out.BuilderHealth = builder
		}
	}
	if len(wire.HandoffHealth) > 0 {
		var handoff *operatorobs.RuntimeHandoffHealth
		if json.Unmarshal(wire.HandoffHealth, &handoff) == nil && operatorobs.RuntimeHandoffHealthValid(out.ObsRuntimeBuilds, handoff) {
			out.HandoffHealth = handoff
		}
	}
	if len(wire.BuildRecoveryHealth) > 0 {
		var buildRecovery *operatorobs.RuntimeBuildRecoveryHealth
		if json.Unmarshal(wire.BuildRecoveryHealth, &buildRecovery) == nil && operatorobs.RuntimeBuildRecoveryHealthValid(out.ObsRuntimeBuilds, buildRecovery) {
			out.BuildRecoveryHealth = buildRecovery
		}
	}
	if len(wire.AppTaskHealth) > 0 {
		var appTask *operatorobs.RuntimeAppTaskHealth
		if json.Unmarshal(wire.AppTaskHealth, &appTask) == nil && operatorobs.RuntimeAppTaskHealthValid(out.ObsRuntimeBuilds, appTask) {
			out.AppTaskHealth = appTask
		}
	}
	if len(wire.CommandCronHealth) > 0 {
		var commandCron *operatorobs.RuntimeCommandCronHealth
		if json.Unmarshal(wire.CommandCronHealth, &commandCron) == nil && operatorobs.RuntimeCommandCronHealthValid(out.ObsRuntimeBuilds, commandCron) {
			out.CommandCronHealth = commandCron
		}
	}
	if len(wire.Diagnostics) > 0 {
		var selection *operatorobs.RuntimeDiagnostics
		if json.Unmarshal(wire.Diagnostics, &selection) == nil && operatorobs.RuntimeDiagnosticsValid(out.ExpectedRole, selection) {
			out.Diagnostics = selection
		}
	}
	if len(wire.CommandCronOutcomes) > 0 {
		var commandCronOutcomes *operatorobs.RuntimeCommandCronOutcomes
		if json.Unmarshal(wire.CommandCronOutcomes, &commandCronOutcomes) == nil && operatorobs.RuntimeCommandCronOutcomesValid(out.ObsRuntimeBuilds, commandCronOutcomes) {
			out.CommandCronOutcomes = commandCronOutcomes
		}
	}
	if len(wire.HTTPCronDelivery) > 0 {
		var httpCronDelivery *operatorobs.RuntimeHTTPCronDelivery
		if json.Unmarshal(wire.HTTPCronDelivery, &httpCronDelivery) == nil && operatorobs.RuntimeHTTPCronDeliveryValid(out.ObsRuntimeBuilds, httpCronDelivery) {
			out.HTTPCronDelivery = httpCronDelivery
		}
	}
	if len(wire.DelayedTaskDispatch) > 0 {
		var delayedTaskDispatch *operatorobs.RuntimeDelayedTaskDispatch
		if json.Unmarshal(wire.DelayedTaskDispatch, &delayedTaskDispatch) == nil && operatorobs.RuntimeDelayedTaskDispatchValid(out.ObsRuntimeBuilds, delayedTaskDispatch) {
			out.DelayedTaskDispatch = delayedTaskDispatch
		}
	}
	if len(wire.CommandCronTiming) > 0 {
		var commandCronTiming *operatorobs.RuntimeCommandCronTiming
		if json.Unmarshal(wire.CommandCronTiming, &commandCronTiming) == nil && operatorobs.RuntimeCommandCronTimingValid(out.ObsRuntimeBuilds, commandCronTiming) {
			out.CommandCronTiming = commandCronTiming
		}
	}
	if len(wire.AppTaskResults) > 0 {
		var appTaskResults *operatorobs.RuntimeAppTaskResults
		if json.Unmarshal(wire.AppTaskResults, &appTaskResults) == nil && operatorobs.RuntimeAppTaskResultsValid(out.ObsRuntimeBuilds, appTaskResults) {
			out.AppTaskResults = appTaskResults
		}
	}
	if len(wire.AppTaskOperations) > 0 {
		var appTaskOperations *operatorobs.RuntimeAppTaskOperations
		if json.Unmarshal(wire.AppTaskOperations, &appTaskOperations) == nil && operatorobs.RuntimeAppTaskOperationsValid(out.ObsRuntimeBuilds, appTaskOperations) {
			out.AppTaskOperations = appTaskOperations
		}
	}
	if len(wire.PostCaptureHealth) > 0 {
		var postCapture *operatorobs.RuntimePostCaptureHealth
		if json.Unmarshal(wire.PostCaptureHealth, &postCapture) == nil && operatorobs.RuntimePostCaptureHealthValid(out.ObsRuntimeBuilds, postCapture) {
			out.PostCaptureHealth = postCapture
		}
	}
	if len(wire.SnapshotPrimeHealth) > 0 {
		var snapshotPrime *operatorobs.RuntimeSnapshotPrimeHealth
		if json.Unmarshal(wire.SnapshotPrimeHealth, &snapshotPrime) == nil && operatorobs.RuntimeSnapshotPrimeHealthValid(out.ObsRuntimeBuilds, snapshotPrime) {
			out.SnapshotPrimeHealth = snapshotPrime
		}
	}
	if len(wire.PreparationHealth) > 0 {
		var preparation *operatorobs.RuntimePreparationHealth
		if json.Unmarshal(wire.PreparationHealth, &preparation) == nil && operatorobs.RuntimePreparationHealthValid(out.ObsRuntimeBuilds, preparation) {
			out.PreparationHealth = preparation
		}
	}
	return nil
}
