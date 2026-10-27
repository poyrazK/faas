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

// Ordinary reports a request with no host, smoke, release or revision pin.
// Its routing needs only the app's view; a pinned request needs an exact read.
func (i PublicRoutingInputs) Ordinary() bool {
	return i.Valid && i.HostDeploymentID == "" && !i.Smoke && i.RequestedReleaseID == "" && !i.ReleasePresent && !i.RevisionPresent
}

// NewViewPublicRoutingPinner pins ordinary requests from view and every other
// request with exact. The snapshot it builds is the one exact would build from
// the same store state: the roster, its lane, and the release verdict with a
// per-request canary split.
func NewViewPublicRoutingPinner(view PublicRoutingViewSource, exact PublicRoutingPinner) PublicRoutingPinner {
	return func(ctx context.Context, app App, inputs PublicRoutingInputs) (PublicRoutingSnapshot, error) {
		if view == nil || !inputs.Ordinary() {
			return exact(ctx, app, inputs)
		}
		result := PublicRoutingSnapshot{AppID: app.ID, AccountID: app.AccountID, Scope: inputs.Scope, Async: inputs.Async}
		current, err := view.View(ctx, state.PublicRoutingViewKey{AppID: app.ID, AccountID: app.AccountID, ProjectID: app.ProjectID, Scope: inputs.Scope})
		if err != nil {
			return result, err
		}
		if inputs.ResolveRelease {
			result.ReleaseResolved, result.ReleaseVerdict = true, "allowed"
			result.ReleaseID, result.ReleaseDeploymentID, err = current.Release.Resolve(uuid.NewString(), time.Now())
			switch {
			case errors.Is(err, state.ErrNotFound):
				result.ReleaseVerdict = "gone"
			case errors.Is(err, state.ErrConflict):
				result.ReleaseVerdict = "conflict"
			case err != nil:
				return result, err
			}
			if result.ReleaseVerdict != "allowed" || result.ReleaseDeploymentID != "" {
				return result, nil
			}
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
