package builderd

import (
	"fmt"
	"time"
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
