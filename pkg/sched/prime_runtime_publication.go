package sched

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// primeRuntimeBusyBackoff is the first wait between prime publication
// attempts; tests shorten it.
var primeRuntimeBusyBackoff = api.PrimeRuntimePublicationBusyBackoff

// publishPrimeRuntime records a primed instance's runtime, waiting out a busy
// runtime fence. A busy refusal commits nothing (the whole transaction rolls
// back), so replaying the publication is safe; the store already replays it
// for about a second. On production-us rc.294 a deploy's own scan publication
// held the fence longer and the deploy failed with "runtime admission inputs
// are busy" after its VM had booted (Hunt 14 H14-31).
func (e *Engine) publishPrimeRuntime(ctx context.Context, out *WakeOutcome, p state.RuntimeInstancePublication) (state.Instance, error) {
	delay := primeRuntimeBusyBackoff
	for attempt := 1; ; attempt++ {
		primed, err := e.publishOwnedRuntimeWithStandards(ctx, out, p)
		if !errors.Is(err, state.ErrApplicationStandardRuntimeBusy) || attempt >= api.PrimeRuntimePublicationBusyAttempts {
			return primed, err
		}
		if e.log != nil {
			e.log.Info("sched: prime: runtime fences busy; retrying publication",
				"app", p.AppID, "instance", p.InstanceID, "attempt", attempt, "wait", delay)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return primed, errors.Join(err, ctx.Err())
		case <-timer.C:
		}
		delay = min(delay*2, api.PrimeRuntimePublicationBusyMaxBackoff)
	}
}
