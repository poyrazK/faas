package main

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/gateway"
	schedpkg "github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// forkReader is what routing a fork request reads.
type forkReader interface {
	AppForkForApp(ctx context.Context, appID, forkID string) (state.AppFork, error)
	InstanceByID(ctx context.Context, id string) (state.Instance, error)
	DeploymentByID(ctx context.Context, id string) (state.Deployment, error)
	AppByID(ctx context.Context, id string) (state.App, error)
}

// forkAction is what creating a fork requires on its app. The fork routes
// have no reviewed organization permission, so apid falls back to the app's
// creator holding the owner/admin action (appaccess.authorize): a fork's
// creator is always the creator of its app.
const forkAction = authz.OrgActionManageAppSecurity

// forkTargetLoader routes a fork request only when the fork, its token and
// its instance all check out (gateway.ForkTargetFromState), and the creator
// still holds the app (appaccess.Standing). A fork is a copy of a running
// production instance, in-memory state included, reachable by a bearer token
// for up to its four-hour TTL, and the company cannot cancel a fork its
// creator made. So the token stops working as soon as the creator leaves the
// company, loses the permission, or the organization stops being active.
// Every refusal is the same not-found, so the route is not an oracle.
func forkTargetLoader(forks forkReader, access appaccess.Store, now func() time.Time) func(context.Context, string, string, string) (gateway.Target, bool, error) {
	return func(ctx context.Context, appID, forkID, token string) (gateway.Target, bool, error) {
		fork, err := forks.AppForkForApp(ctx, appID, forkID)
		if errors.Is(err, state.ErrNotFound) || (err == nil && fork.InstanceID == nil) {
			return gateway.Target{}, false, nil
		}
		if err != nil {
			return gateway.Target{}, false, err
		}
		instance, err := forks.InstanceByID(ctx, *fork.InstanceID)
		if errors.Is(err, state.ErrNotFound) {
			return gateway.Target{}, false, nil
		}
		if err != nil {
			return gateway.Target{}, false, err
		}
		dep, err := forks.DeploymentByID(ctx, instance.DeploymentID)
		if err != nil {
			return gateway.Target{}, false, err
		}
		target, ok := gateway.ForkTargetFromState(fork, instance, schedpkg.DeploymentRuntimePort(dep), token, now())
		if !ok {
			return gateway.Target{}, false, nil
		}
		app, err := forks.AppByID(ctx, appID)
		if errors.Is(err, state.ErrNotFound) {
			return gateway.Target{}, false, nil
		}
		if err != nil {
			return gateway.Target{}, false, err
		}
		standing, err := appaccess.Standing(ctx, access, app.AccountID, app, forkAction)
		if err != nil {
			return gateway.Target{}, false, err
		}
		return target, standing, nil
	}
}
