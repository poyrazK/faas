package sched

import (
	"context"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

type standardEgressRouter interface {
	UpdateAdmittedAppEgressPolicy(context.Context, runtimeadmission.Identity, runtimeadmission.EgressPolicy) (runtimeadmission.EgressReceipt, error)
}

// Independent repair runs on startup, each tick and app policy notifications.
// Legacy echoed revisions never populate the private standard observation table.
func (e *EgressDriftSubscriber) reconcileStandardEgress(ctx context.Context, appID string) {
	store, ok := e.engine.store.(state.ApplicationStandardEgressStore)
	if !ok {
		return
	}
	targets, err := store.ListPendingApplicationStandardEgress(ctx, appID)
	if err != nil {
		e.log.Warn("schedd: list standard egress targets failed", "err", err)
		return
	}
	for _, target := range targets {
		if ctx.Err() != nil {
			return
		}
		e.applyStandardEgress(ctx, store, target)
	}
}

func (e *EgressDriftSubscriber) applyStandardEgress(ctx context.Context, store state.ApplicationStandardEgressStore, t state.ApplicationStandardEgressTarget) {
	router, ok := e.router.(standardEgressRouter)
	if !ok {
		return
	}
	receipt, err := router.UpdateAdmittedAppEgressPolicy(ctx, t.Identity, t.Policy)
	if err == nil {
		_, err = store.RecordApplicationStandardEgress(ctx, t, receipt)
	}
	if err != nil {
		e.log.Warn("schedd: standard egress observation pending", "app", t.AppID, "node", t.Identity.NodeID, "revision", t.DesiredRevision, "err", err)
	}
}
