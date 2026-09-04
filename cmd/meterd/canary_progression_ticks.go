// cmd/meterd/canary_progression_ticks.go — canary_progression
// adapter (issue #976 / ADR-122 / SAFE-RELEASES-A).
//
// The tick driver lives on meter.Loop and is wired once from main.go. This
// file keeps only the narrow state.Store adapter needed by pkg/canary, so a
// second free-function ticker cannot accidentally run alongside the loop.
package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/canary"
	"github.com/onebox-faas/faas/pkg/state"
)

// canaryStoreAdapter bridges pkg/state.Store to pkg/canary.Store. It lives in
// cmd/meterd so pkg/canary stays free of the pkg/state import cycle.
type canaryStoreAdapter struct {
	store state.Store
}

func (a *canaryStoreAdapter) ListCanaryInFlight(ctx context.Context) ([]canary.CanaryRow, error) {
	deps, err := a.store.ListCanaryInFlight(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]canary.CanaryRow, 0, len(deps))
	for _, d := range deps {
		out = append(out, canary.CanaryRow{
			ID:                d.ID,
			AppID:             d.AppID,
			CanaryPreset:      d.CanaryPreset,
			CanaryStep:        d.CanaryStep,
			CanaryTotalSteps:  d.CanaryTotalSteps,
			CanaryStepStarted: canaryPtrTime(d.CanaryStepStartedAt),
			RolloutState:      d.RolloutState,
			CanaryStages:      d.CanaryStages,
		})
	}
	return out, nil
}

func canaryPtrTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
