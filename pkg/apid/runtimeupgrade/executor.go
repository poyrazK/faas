// Package runtimeupgrade provides private apid runtime upgrade orchestration.
// No customer route or daemon startup enables it. It writes only intent/state;
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

// Run uses durable polling, so notifications and process-local phase state are
// never required for recovery. Infrastructure errors stop this invocation;
// restarting it retries after the abandoned lease expires.
func (e Executor) Run(ctx context.Context) error {
	ticker := time.NewTicker(api.RuntimeUpgradeOperationInterval)
	defer ticker.Stop()
	for {
		if _, err := e.RunOnce(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
