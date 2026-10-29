package builderd

import (
	"fmt"
	"time"
)

// buildPhaseClock records when a VM build crossed each phase so a slow build
// says where its time went: starting the builder VM, the build inside it, the
// host-side artifact checks between the VM and completion, and completion
// (layer handoff and deployment state).
type buildPhaseClock struct {
	start, vmReady, vmDone, published, completed time.Time
}

func phaseDuration(from, to time.Time) time.Duration {
	if from.IsZero() || to.IsZero() || to.Before(from) {
		return 0
	}
	return to.Sub(from).Round(100 * time.Millisecond)
}

// vmLine is the customer build-log line written when the builder VM finishes.
func (c buildPhaseClock) vmLine() string {
	return fmt.Sprintf("[build] builder VM ready in %s; build in VM %s\n",
		phaseDuration(c.start, c.vmReady), phaseDuration(c.vmReady, c.vmDone))
}

// attrs are the slog attributes for the completed build, in milliseconds.
func (c buildPhaseClock) attrs() []any {
	return []any{
		"vm_start_ms", phaseDuration(c.start, c.vmReady).Milliseconds(),
		"vm_build_ms", phaseDuration(c.vmReady, c.vmDone).Milliseconds(),
		"artifact_checks_ms", phaseDuration(c.vmDone, c.published).Milliseconds(),
		"complete_ms", phaseDuration(c.published, c.completed).Milliseconds(),
		"total_ms", phaseDuration(c.start, c.completed).Milliseconds(),
	}
}
