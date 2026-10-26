// adr: 570
package main

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// newPublicRoutingPinner verifies the app's ADR-570 host-policy snapshot in
// the same view as the deployment roster (FAAS_TRAFFIC_AUTHORITATIVE_HOST_POLICY).
func newPublicRoutingPinner(store state.PublicRoutingSnapshotStore) gateway.PublicRoutingPinner {
	return publicRoutingPinner(store, true)
}

// publicRoutingPinnerFor is the production wiring. With the default cached
// host policy a host resolves from the route cache and carries no host-policy
// snapshot to re-verify, so requiring one refused every public request. The
// view still verifies the owner and pins the deployment roster.
func publicRoutingPinnerFor(store state.PublicRoutingSnapshotStore, cachedHostPolicy bool) gateway.PublicRoutingPinner {
	return publicRoutingPinner(store, !cachedHostPolicy)
}

func publicRoutingPinner(store state.PublicRoutingSnapshotStore, verifyHostPolicy bool) gateway.PublicRoutingPinner {
	return func(ctx context.Context, app gateway.App, inputs gateway.PublicRoutingInputs) (gateway.PublicRoutingSnapshot, error) {
		result := gateway.PublicRoutingSnapshot{AppID: app.ID, AccountID: app.AccountID, Scope: inputs.Scope,
			ReleaseRequested: inputs.RequestedReleaseID, RevisionID: inputs.RequestedRevisionID,
			HostDeploymentID: inputs.HostDeploymentID, HostScope: inputs.HostScope, Async: inputs.Async}
		if store == nil || !isAppID(app.ID) || !isAppID(app.AccountID) || app.ProjectID != "" && !isAppID(app.ProjectID) {
			return result, errors.New("public routing owner is unavailable")
		}
		err := store.WithPublicRoutingSnapshot(ctx, func(reader state.PublicRoutingPolicyReader) error {
			if err := reader.VerifyPublicRoutingOwner(ctx, app.ID, app.AccountID, app.ProjectID); err != nil {
				return err
			}
			if verifyHostPolicy {
				if err := verifyPublicHostPolicy(ctx, reader, app); err != nil {
					return err
				}
			}
			if inputs.HostDeploymentID != "" {
				result.HostChecked = true
				var err error
				if inputs.Smoke {
					// The candidate is not live yet, and its scope need not be the host's.
					result.HostAllowed, err = reader.PublicSmokeCandidateAllowed(ctx, app.ID, inputs.HostDeploymentID)
					return err
				}
				result.HostAllowed, err = reader.PublicHostPinAllowed(ctx, app.ID, inputs.HostScope, inputs.HostDeploymentID)
				return err
			}
			if inputs.ResolveRelease {
				if err := readPublicReleaseSnapshot(ctx, reader, app.ID, inputs, &result); err != nil {
					return err
				}
				if result.ReleaseVerdict != "allowed" || result.ReleaseDeploymentID != "" {
					return nil
				}
			}
			if inputs.RevisionPresent {
				result.RevisionChecked = true
				var err error
				result.RevisionAllowed, err = reader.PublicRevisionAllowed(ctx, app.ID, inputs.Scope, inputs.RequestedRevisionID)
				return err
			}
			if inputs.Async {
				return nil
			}
			rows, err := reader.PublicDeploymentWeights(ctx, app.ID, inputs.Scope)
			if err != nil {
				return err
			}
			for _, row := range rows {
				result.Weights = append(result.Weights, gateway.DeploymentWeightsRow{ID: row.ID, TrafficPercent: row.TrafficPercent})
			}
			return nil
		})
		return result, err
	}
}

func readPublicReleaseSnapshot(ctx context.Context, reader state.PublicRoutingPolicyReader, app string, inputs gateway.PublicRoutingInputs, result *gateway.PublicRoutingSnapshot) error {
	result.ReleaseResolved, result.ReleaseVerdict = true, "allowed"
	var err error
	if canary, ok := reader.(interface {
		ResolvePublicProjectCanary(context.Context, string, string, string) (string, string, error)
	}); ok && inputs.RequestedReleaseID == "" {
		// A running project canary splits unpinned traffic in the same view.
		result.ReleaseID, result.ReleaseDeploymentID, err = canary.ResolvePublicProjectCanary(ctx, app, inputs.Scope, uuid.NewString())
	} else {
		result.ReleaseID, result.ReleaseDeploymentID, err = reader.ResolvePublicProjectRelease(ctx, app, inputs.Scope, inputs.RequestedReleaseID)
	}
	switch {
	case errors.Is(err, state.ErrNotFound):
		result.ReleaseVerdict = "gone"
	case errors.Is(err, state.ErrConflict):
		result.ReleaseVerdict = "conflict"
	case err != nil:
		return err
	}
	return nil
}
