package builderd

import (
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// builderQueueWaitNotice is the queue wait from which the build log says how
// long the build waited for a builder slot.
const builderQueueWaitNotice = 5 * time.Second

// builderSlotLogLine names the allocated slot and, after a noticeable wait,
// how long the build queued for it. On production-us builds wait p90 180 s
// and p99 440 s for a builder, and the deploy stages count that wait as
// source download, so the build log is where a customer learns why the
// deploy sat still (Hunt 14 H14-12).
func builderSlotLogLine(label string, wait time.Duration) string {
	if wait < builderQueueWaitNotice {
		return fmt.Sprintf("allocated builder slot (%s)\n", label)
	}
	return fmt.Sprintf("allocated builder slot (%s) after %s in the build queue\n", label, wait.Round(time.Second))
}

// builderQueueWaitReason is the note on the closed source_download stage when
// the build queued for a builder slot: the deploy stages book that wait to
// "Source downloaded", which read as a 192 s download on production-us
// (Hunt 12 H12-5). RequeueBuild keeps enqueued_at and each claim stamps
// started_at, so their gap is the time spent queued.
func builderQueueWaitReason(build state.Build) string {
	if build.StartedAt.IsZero() || build.EnqueuedAt.IsZero() {
		return ""
	}
	wait := build.StartedAt.Sub(build.EnqueuedAt)
	if wait < builderQueueWaitNotice {
		return ""
	}
	return fmt.Sprintf("waited %s for a builder slot", wait.Round(time.Second))
}
