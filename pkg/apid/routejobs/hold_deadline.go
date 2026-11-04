package routejobs

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// HoldPastDeadline reports how long the route health guard has held
// deploymentID and whether that passed api.RouteHealthHoldDeadline at now
// (ADR-20261028110044). An aborted canary or one not among holds is not held.
func HoldPastDeadline(holds []state.RouteHealthHoldAlert, deploymentID string, now time.Time) (time.Duration, bool) {
	for _, h := range holds {
		if h.DeploymentID != deploymentID || h.Status == "aborted" || h.UpdatedAt.IsZero() {
			continue
		}
		held := now.Sub(h.UpdatedAt)
		return held, held >= api.RouteHealthHoldDeadline
	}
	return 0, false
}
