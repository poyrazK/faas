package state

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// routeMonitorTelemetryUnavailable is the production monitor's reason for
// evidence a gateway may have lost (ADR-20261028110044 amendment 1): a
// report reason every client already accepts.
const routeMonitorTelemetryUnavailable = "telemetry_unavailable"

// pgRouteTelemetryLost reports whether a gateway may have lost request
// telemetry in the closed windows route evidence reads at now. Evidence over
// a gap would compare partial counts, so callers report it unavailable.
func pgRouteTelemetryLost(ctx context.Context, db sqlc.DBTX, now time.Time) (bool, error) {
	windows := routehealth.Windows(now)
	lost, err := sqlc.New().RouteTelemetryLost(ctx, db, sqlc.RouteTelemetryLostParams{
		WindowFrom: pgTimestamptzUTC(windows[0].Start), WindowUntil: pgTimestamptzUTC(windows[len(windows)-1].End),
	})
	if err != nil {
		return false, fmt.Errorf("read request telemetry coverage: %w", err)
	}
	return lost, nil
}
