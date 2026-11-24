package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// consumerPlanStore adapts the Postgres plan store to the gateway's plan
// admission gate (ADR-953).
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
		MaxUnitsPerMonth: policy.MaxUnitsPerMonth, RouteWeights: policy.RouteWeights, RouteLimits: gatewayRouteLimits(policy.RouteLimits),
		AlertThresholdsPercent: policy.AlertThresholdsPercent, SpendCapped: policy.SpendCapped,
		SpendCapMillicents: policy.SpendCapMillicents, SpendDueMillicents: policy.SpendDueMillicents,
		SpendCurrency: policy.SpendCurrency, OverdueBlocked: policy.OverdueBlocked, OverdueDueAt: policy.OverdueDueAt}, err
}

func (a consumerPlanStore) AdmitConsumerPlanRequest(ctx context.Context, accountID, consumerID string, policy gateway.ConsumerPlanPolicy, route string, units int64) (gateway.ConsumerPlanDecision, error) {
	decision, err := a.store.AdmitAPIConsumerPlanRequest(ctx, accountID, consumerID, state.APIConsumerPlanPolicy{
		PlanID: policy.PlanID, AppID: policy.AppID, MaxRequestsPerMinute: policy.MaxRequestsPerMinute,
		MaxUnitsPerMonth: policy.MaxUnitsPerMonth, RouteWeights: policy.RouteWeights, RouteLimits: stateRouteLimits(policy.RouteLimits),
		AlertThresholdsPercent: policy.AlertThresholdsPercent,
	}, route, units)
	return gateway.ConsumerPlanDecision{Allowed: decision.Allowed, Scope: decision.Scope, Limit: decision.Limit,
		Observed: decision.Observed, RetryAfterSeconds: decision.RetryAfterSeconds}, err
}

func gatewayRouteLimits(in map[string]state.APIConsumerPlanRouteLimit) map[string]api.APIConsumerPlanRouteLimit {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]api.APIConsumerPlanRouteLimit, len(in))
	for route, limit := range in {
		out[route] = api.APIConsumerPlanRouteLimit{MaxRequestsPerMinute: limit.MaxRequestsPerMinute, MaxRequestsPerMonth: limit.MaxRequestsPerMonth}
	}
	return out
}

func stateRouteLimits(in map[string]api.APIConsumerPlanRouteLimit) map[string]state.APIConsumerPlanRouteLimit {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]state.APIConsumerPlanRouteLimit, len(in))
	for route, limit := range in {
		out[route] = state.APIConsumerPlanRouteLimit{MaxRequestsPerMinute: limit.MaxRequestsPerMinute, MaxRequestsPerMonth: limit.MaxRequestsPerMonth}
	}
	return out
}
