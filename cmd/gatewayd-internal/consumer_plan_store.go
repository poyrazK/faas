package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// consumerPlanStore adapts the Postgres plan store to the gateway's plan
// admission gate (ADR-974).
type consumerPlanStore struct{ store *state.PgStore }

func newConsumerPlanStore(store *state.PgStore) gateway.ConsumerPlanStore {
	if store == nil {
		return nil
	}
	return consumerPlanStore{store: store}
}

func (a consumerPlanStore) ConsumerPlanPolicy(ctx context.Context, accountID, appID, consumerID string) (gateway.ConsumerPlanPolicy, error) {
	policy, err := a.store.GetAPIConsumerPlanPolicy(ctx, accountID, appID, consumerID, time.Now().UTC())
	return gateway.ConsumerPlanPolicy{PlanID: policy.PlanID, AppID: policy.AppID, MaxRequestsPerMinute: policy.MaxRequestsPerMinute,
		MaxUnitsPerMonth: policy.MaxUnitsPerMonth, RouteWeights: policy.RouteWeights,
		AlertThresholdsPercent: policy.AlertThresholdsPercent}, err
}

func (a consumerPlanStore) AdmitConsumerPlanRequest(ctx context.Context, accountID, consumerID string, policy gateway.ConsumerPlanPolicy, units int64) (gateway.ConsumerPlanDecision, error) {
	decision, err := a.store.AdmitAPIConsumerPlanRequest(ctx, accountID, consumerID, state.APIConsumerPlanPolicy{
		PlanID: policy.PlanID, AppID: policy.AppID, MaxRequestsPerMinute: policy.MaxRequestsPerMinute,
		MaxUnitsPerMonth: policy.MaxUnitsPerMonth, RouteWeights: policy.RouteWeights,
		AlertThresholdsPercent: policy.AlertThresholdsPercent,
	}, units)
	return gateway.ConsumerPlanDecision{Allowed: decision.Allowed, Scope: decision.Scope, Limit: decision.Limit,
		Observed: decision.Observed, RetryAfterSeconds: decision.RetryAfterSeconds}, err
}
