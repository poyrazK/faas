package analyticsrollup

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
)

// OptionalRead gives raw-sample enrichment its own bounded budget. Counts
// from durable rollups remain usable when an optional source is unavailable.
func OptionalRead[T any](ctx context.Context, read func(context.Context) (T, error)) (T, error) {
	bounded, cancel := context.WithTimeout(ctx, api.RequestAnalyticsSampledMeasuresTimeout)
	defer cancel()
	return read(bounded)
}
