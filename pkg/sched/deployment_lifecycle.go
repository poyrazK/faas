package sched

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// withBuiltLifecycle returns app with the before_checkpoint hook that its
// deployment was built with. imaged stamps the app's hook into each
// deployment's guest manifest at build time, and the guest only answers the
// callback its own layer declares. A later deploy that adds or removes the
// hook changes the app manifest but not older releases, so parks, primes and
// rollbacks of those releases must follow the deployment (Hunt 32 H32-49).
// Deployments built before imaged recorded the hook keep the app's hook.
func withBuiltLifecycle(app state.App, dep state.Deployment) state.App {
	if dep.ID == "" || !dep.BeforeCheckpointHookKnown {
		return app
	}
	if !dep.BeforeCheckpointHook {
		app.Manifest.BeforeCheckpoint = nil
		return app
	}
	if app.Manifest.BeforeCheckpoint == nil {
		// The guest holds the hook's path and timeout; the host only needs
		// to know that a callback is due before capture.
		app.Manifest.BeforeCheckpoint = &api.BeforeCheckpointHook{}
	}
	return app
}

// appForInstanceAndDeployment is state.AppForInstance that also returns the
// deployment row it read. The deployment is zero for an instance without one.
func appForInstanceAndDeployment(ctx context.Context, store interface {
	AppByID(context.Context, string) (state.App, error)
	DeploymentByID(context.Context, string) (state.Deployment, error)
}, ins state.Instance) (state.App, state.Deployment, error) {
	if ins.DeploymentID == "" {
		app, err := store.AppByID(ctx, ins.AppID)
		return app, state.Deployment{}, err
	}
	dep, err := store.DeploymentByID(ctx, ins.DeploymentID)
	if err != nil {
		return state.App{}, state.Deployment{}, err
	}
	app, err := state.AppForInstanceDeployment(ctx, store, ins, dep)
	return app, dep, err
}
