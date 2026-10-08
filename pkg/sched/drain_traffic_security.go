// adr: 570
package sched

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
)

// The delivery owns these registrations until gateway forwarding/result
// cleanup ends. Its original scheduler context still owns durable claim writes.
type invocationTraffic struct {
	ctx      context.Context
	cancel   context.CancelCauseFunc
	registry *trafficrevocation.Registry
	states   map[trafficrevocation.Scope]trafficrevocation.State
	releases []func()
}

func WithDrainTrafficRevocations(registry *trafficrevocation.Registry) DrainOption {
	return func(d *Drain) { d.trafficRevocations = registry }
}

func (d *Drain) prepareInvocationTraffic(ctx context.Context, inv state.Invocation) (context.Context, state.Invocation, state.InvocationVersion, *invocationTraffic, error) {
	if d.trafficRevocations == nil { // Legacy in-process drains only.
		prepared, version, err := state.ResolveInvocationVersion(ctx, d.store, inv)
		return ctx, prepared, version, nil, err
	}
	prepared, version, owner, err := state.ResolveInvocationDispatch(ctx, d.store, inv, nil)
	if err != nil {
		return ctx, inv, state.InvocationVersion{}, nil, err
	}
	ctx, cancel := context.WithCancelCause(ctx)
	traffic := &invocationTraffic{ctx: ctx, cancel: cancel, registry: d.trafficRevocations, states: make(map[trafficrevocation.Scope]trafficrevocation.State)}
	scopes := []trafficrevocation.Scope{{Kind: "account", ID: owner}, {Kind: "app", ID: inv.AppID}}
	if version.DeploymentID != "" {
		scopes = append(scopes, trafficrevocation.Scope{Kind: "deployment", ID: version.DeploymentID})
	}
	if err := traffic.add(scopes); err != nil {
		traffic.close()
		return ctx, inv, state.InvocationVersion{}, nil, err
	}
	return ctx, prepared, version, traffic, nil
}

func (traffic *invocationTraffic) add(scopes []trafficrevocation.Scope) error {
	fresh := make([]trafficrevocation.Scope, 0, len(scopes))
	seen := make(map[trafficrevocation.Scope]bool, len(scopes))
	for _, scope := range scopes {
		if _, exists := traffic.states[scope]; !exists && !seen[scope] {
			fresh = append(fresh, scope)
			seen[scope] = true
		}
	}
	if len(traffic.states)+len(fresh) > api.TrafficSecurityMaxRequestScopes {
		return trafficrevocation.ErrUnavailable
	}
	if len(fresh) == 0 {
		return context.Cause(traffic.ctx)
	}
	states, release, err := traffic.registry.AdmitSnapshot(traffic.ctx, fresh, traffic.cancel)
	if err != nil {
		return err
	}
	for scope, state := range states {
		traffic.states[scope] = state
	}
	traffic.releases = append(traffic.releases, release)
	return context.Cause(traffic.ctx)
}

func (traffic *invocationTraffic) verify() error {
	if traffic == nil {
		return nil
	}
	release, err := traffic.registry.AdmitAt(traffic.ctx, traffic.states, traffic.cancel)
	if err != nil {
		return err
	}
	release()
	return context.Cause(traffic.ctx)
}

func (traffic *invocationTraffic) handoff(ctx context.Context) (context.Context, error) {
	value, err := trafficrevocation.EncodeAdmittedSnapshot(traffic.states)
	if err != nil {
		return ctx, err
	}
	return trafficrevocation.WithHandoffSnapshot(ctx, value)
}

func (traffic *invocationTraffic) close() {
	if traffic == nil {
		return
	}
	for _, release := range traffic.releases {
		release()
	}
	traffic.cancel(nil)
}

// The coordinator owns its bounded leader. A revoked delivery can stop waiting
// even when it happened to initiate that shared wake; other callers still join it.
func (d *Drain) awaitInvocationWake(ctx context.Context, appID string) (CoordOutcome, error) {
	type result struct {
		out CoordOutcome
		err error
	}
	finished := make(chan result, 1)
	go func() {
		if err := context.Cause(ctx); err != nil {
			finished <- result{err: err}
			return
		}
		out, err := d.engine.EnsureWake(ctx, appID, TriggerMeterd)
		finished <- result{out: out, err: err}
	}()
	select {
	case <-ctx.Done():
		return CoordOutcome{}, context.Cause(ctx)
	case result := <-finished:
		return result.out, result.err
	}
}

func (d *Drain) verifyInvocationWake(ctx context.Context, inv state.Invocation, wake WakeResult, traffic *invocationTraffic) (state.Invocation, error) {
	claim := state.InvocationTarget{InstanceID: wake.InstanceID, NodeID: wake.NodeID, DeploymentID: wake.DeploymentID}
	prepared, _, _, err := state.ResolveInvocationDispatch(ctx, d.store, inv, &claim)
	if err != nil {
		return inv, err
	}
	if traffic != nil {
		if err := traffic.add([]trafficrevocation.Scope{{Kind: "deployment", ID: wake.DeploymentID}}); err != nil {
			return inv, err
		}
		if err := traffic.verify(); err != nil {
			return inv, err
		}
	}
	return prepared, nil
}
