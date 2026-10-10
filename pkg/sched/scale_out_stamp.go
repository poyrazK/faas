package sched

import (
	"context"
	"sync"
	"time"
)

// scaleOutStampTimeout bounds the background ADR-590 stamp. It is best-effort:
// a slow or failed stamp only means the next wake's cooldown consult sees an
// older stamp, the safe direction (see admitAndDispatchWithOptions).
const scaleOutStampTimeout = 5 * time.Second

// startScaleOutStamp records the deployment's scale-out stamp in the
// background and returns an idempotent join. The wake joins it before runtime
// publication so the two transactions never contend for the same row locks.
func (e *Engine) startScaleOutStamp(ctx context.Context, appID, deploymentID string) func() {
	done := make(chan struct{})
	go func() {
		defer close(done)
		stampCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), scaleOutStampTimeout)
		defer cancel()
		if err := e.store.StampDeploymentScaleOut(stampCtx, deploymentID); err != nil {
			e.log.Warn("sched: stamp original environment scale-out failed", "app", appID, "err", err)
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { <-done }) }
}
