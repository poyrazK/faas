package operatorobs

import (
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
)

func fitRuntimeLoopResponse(out RuntimeBuildsLoopResponse) RuntimeBuildsLoopResponse {
	fits := func() bool {
		body, err := json.MarshalIndent(out, "", "  ")
		return err != nil || len(body) < api.ObsRuntimeBuildResponseMaxBytes
	}
	if fits() {
		return out
	}
	// Preserve higher-priority evidence and all full-set summaries. Empty each
	// lower-priority preview before shortening the next one.
	if out.DelayedTaskDispatch != nil && trimRuntimePreview(&out.DelayedTaskDispatch.Items, &out.DelayedTaskDispatch.OmittedItems, fits) {
		return out
	}
	if out.HTTPCronDelivery != nil && trimRuntimePreview(&out.HTTPCronDelivery.Items, &out.HTTPCronDelivery.OmittedItems, fits) {
		return out
	}
	if out.CommandCronTiming != nil && trimRuntimePreview(&out.CommandCronTiming.Items, &out.CommandCronTiming.OmittedItems, fits) {
		return out
	}
	if out.CommandCronOutcomes != nil && trimRuntimePreview(&out.CommandCronOutcomes.Items, &out.CommandCronOutcomes.OmittedItems, fits) {
		return out
	}
	if out.AppTaskResults != nil && trimRuntimePreview(&out.AppTaskResults.Items, &out.AppTaskResults.OmittedItems, fits) {
		return out
	}
	if out.AppTaskOperations != nil && trimRuntimePreview(&out.AppTaskOperations.Items, &out.AppTaskOperations.OmittedItems, fits) {
		return out
	}
	if out.CommandCronHealth != nil && trimRuntimePreview(&out.CommandCronHealth.Items, &out.CommandCronHealth.OmittedItems, fits) {
		return out
	}
	if out.AppTaskHealth != nil && trimRuntimePreview(&out.AppTaskHealth.Items, &out.AppTaskHealth.OmittedItems, fits) {
		return out
	}
	if out.BuildRecoveryHealth != nil && trimRuntimePreview(&out.BuildRecoveryHealth.Items, &out.BuildRecoveryHealth.OmittedItems, fits) {
		return out
	}
	if out.PostCaptureHealth != nil && trimRuntimePreview(&out.PostCaptureHealth.Items, &out.PostCaptureHealth.OmittedItems, fits) {
		return out
	}
	if out.SnapshotPrimeHealth != nil && trimRuntimePreview(&out.SnapshotPrimeHealth.Items, &out.SnapshotPrimeHealth.OmittedItems, fits) {
		return out
	}
	if out.PreparationHealth != nil && trimRuntimePreview(&out.PreparationHealth.Items, &out.PreparationHealth.OmittedItems, fits) {
		return out
	}
	if out.HandoffHealth != nil && trimRuntimePreview(&out.HandoffHealth.Items, &out.HandoffHealth.OmittedItems, fits) {
		return out
	}
	if out.BuilderHealth != nil && trimRuntimePreview(&out.BuilderHealth.Items, &out.BuilderHealth.OmittedItems, fits) {
		return out
	}
	if out.LoopHealth != nil && trimRuntimePreview(&out.LoopHealth.Items, &out.LoopHealth.OmittedItems, fits) {
		return out
	}
	trimRuntimePreview(&out.Items, &out.OmittedItems, fits)
	return out
}

// trimRuntimePreview retains the largest prefix that fits. The caller has
// already established that the full response exceeds the envelope. Dropping
// any typed preview item saves more JSON bytes than its omission count adds,
// so prefix sizes are monotonic. Binary search avoids serializing the entire
// response once for every dropped item.
func trimRuntimePreview[T any](items *[]T, omitted *int, fits func() bool) bool {
	if len(*items) == 0 {
		return false
	}
	original, alreadyOmitted := *items, *omitted
	keep := func(n int) {
		*items = original[:n]
		*omitted = alreadyOmitted + len(original) - n
	}
	keep(0)
	if !fits() {
		return false
	}
	low, high := 0, len(original)
	for low+1 < high {
		mid := low + (high-low)/2
		keep(mid)
		if fits() {
			low = mid
		} else {
			high = mid
		}
	}
	keep(low)
	return true
}
