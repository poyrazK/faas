package state

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RouteLabelPins returns the route labels ("METHOD /path") an app's route
// health guard or production monitor selects. The gateway keeps their
// telemetry labels in a full route set (ADR-20261028110044 amendment 4).
func (s *PgStore) RouteLabelPins(ctx context.Context, appID string) ([]string, error) {
	labels, err := sqlc.New().RouteLabelPins(ctx, s.pool, appID)
	if err != nil {
		return nil, fmt.Errorf("read route label pins: %w", err)
	}
	return labels, nil
}
