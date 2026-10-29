// adr: 570
package gateway

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// PublicRoutingViewSource returns an app's routing view from the gateway's
// refreshed copy (ADR-20261027085556); pkg/publicrouting implements it.
type PublicRoutingViewSource interface {
	View(context.Context, state.PublicRoutingViewKey) (state.PublicRoutingView, error)
}

// NewViewPublicRoutingPinner pins requests from view and falls back to exact
// only where the view cannot answer. It builds the snapshot exact would build
// from the same store state (ADR-20261027085556, ADR-20261028095914): the
// roster, its lane and the release verdict with a per-request canary split;
// and, for a pinned request, the host, release or revision verdict. A hosting
// smoke checks a candidate that is not live yet and keeps the exact read, as
// does a pin naming a deployment or graph that the view's bounded lists cut,
// or a revision pin on an app whose pin setting the view could not read.
func NewViewPublicRoutingPinner(view PublicRoutingViewSource, exact PublicRoutingPinner) PublicRoutingPinner {
	return func(ctx context.Context, app App, inputs PublicRoutingInputs) (PublicRoutingSnapshot, error) {
		if view == nil || !inputs.Valid || inputs.Smoke {
			return exact(ctx, app, inputs)
		}
		result := PublicRoutingSnapshot{AppID: app.ID, AccountID: app.AccountID, Scope: inputs.Scope,
			ReleaseRequested: inputs.RequestedReleaseID, RevisionID: inputs.RequestedRevisionID,
			HostDeploymentID: inputs.HostDeploymentID, HostScope: inputs.HostScope, Async: inputs.Async}
		current, err := view.View(ctx, state.PublicRoutingViewKey{AppID: app.ID, AccountID: app.AccountID, ProjectID: app.ProjectID, Scope: inputs.Scope})
		if err != nil {
			return result, err
		}
		now := time.Now()
		if inputs.HostDeploymentID != "" {
			allowed, ok := current.HostPinAllowed(inputs.HostScope, inputs.HostDeploymentID)
			if !ok {
				return exact(ctx, app, inputs)
			}
			result.HostChecked, result.HostAllowed = true, allowed
			return result, nil
		}
		if inputs.ResolveRelease {
			target := state.PublicRoutingReleaseTarget{}
			if inputs.RequestedReleaseID != "" {
				var ok bool
				if target, ok = current.Release.Requested(inputs.RequestedReleaseID, now); !ok {
					return exact(ctx, app, inputs)
				}
			} else {
				target.ID, target.DeploymentID, target.Err = current.Release.Resolve(uuid.NewString(), now)
			}
			result.ReleaseResolved, result.ReleaseVerdict = true, "allowed"
			result.ReleaseID, result.ReleaseDeploymentID = target.ID, target.DeploymentID
			switch {
			case errors.Is(target.Err, state.ErrNotFound):
				result.ReleaseVerdict = "gone"
			case errors.Is(target.Err, state.ErrConflict):
				result.ReleaseVerdict = "conflict"
			case target.Err != nil:
				return result, target.Err
			}
			if result.ReleaseVerdict != "allowed" || result.ReleaseDeploymentID != "" {
				return result, nil
			}
		}
		if inputs.RevisionPresent {
			allowed, ok := current.RevisionAllowed(inputs.Scope, inputs.RequestedRevisionID, now)
			if !ok {
				return exact(ctx, app, inputs)
			}
			result.RevisionChecked, result.RevisionAllowed = true, allowed
			return result, nil
		}
		if inputs.Async {
			return result, nil
		}
		for _, row := range current.Weights {
			result.Weights = append(result.Weights, DeploymentWeightsRow{ID: row.ID, TrafficPercent: row.TrafficPercent})
		}
		if current.Lane != "" && current.Lane != normalizedPublicRoutingScope(inputs.Scope) {
			result.RosterScope = current.Lane
		}
		return result, nil
	}
}

func normalizedPublicRoutingScope(scope string) string {
	if scope == "" {
		return state.DefaultEnvScope
	}
	return scope
}
