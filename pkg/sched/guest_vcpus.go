// adr: 20261109172421

package sched

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// An app instance's CPU follows its size, never its plan
// (ADR-20261109172421): one guest vCPU per GB of instance RAM, a cpu.weight
// of one per 16 MB, no CPU quota unless the app chose 250 or 500 millicores,
// and a booking of its RAM's CPU baseline. Builders, jobs, data-repair
// sandboxes and agent runs keep their own CPU rules.

// appVCPUs is the guest vCPU count an instance of app boots and books with.
func appVCPUs(app state.App) int {
	return api.SizeVCPUs(app.RAMMB)
}

// appCPUWeight is the cpu.weight an app instance's AppSpec carries. The
// AppSpec also sets CPUUncapped: no quota while the app's ceiling is the
// default; vmmd keeps a 250 or 500 millicore choice as a cap.
func appCPUWeight(app state.App) int32 {
	return int32(api.SizeCPUWeight(app.RAMMB)) //nolint:gosec // bounded by SizeCPUWeightMax
}

// appCPUMillicores is the CPU ceiling an app instance's AppSpec and live CPU
// policy carry: the app's own value. vmmd runs the default without a quota.
func appCPUMillicores(app state.App) int {
	return effectiveAppCPUMillicores(app)
}

// appCPUBookingMillicores is what an app instance books against a node's
// CPU, and its share of a busy host: the baseline for its own RAM, never more
// than it can use (its vCPUs, or the cap it chose).
func appCPUBookingMillicores(app state.App) int {
	usable := appVCPUs(app) * 1000
	if chosen := effectiveAppCPUMillicores(app); chosen < api.DefaultAppCPUMillicores {
		usable = chosen
	}
	baseline := app.RAMMB * api.AppCPUBaselineMillicoresPerGB / 1024
	return max(1, min(usable, baseline, api.AppCPUBookingMaxMillicores))
}
