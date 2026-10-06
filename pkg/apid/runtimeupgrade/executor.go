// Package runtimeupgrade provides private apid runtime upgrade orchestration.
// No customer route enables it. Private worker startup requires an explicit
// flag, with no shipped service enabling it. It writes only intent/state;
// builderd, imaged and schedd perform their existing build/prime pipeline.
package runtimeupgrade

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type Executor struct {
	Store state.RuntimeUpgradeOperationStore
	// Observe runs after a bounded iteration, including recoverable errors.
	// Worker wiring uses this for liveness and sanitized failure telemetry.
	Observe func(error)
}

// RunOnce claims at most one operation. A lost response is safe: queue/phase
// and cutover/completion commit atomically; an abandoned lease expires.
func (e Executor) RunOnce(ctx context.Context) (bool, error) {
	if e.Store == nil {
		return false, errors.New("runtime upgrade executor: store unavailable")
	}
	stepCtx, cancel := context.WithTimeout(ctx, api.RuntimeUpgradeOperationLease)
	defer cancel()
	claim, err := e.Store.ClaimRuntimeUpgradeOperation(stepCtx)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("runtime upgrade executor: claim: %w", err)
	}
	if _, err := e.Store.AdvanceRuntimeUpgradeOperation(stepCtx, claim); err != nil {
		return true, fmt.Errorf("runtime upgrade executor: advance: %w", err)
	}
	return true, nil
}

// Run supervises durable polling with capped backoff. A failed iteration leaves
// its lease to expire; the next claim never resumes a stale token. No process
// state or notification is required for recovery. Cancellation stops promptly.
func (e Executor) Run(ctx context.Context) error {
	if e.Store == nil {
		return errors.New("runtime upgrade executor: store unavailable")
	}
	delay := api.RuntimeUpgradeOperationInterval
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := e.RunOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e.Observe != nil {
			e.Observe(err)
		}
		wait := api.RuntimeUpgradeOperationInterval
		if err != nil {
			wait = delay
			delay = min(2*delay, api.RuntimeUpgradeWorkerRetryMax)
		} else {
			delay = api.RuntimeUpgradeOperationInterval
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
