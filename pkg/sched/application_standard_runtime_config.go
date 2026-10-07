// adr: 595 — publish owned runtime configuration and native receipts atomically.
package sched

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

func (e *Engine) publishOwnedRuntimeWithStandards(ctx context.Context, out *WakeOutcome, p state.RuntimeInstancePublication) (state.Instance, error) {
	if out == nil {
		return state.Instance{}, runtimeadmission.ErrInvalid
	}
	if err := e.checkManagedPostgresAdmission(ctx, p.AppID); err != nil {
		return state.Instance{}, err
	}
	p.AdmissionReceipt = out.RuntimeAdmissionReceipt
	return e.store.PublishOwnedInstanceRuntime(ctx, p)
}

func (e *Engine) publishOwnedWarmWithStandards(ctx context.Context, receipt *runtimeadmission.Receipt, p state.RuntimeInstancePublication) (state.Instance, error) {
	p.PromotionReceipt = receipt
	fresh, err := e.store.PublishOwnedInstanceRuntime(ctx, p)
	if err == nil || receipt == nil {
		return fresh, err
	}
	// Retry the exact retained receipt after a lost commit acknowledgment.
	retryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ApplicationStandardRuntimeCleanupTimeout)
	defer cancel()
	return e.store.PublishOwnedInstanceRuntime(retryCtx, p)
}
