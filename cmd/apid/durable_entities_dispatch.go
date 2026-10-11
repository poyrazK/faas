package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/state"
	pkgtrace "github.com/onebox-faas/faas/pkg/trace"
)

func (s *server) invokeDurableEntityHandler(ctx context.Context, acct state.Account, app state.App, scope string, id durableentity.ID, request api.DurableEntityInvokeRequest, view durableentity.View) (durableentity.Transition, error) {
	return s.dispatchDurableEntityHandler(ctx, acct, app, scope, id, request, view, "invoke")
}

func (s *server) dispatchDurableEntityHandler(ctx context.Context, acct state.Account, app state.App, scope string, id durableentity.ID, request api.DurableEntityInvokeRequest, view durableentity.View, event string) (durableentity.Transition, error) {
	inv, version, err := s.durableEntityInvocationVersion(ctx, app, scope, id)
	if err != nil {
		return durableentity.Transition{}, err
	}
	envelope := durableentity.HandlerRequest{ProtocolVersion: api.DurableEntityProtocolVersion, Event: event, Entity: id, RequestID: request.RequestID, Payload: request.Payload, State: view, DeploymentID: version.DeploymentID}
	if s.durableEntityOutboxEnabled && s.durableEntityOutboxHandlersEnabled {
		envelope.ProtocolVersion, envelope.Limits = api.DurableEntityOutboxProtocolVersion, durableentity.OutboxHandlerLimits()
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return durableentity.Transition{}, durableentity.ErrLimit
	}
	if len(body) > api.MaxDurableEntityInvocationBytes {
		return durableentity.Transition{}, &durableentity.LimitError{Budget: "invocation_bytes", Limit: api.MaxDurableEntityInvocationBytes, Observed: int64(len(body))}
	}
	final, err := s.enqueueDurableEntityGuest(ctx, acct, inv, body)
	if err != nil {
		return durableentity.Transition{}, err
	}

	return s.durableEntityHandlerTransition(ctx, id, final.Result, envelope.ProtocolVersion)
}

func (s *server) enqueueDurableEntityGuest(ctx context.Context, acct state.Account, inv state.Invocation, body []byte) (state.Invocation, error) {
	inv.Payload = body
	deadline, ok := ctx.Deadline()
	if !ok {
		return state.Invocation{}, context.DeadlineExceeded
	}
	inv.DeadlineAt, inv.DueAt = &deadline, time.Now().UTC()
	inv.ResultRetentionUntil = retentionForRequest(nil, acct)
	inv.RetryPolicyJSON, _ = json.Marshal(api.RetryPolicyDTO{MaxAttempts: 1})
	queued, problem := s.enqueuePreparedInvocation(ctx, inv, "enqueue entity handler")
	if problem != nil {
		return state.Invocation{}, problem
	}
	final, err := s.waitDurableEntityHandler(ctx, queued.ID)
	if err != nil {
		return state.Invocation{}, err
	}
	return final, nil
}

func (s *server) durableEntityInvocationVersion(ctx context.Context, app state.App, scope string, id durableentity.ID) (state.Invocation, state.InvocationVersion, error) {
	inv := state.Invocation{AccountID: app.AccountID, AppID: app.ID, PlatformTenantID: id.TenantID, Source: state.InvocationAsyncInvoke, Method: http.MethodPost, Path: api.DurableEntityHandlerPath, DeploymentScope: scope}
	if app.ProjectID != "" && app.PreviewOfSlug == "" {
		env, err := s.store.ProjectEnvironmentBySlug(ctx, app.AccountID, app.ProjectID, scope)
		if err != nil {
			return inv, state.InvocationVersion{}, durableEntityVersionProblem(err)
		}
		if env.ID != id.EnvironmentID {
			return inv, state.InvocationVersion{}, durableEntityVersionProblem(state.ErrConflict)
		}
		if scope != "production" && scope != state.DefaultEnvScope {
			inv.EnvironmentID = id.EnvironmentID
		}
	}
	headers, err := s.durableEntityVersionHeaders(ctx, app, scope)
	if err != nil {
		return inv, state.InvocationVersion{}, durableEntityVersionProblem(err)
	}
	inv.Headers, err = pkgtrace.MergeHeaders(ctx, headers)
	if err != nil {
		return inv, state.InvocationVersion{}, api.ErrCapacity("prepare entity handler headers")
	}
	prepared, version, err := state.ResolveInvocationVersionForEnvironment(ctx, s.store, inv, scope)
	if err != nil || version.DeploymentID == "" {
		return inv, version, durableEntityVersionProblem(err)
	}
	return prepared, version, nil
}

func (s *server) durableEntityVersionHeaders(ctx context.Context, app state.App, scope string) (json.RawMessage, error) {
	if app.ProjectID != "" && app.PreviewOfSlug == "" {
		resolver, ok := s.store.(state.ProjectReleaseSetStore)
		if !ok {
			return nil, state.ErrConflict
		}
		release, _, err := resolver.ResolveProjectRelease(ctx, app.ID, scope, "")
		if err != nil {
			return nil, err
		}
		if release != "" {
			return json.Marshal(map[string]string{api.ReleaseHeader: release, "Content-Type": "application/json"})
		}
	}
	dep, err := s.store.LiveDeploymentForScope(ctx, app.ID, scope)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{api.RevisionHeader: dep.ID, "Content-Type": "application/json"})
}

func durableEntityVersionProblem(err error) *api.Problem {
	if err != nil && !errors.Is(err, state.ErrNotFound) && !errors.Is(err, state.ErrConflict) && !errors.Is(err, state.ErrInvalidArgument) {
		return api.ErrCapacity("resolve entity deployment")
	}
	return api.NewProblem(http.StatusConflict, "durable_entity_deployment_unavailable", "Entity deployment unavailable", "a live deployment with revision pinning or a usable project release is required in the selected environment")
}

func (s *server) waitDurableEntityHandler(ctx context.Context, invocationID string) (state.Invocation, error) {
	// Poll the durable execution row so a lost completion notification cannot
	// make a successful handler invisible. Entity publication still occurs only
	// in the bucket; a late execution result has no claim and cannot commit.
	timer := time.NewTicker(api.DurableEntityResultPollInterval)
	defer timer.Stop()
	for {
		inv, err := s.store.InvocationByID(ctx, invocationID)
		if err != nil {
			return inv, api.ErrCapacity("read entity handler result")
		}
		switch inv.State {
		case state.InvocationCompleted:
			return inv, nil
		case state.InvocationFailed, state.InvocationDeadLetter, state.InvocationCancelled:
			return inv, api.NewProblem(http.StatusBadGateway, "durable_entity_handler_failed", "Entity handler failed", "the handler did not produce a successful transition; retry with the same request_id and payload")
		}
		select {
		case <-ctx.Done():
			return inv, ctx.Err()
		case <-timer.C:
		}
	}
}
