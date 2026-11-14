package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	devPatchWatchInterval = 250 * time.Millisecond
	devPatchWatchTimeout  = 20 * time.Second
)

// devPatchEvent is the --json NDJSON record for one live patch (ADR-740). It
// carries only timings and identifiers, never source.
type devPatchEvent struct {
	Event         string `json:"event"`
	DeploymentID  string `json:"deployment_id"`
	Generation    int64  `json:"generation"`
	State         string `json:"state"`
	EditToPatchMS int64  `json:"edit_to_patch_ms,omitempty"`
	ApplyMS       int64  `json:"apply_ms,omitempty"`
	ErrorCode     string `json:"error_code,omitempty"`
	// BuildDeferred is set when this patch is the whole sync and the build
	// waits for the developer to stop saving (ADR-20261021221906).
	BuildDeferred bool `json:"build_deferred,omitempty"`
}

type devPatchStatusFunc func(ctx context.Context, generation int64) (api.DevPatchStatusResponse, error)

// watchDevPatch waits for a published live patch to reach the running
// environment while the normal developer build continues. It stops when the
// patch is acknowledged, the sync is superseded (ctx), or the timeout passes;
// a patch that never lands simply leaves the build to deliver the edit.
func watchDevPatch(ctx context.Context, generation int64, status devPatchStatusFunc, interval, timeout time.Duration, delivered func(api.DevPatchStatusResponse, time.Time)) {
	if current, observedAt, ok := awaitDevPatch(ctx, generation, status, interval, timeout); ok {
		delivered(current, observedAt)
	}
}

// awaitDevPatch polls a published patch until it leaves pending. ok is
// false when ctx ends or the timeout passes first.
func awaitDevPatch(ctx context.Context, generation int64, status devPatchStatusFunc, interval, timeout time.Duration) (api.DevPatchStatusResponse, time.Time, bool) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		current, err := status(ctx, generation)
		if err == nil && current.State != api.DevPatchStatePending {
			return current, time.Now(), true
		}
		select {
		case <-ctx.Done():
			return api.DevPatchStatusResponse{}, time.Time{}, false
		case <-deadline.C:
			return api.DevPatchStatusResponse{}, time.Time{}, false
		case <-ticker.C:
		}
	}
}

// finishDeferredDevSync is a sync whose build waits behind its live patch
// (ADR-20261021221906): it is live once the patch applies. A failed patch hands the
// sync back to the build, which apid starts when it reports the failure; a
// patch no instance acknowledged in time stays published and applies when
// the environment next starts. ctx ending means a newer sync superseded it.
func finishDeferredDevSync(ctx context.Context, dep api.DeploymentResponse, status devPatchStatusFunc, interval, timeout time.Duration, delivered func(api.DevPatchStatusResponse, time.Time) time.Duration) (int, bool) {
	current, observedAt, ok := awaitDevPatch(ctx, dep.DevPatch.Generation, status, interval, timeout)
	switch {
	case ctx.Err() != nil:
		return 1, true
	case !ok:
		reportDeferredDevPatch(dep, api.DevPatchStatusResponse{Generation: dep.DevPatch.Generation, State: api.DevPatchStatePending}, 0)
		return 0, true
	}
	editToPatch := delivered(current, observedAt)
	if current.State == api.DevPatchStateFailed {
		reportDevPatch(dep.ID, current, editToPatch)
		return 0, false
	}
	reportDeferredDevPatch(dep, current, editToPatch)
	return 0, true
}

// reportDeferredDevPatch says the edit is live and the build waits.
func reportDeferredDevPatch(dep api.DeploymentResponse, status api.DevPatchStatusResponse, editToPatch time.Duration) {
	if jsonOutput {
		_ = writeJSON(devPatchEvent{
			Event: "developer_patch", DeploymentID: dep.ID, Generation: status.Generation, State: status.State,
			EditToPatchMS: editToPatch.Milliseconds(), ApplyMS: status.ApplyMS, BuildDeferred: true,
		})
		return
	}
	if status.State == api.DevPatchStatePending {
		PrintProgress(osStdout, "live patch %d published; it applies when the environment next starts. The full build runs after you stop saving.", status.Generation)
		return
	}
	PrintOK(osStdout, "Live patch applied in %s; your edit is live. The full build runs after you stop saving.", editToPatch.Round(100*time.Millisecond))
}

// devHistoryPatchNote renders the ADR-740 live patch phase of one sync for
// `gregale dev history`: when the edit reached the running environment,
// before the build that followed it.
func devHistoryPatchNote(phases []api.DevSyncPhase) string {
	for _, phase := range phases {
		if phase.Phase != devPhasePatch {
			continue
		}
		if phase.Status == devPhaseFailed {
			return "  (live patch failed: " + phase.Reason + ")"
		}
		return "  (live patch " + formatDevDuration(phase.DurationMS) + ")"
	}
	return ""
}

// reportDevPatch tells the developer the edit is already running, before the
// full build finishes, or why the live patch did not apply.
func reportDevPatch(deploymentID string, status api.DevPatchStatusResponse, editToPatch time.Duration) {
	if jsonOutput {
		_ = writeJSON(devPatchEvent{
			Event: "developer_patch", DeploymentID: deploymentID, Generation: status.Generation, State: status.State,
			EditToPatchMS: editToPatch.Milliseconds(), ApplyMS: status.ApplyMS, ErrorCode: status.ErrorCode,
		})
		return
	}
	if status.State == api.DevPatchStateFailed {
		PrintWarn(osStderr, "live patch %d did not apply (%s); the full build will deliver this edit", status.Generation, status.ErrorCode)
		return
	}
	PrintOK(osStdout, "Live patch applied in %s; the app is restarting with your edit while the full build continues.", editToPatch.Round(100*time.Millisecond))
}
