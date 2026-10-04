// adr: 531
package main

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func loadServiceRoutingSnapshot(ctx context.Context, source state.ServicePolicyReader, policy gateway.ServicePolicySnapshot) (*gateway.ServiceRoutingSnapshot, error) {
	inputs, present := gateway.ServicePolicyRoutingInputsFromContext(ctx)
	if !present || inputs.Probe || !inputs.ReleaseValid ||
		(inputs.ReleasePresent && (!inputs.ResolveRelease || inputs.CallerDeploymentID == "")) ||
		(policy.Caller.RequireHTTPS && !inputs.Secure) ||
		(policy.Caller.CallScope != nil && !policy.Caller.CallScope.Allows(inputs.Method, inputs.TargetPath)) {
		return nil, nil
	}
	reader, ok := source.(state.ServiceRoutingPolicyReader)
	if !ok {
		return nil, gateway.ErrServiceProxyUnavailable
	}
	routing := &gateway.ServiceRoutingSnapshot{CallerDeploymentID: inputs.CallerDeploymentID}
	if inputs.ResolveRelease {
		if err := readServiceReleaseSnapshot(ctx, reader, policy, inputs, routing); err != nil {
			return nil, err
		}
		// Known refusals are pinned verdicts; storage errors abort the snapshot.
		if routing.ReleaseVerdict != "allowed" || routing.ReleaseDeploymentID != "" {
			return routing, nil
		}
	}
	if inputs.OverridePresent {
		routing.OverrideDeploymentID = inputs.OverrideDeploymentID
		if inputs.OverrideValid {
			routing.OverrideChecked = true
			var err error
			routing.OverrideAllowed, err = reader.ServiceDeploymentOverrideAllowed(ctx, policy.Target.AppID, inputs.OverrideDeploymentID)
			if err != nil {
				return nil, err
			}
		}
		return routing, nil
	}
	weights, err := reader.ServiceDeploymentWeights(ctx, policy.Target.AppID)
	if err != nil {
		return nil, err
	}
	for _, weight := range weights {
		routing.Weights = append(routing.Weights, gateway.DeploymentWeightsRow{ID: weight.ID, TrafficPercent: weight.TrafficPercent})
	}
	return routing, nil
}

func readServiceReleaseSnapshot(ctx context.Context, reader state.ServiceRoutingPolicyReader, policy gateway.ServicePolicySnapshot, inputs gateway.ServicePolicyRoutingInputs, routing *gateway.ServiceRoutingSnapshot) error {
	routing.ReleaseResolved = true
	routing.RequestedReleaseID, routing.ReleaseVerdict = inputs.RequestedReleaseID, "allowed"
	var err error
	routing.ReleaseID, routing.ReleaseDeploymentID, err = reader.ResolveServiceRelease(ctx, policy.Caller.AppID, inputs.CallerDeploymentID, policy.Target.AppID, inputs.RequestedReleaseID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		routing.ReleaseError, routing.ReleaseVerdict = gateway.ErrReleaseGone, "gone"
	case errors.Is(err, state.ErrConflict):
		routing.ReleaseError, routing.ReleaseVerdict = gateway.ErrReleaseConflict, "conflict"
	case err != nil:
		return err
	}
	return nil
}
