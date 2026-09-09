package main

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// The development loop already has all of the server-side stage signals. This
// adapter turns them into a compact, stable summary at the end of each sync so
// a developer can tell which part of edit → live got slower without reading a
// scrolling ticker. The names are intentionally product-facing; the server's
// closed stage vocabulary stays an implementation detail.
const (
	devPhaseSync   = "sync"
	devPhaseCache  = "cache"
	devPhaseBuild  = "build"
	devPhaseBoot   = "boot"
	devPhaseReady  = "ready"
	devPhaseRoute  = "route"
	devPhaseFailed = "failed"
)

var devPhaseOrder = []string{
	devPhaseSync,
	devPhaseCache,
	devPhaseBuild,
	devPhaseBoot,
	devPhaseReady,
	devPhaseRoute,
}

type devPhaseTiming struct {
	Phase      string `json:"phase"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

type devPhaseTracker struct {
	mu             sync.Mutex
	deploymentID   string
	started        map[string]time.Time
	timings        map[string]devPhaseTiming
	routeStartedAt time.Time
}

func newDevPhaseTracker() *devPhaseTracker {
	return &devPhaseTracker{
		started: make(map[string]time.Time),
		timings: make(map[string]devPhaseTiming),
	}
}

func (t *devPhaseTracker) setDeploymentID(id string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.deploymentID = id
	t.mu.Unlock()
}

func (t *devPhaseTracker) begin(phase string) {
	if t == nil || phase == "" {
		return
	}
	t.mu.Lock()
	t.started[phase] = time.Now()
	t.timings[phase] = devPhaseTiming{Phase: phase, Status: "in_progress"}
	if phase == devPhaseReady {
		// Route switching begins only after the guest is ready. This keeps
		// the route number separate from boot/readiness regressions.
		t.routeStartedAt = time.Time{}
	}
	t.mu.Unlock()
}

func (t *devPhaseTracker) complete(phase string, duration time.Duration) {
	t.completeWithReason(phase, duration, "")
}

func (t *devPhaseTracker) completeWithReason(phase string, duration time.Duration, reason string) {
	if t == nil || phase == "" {
		return
	}
	if duration < 0 {
		duration = 0
	}
	t.mu.Lock()
	delete(t.started, phase)
	timing := devPhaseTiming{
		Phase:      phase,
		Status:     "completed",
		DurationMS: duration.Milliseconds(),
		Reason:     reason,
	}
	if reason != "" {
		timing.Status = devPhaseFailed
	}
	t.timings[phase] = timing
	if phase == devPhaseReady && reason == "" {
		t.routeStartedAt = time.Now()
	}
	t.mu.Unlock()
}

func (t *devPhaseTracker) sourceSync(duration time.Duration, err error) {
	if err != nil {
		t.completeWithReason(devPhaseSync, duration, err.Error())
		return
	}
	t.complete(devPhaseSync, duration)
}

// observeStage translates the deployment stream's closed stage names into the
// smaller set a developer cares about. DurationMs is authoritative when the
// server closes a stage; the client clock is used only for the route switch.
func (t *devPhaseTracker) observeStage(name, status string, durationMS int64, reason string) {
	phase := map[string]string{
		"dependency_restore": devPhaseCache,
		"image_build":        devPhaseBuild,
		"snapshot_prepare":   devPhaseBoot,
		"readiness":          devPhaseReady,
	}[name]
	if phase == "" {
		return
	}
	switch status {
	case stageStatusInProgress:
		t.begin(phase)
	case stageStatusCompleted:
		t.complete(phase, time.Duration(durationMS)*time.Millisecond)
	case stageStatusFailed:
		if reason == "" {
			reason = devPhaseFailed
		}
		t.completeWithReason(phase, time.Duration(durationMS)*time.Millisecond, reason)
	}
}

func (t *devPhaseTracker) finishRouteSwitch() {
	if t == nil {
		return
	}
	t.mu.Lock()
	started := t.routeStartedAt
	t.mu.Unlock()
	if started.IsZero() {
		return
	}
	t.complete(devPhaseRoute, time.Since(started))
}

func (t *devPhaseTracker) snapshot() (string, []devPhaseTiming) {
	if t == nil {
		return "", nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	items := make([]devPhaseTiming, 0, len(devPhaseOrder))
	for _, phase := range devPhaseOrder {
		if timing, ok := t.timings[phase]; ok {
			items = append(items, timing)
		}
	}
	return t.deploymentID, items
}

func (t *devPhaseTracker) render(w io.Writer) {
	deploymentID, timings := t.snapshot()
	if len(timings) == 0 {
		return
	}
	byPhase := make(map[string]devPhaseTiming, len(timings))
	for _, timing := range timings {
		byPhase[timing.Phase] = timing
	}
	parts := make([]string, 0, len(timings))
	for _, phase := range devPhaseOrder {
		timing, ok := byPhase[phase]
		if !ok {
			continue
		}
		if timing.Status == devPhaseFailed {
			if timing.Reason == "" {
				parts = append(parts, fmt.Sprintf("%s=failed", phase))
			} else {
				parts = append(parts, fmt.Sprintf("%s=failed (%s)", phase, timing.Reason))
			}
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", phase, formatDevPhaseDuration(timing.DurationMS)))
	}
	if len(parts) == 0 {
		return
	}
	if deploymentID == "" {
		_, _ = fmt.Fprintf(w, "dev phases: %s\n", strings.Join(parts, " · "))
		return
	}
	_, _ = fmt.Fprintf(w, "dev phases (%s): %s\n", deploymentID, strings.Join(parts, " · "))
}

func formatDevPhaseDuration(durationMS int64) string {
	if durationMS < 0 {
		durationMS = 0
	}
	if durationMS < 1000 {
		return fmt.Sprintf("%dms", durationMS)
	}
	return (time.Duration(durationMS) * time.Millisecond).Round(100 * time.Millisecond).String()
}
