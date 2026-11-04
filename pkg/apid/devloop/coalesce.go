package devloop

import (
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// CoalesceWindowEnv overrides api.DevBuildCoalesceWindow in seconds; 0 turns
// build coalescing off. Tests use a short window.
const CoalesceWindowEnv = "FAAS_DEV_BUILD_COALESCE_SECONDS"

// CoalesceWindow is the developer build coalescing window (ADR-20261021221906). A
// malformed or negative override keeps the default.
func CoalesceWindow(getenv func(string) string) time.Duration {
	raw := strings.TrimSpace(getenv(CoalesceWindowEnv))
	if raw == "" {
		return api.DevBuildCoalesceWindow
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 0 {
		return api.DevBuildCoalesceWindow
	}
	return time.Duration(seconds) * time.Second
}

// BuildNotBefore says until when a developer sync's convergence build can
// wait, or the zero time when it must build now. It can wait only when the
// sync is about to reach the running instance as a live patch: eligible,
// with changes, and with delivery on. A sync the developer asked to build
// now (gregale dev --once, a failed patch) never waits.
func BuildNotBefore(now time.Time, preview *api.DevPatchPreview, deliveryEnabled, buildNow bool, window time.Duration) time.Time {
	if preview == nil || !preview.Eligible || preview.ChangedPaths == 0 || !deliveryEnabled || buildNow || window <= 0 {
		return time.Time{}
	}
	return now.Add(window).UTC()
}
