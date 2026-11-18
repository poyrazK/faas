package reconcile

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// applyWorkloadCreateSettings applies the settings a repository declares for
// a new app (retail-store feedback A5 and the Compose public-auth key,
// 2026-11-17). An existing app keeps what it has; `gregale app update`
// changes it.
//
//   - StartupDeadlineS (x-gregale-startup-deadline or healthcheck timing) is
//     capped at the plan's maximum, which the manifest validation enforces.
//   - PublicAuthMode open serves the public URL without a key on any plan;
//     bearer is honoured where the plan allows it (not Free).
func applyWorkloadCreateSettings(app *state.App, w reposcan.Workload, plan api.Plan) {
	if w.StartupDeadlineS > 0 {
		deadline := w.StartupDeadlineS
		if limits, ok := api.LimitsFor(plan); ok && limits.MaxStartupDeadlineS > 0 {
			deadline = min(deadline, limits.MaxStartupDeadlineS)
		}
		deadline = min(deadline, api.MaxAppManifestStartupDeadlineS)
		if deadline > api.ColdBootReadyWindowS {
			app.Manifest.StartupDeadlineS = deadline
		}
	}
	switch w.PublicAuthMode {
	case api.AppPublicAuthModeOpen:
		app.PublicAuthMode, app.RequireAuthn = api.AppPublicAuthModeOpen, false
	case api.AppPublicAuthModeBearer:
		if plan != api.PlanFree {
			app.PublicAuthMode, app.RequireAuthn = api.AppPublicAuthModeBearer, true
		}
	}
}
