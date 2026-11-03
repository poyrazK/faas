package builderd

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
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

// guestTimingsLog is the customer build-log text for the timings the builder
// guest reported, or "" from an older guest. The guest is untrusted, so the
// step list and names are bounded again and stripped of control characters.
func guestTimingsLog(t *api.BuildTimings) string {
	if t == nil {
		return ""
	}
	var parts []string
	if t.GuestStartMS > 0 {
		parts = append(parts, "build started "+msDuration(t.GuestStartMS).String()+" after boot")
	}
	if ready, ok := guestStageAt(t, "buildkit-ready"); ok {
		parts = append(parts, "BuildKit ready at "+msDuration(ready).String())
	}
	if t.BuildMS > 0 {
		command := "build command " + msDuration(t.BuildMS).String()
		var exports []string
		if t.ImageExportMS > 0 {
			exports = append(exports, "image export "+msDuration(t.ImageExportMS).String())
		}
		if t.CacheExportMS > 0 {
			exports = append(exports, "dependency cache export "+msDuration(t.CacheExportMS).String())
		}
		if len(exports) > 0 {
			command += " (" + strings.Join(exports, ", ") + ")"
		}
		parts = append(parts, command)
	}
	var b strings.Builder
	if len(parts) > 0 {
		b.WriteString("[build] builder VM: " + strings.Join(parts, ", ") + "\n")
	}
	steps := t.SlowestSteps
	if len(steps) > api.BuildTimingMaxSteps {
		steps = steps[:api.BuildTimingMaxSteps]
	}
	if len(steps) > 0 {
		b.WriteString("[build] slowest build steps:\n")
		for _, step := range steps {
			fmt.Fprintf(&b, "  %8s  %s\n", msDuration(step.MS), printableStepName(step.Name))
		}
	}
	return b.String()
}

// guestTimingAttrs are the slog attributes for the guest's timings. They
// hold no step names: those can carry customer build commands.
func guestTimingAttrs(t *api.BuildTimings) []any {
	if t == nil {
		return nil
	}
	ready, _ := guestStageAt(t, "buildkit-ready")
	return []any{
		"guest_start_ms", t.GuestStartMS,
		"buildkit_ready_ms", ready,
		"build_command_ms", t.BuildMS,
		"image_export_ms", t.ImageExportMS,
		"cache_export_ms", t.CacheExportMS,
	}
}

func guestStageAt(t *api.BuildTimings, stage string) (int64, bool) {
	for _, s := range t.Stages {
		if s.Stage == stage {
			return s.AtMS, true
		}
	}
	return 0, false
}

func msDuration(ms int64) time.Duration {
	return (time.Duration(max(ms, 0)) * time.Millisecond).Round(100 * time.Millisecond)
}

func printableStepName(name string) string {
	if len(name) > api.BuildTimingStepNameMaxBytes {
		name = name[:api.BuildTimingStepNameMaxBytes]
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, name)
}
