package operatorobs

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

// RuntimeBuildAdmission bounds source-backed collections across all operators
// using one apid. Its zero value is ready to use, independent of metrics wiring.
// There is no waiting queue, shared result or cached evidence.
type RuntimeBuildAdmission struct{ active atomic.Int64 }

func (g *RuntimeBuildAdmission) Active() int64 {
	if g == nil {
		return 0
	}
	return g.active.Load()
}

// Collect holds capacity until collection and preview fitting have unwound.
// Cancellation does not release a slot while its source work is still running.
// Unselected and already-canceled reads preserve their existing no-source path.
func (g *RuntimeBuildAdmission) Collect(ctx context.Context, client *promql.Client, registry RuntimeBuildRegistry, selected RuntimeBuildSelection, now time.Time, metrics *RuntimeDiagnosticsMetrics) (RuntimeBuildsLoopResponse, *api.Problem) {
	if selected.role == "" {
		return metrics.RuntimeBuilds(ctx, client, registry, selected, now), nil
	}
	if ctx.Err() != nil {
		return metrics.RuntimeBuilds(ctx, nil, nil, selected, now), nil
	}
	if observed, admitted := g.tryAcquire(); !admitted {
		if metrics != nil {
			metrics.refused.Inc()
		}
		return RuntimeBuildsLoopResponse{}, api.ErrObsRuntimeBusy(observed)
	}
	defer g.active.Add(-1)
	return metrics.RuntimeBuilds(ctx, client, registry, selected, now), nil
}

func (g *RuntimeBuildAdmission) tryAcquire() (int64, bool) {
	for {
		active := g.active.Load()
		if active >= api.ObsRuntimeBuildCollectionsMax {
			return active, false
		}
		if g.active.CompareAndSwap(active, active+1) {
			return active + 1, true
		}
	}
}
