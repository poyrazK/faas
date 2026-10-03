package main

import (
	"context"
	"github.com/onebox-faas/faas/pkg/gateway"
)

type asyncRouteEnqueuer struct {
	store gateway.AsyncRouteInvocationStore
}

func (e *asyncRouteEnqueuer) EnqueueAsyncRoute(ctx context.Context, req gateway.AsyncRouteRequest) (gateway.AsyncRouteAccepted, error) {
	return gateway.EnqueueAsyncRoute(ctx, e.store, req)
}
