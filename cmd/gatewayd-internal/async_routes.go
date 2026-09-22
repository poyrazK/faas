package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

var asyncRouteInvocationNamespace = uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale.dev/async-route-invocation/v1"))

type asyncRouteInvocationStore interface {
	AppByID(context.Context, string) (state.App, error)
	EnqueueInvocation(context.Context, state.Invocation) (state.Invocation, error)
	InvocationByID(context.Context, string) (state.Invocation, error)
}

type asyncRouteEnqueuer struct {
	store asyncRouteInvocationStore
}

func (e *asyncRouteEnqueuer) EnqueueAsyncRoute(ctx context.Context, req gateway.AsyncRouteRequest) (gateway.AsyncRouteAccepted, error) {
	app, err := e.store.AppByID(ctx, req.AppID)
	if err != nil {
		return gateway.AsyncRouteAccepted{}, err
	}
	if req.AccountID == "" || app.AccountID != req.AccountID {
		return gateway.AsyncRouteAccepted{}, state.ErrNotFound
	}
	headers, err := json.Marshal(req.Headers)
	if err != nil {
		return gateway.AsyncRouteAccepted{}, err
	}
	invocationID := ""
	if req.IdempotencyKey != "" {
		invocationID = uuid.NewSHA1(asyncRouteInvocationNamespace, []byte(req.AppID+"\x00"+req.IdempotencyKey)).String()
	}
	retryPolicy := app.RetryPolicyJSON
	if string(retryPolicy) == "{}" {
		retryPolicy = nil
	}
	inv, err := e.store.EnqueueInvocation(ctx, state.Invocation{
		ID:              invocationID,
		AppID:           req.AppID,
		AccountID:       req.AccountID,
		Source:          state.InvocationAsyncInvoke,
		Method:          req.Method,
		Path:            req.Path,
		Payload:         append(json.RawMessage(nil), req.Payload...),
		Headers:         headers,
		DueAt:           time.Now().UTC(),
		RetryPolicyJSON: append(json.RawMessage(nil), retryPolicy...),
	})
	if err == nil {
		return gateway.AsyncRouteAccepted{ID: inv.ID}, nil
	}
	if !errors.Is(err, state.ErrConflict) || invocationID == "" {
		return gateway.AsyncRouteAccepted{}, err
	}
	existing, lookupErr := e.store.InvocationByID(ctx, invocationID)
	if lookupErr != nil || existing.AppID != req.AppID || existing.AccountID != req.AccountID {
		if lookupErr != nil {
			return gateway.AsyncRouteAccepted{}, lookupErr
		}
		return gateway.AsyncRouteAccepted{}, state.ErrConflict
	}
	return gateway.AsyncRouteAccepted{ID: existing.ID}, nil
}
