package udpd

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync/atomic"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/ingressroute"
	"github.com/onebox-faas/faas/pkg/state"
)

// TargetSource provides authoritative app, intent and live-instance projections.
// The public edge cannot mutate lifecycle state through this interface.
type TargetSource interface {
	state.AppOwnerReader
	AppByID(context.Context, string) (state.App, error)
	UDPListenerByAppAndName(context.Context, string, string) (state.UDPListener, error)
	LiveDeployments(context.Context, string) ([]state.Deployment, error)
	ListInstancesForApp(context.Context, string) ([]state.Instance, error)
}
type Admitter interface {
	AdmitInstance(ctx context.Context, appID, deploymentID, scope, trigger string) (instanceID, nodeID, deploymentIDOut, wakeID string, method int32, atCapacity bool, port int, err error)
}

// StoreTargetResolver validates current ownership and the declared UDP port
// before selecting a running instance or asking schedd to wake the app.
type StoreTargetResolver struct {
	Store    TargetSource
	Admitter Admitter
	cursor   atomic.Uint64
}

func (r *StoreTargetResolver) ResolveTarget(ctx context.Context, route Route) (gateway.Target, error) {
	if r == nil || r.Store == nil || ctx == nil {
		return gateway.Target{}, errors.New("UDP resolver requires context and a state source")
	}
	if route.AppID == "" || route.AccountID == "" || route.ListenerName == "" || route.ListenerID == "" || route.PublicPort < api.UDPListenerPublicPortMin || route.PublicPort > api.UDPListenerPublicPortMax || route.GuestPort < 1 || route.GuestPort > 65535 {
		return gateway.Target{}, errors.New("invalid UDP app route")
	}
	if err := r.validateIntent(ctx, route); err != nil {
		return gateway.Target{}, err
	}
	deploymentID, err := ingressroute.Deployment(ctx, r.Store, route.AppID, rand.Uint64())
	if err != nil {
		return gateway.Target{}, fmt.Errorf("select raw ingress deployment: %w", err)
	}
	instances, err := state.ListLiveInstancesForApp(ctx, r.Store, route.AppID)
	if err != nil {
		return gateway.Target{}, err
	}
	if err := ctx.Err(); err != nil {
		return gateway.Target{}, err
	}
	running := make([]state.Instance, 0, len(instances))
	for _, instance := range instances {
		if instance.AppID == route.AppID && instance.DeploymentID == deploymentID && instance.State == string(state.StateRunning) && instance.Mode != string(state.InstanceModeMirror) && !state.IsFork(instance.Mode) && instance.ID != "" && instance.NodeID != "" {
			running = append(running, instance)
		}
	}
	if len(running) > 0 {
		running, err = ingressroute.ReadyInstances(ctx, r.Store, route.AppID, deploymentID, running)
		if err != nil {
			return gateway.Target{}, err
		}
		if len(running) == 0 {
			return gateway.Target{}, ingressroute.ErrNoReadyInstance
		}
		if err := r.validateIntent(ctx, route); err != nil {
			return gateway.Target{}, err
		}
		if err := ingressroute.RequireDeployment(ctx, r.Store, route.AppID, deploymentID); err != nil {
			return gateway.Target{}, err
		}
		instance := running[(r.cursor.Add(1)-1)%uint64(len(running))]
		return gateway.Target{AppID: route.AppID, InstanceID: instance.ID, NodeID: instance.NodeID, DeploymentID: instance.DeploymentID, WakeID: instance.WakeID, Port: route.GuestPort}, nil
	}
	if r.Admitter == nil {
		return gateway.Target{}, errors.New("UDP app has no running instance and admission is unavailable")
	}
	instance, node, deployment, wake, _, capacity, _, err := r.Admitter.AdmitInstance(ctx, route.AppID, deploymentID, "", "gateway")
	if err != nil {
		return gateway.Target{}, fmt.Errorf("admit UDP app: %w", err)
	}
	if capacity || instance == "" || node == "" || deployment != deploymentID {
		return gateway.Target{}, errors.New("UDP admission produced no routable instance")
	}
	ready, err := ingressroute.ReadyInstances(ctx, r.Store, route.AppID, deploymentID, []state.Instance{{ID: instance, AppID: route.AppID, DeploymentID: deploymentID, NodeID: node, WakeID: wake}})
	if err != nil {
		return gateway.Target{}, err
	}
	if len(ready) == 0 {
		return gateway.Target{}, ingressroute.ErrNoReadyInstance
	}
	// Waking may outlast an intent change; never admit a peer from stale intent.
	if err := r.validateIntent(ctx, route); err != nil {
		return gateway.Target{}, err
	}
	if err := ingressroute.RequireDeployment(ctx, r.Store, route.AppID, deploymentID); err != nil {
		return gateway.Target{}, err
	}
	return gateway.Target{AppID: route.AppID, InstanceID: instance, NodeID: node, DeploymentID: deployment, WakeID: wake, Port: route.GuestPort}, nil
}

func (r *StoreTargetResolver) validateIntent(ctx context.Context, route Route) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	app, err := r.Store.AppByID(ctx, route.AppID)
	if err != nil {
		return err
	}
	if app.Status == state.AppDeleted || app.AccountID != route.AccountID {
		return errors.New("UDP app ownership no longer matches listener")
	}
	if err := ingressroute.RequireActiveOwner(ctx, r.Store, app); err != nil {
		return err
	}
	if app.MaintenanceMode {
		return errors.New("UDP app is in maintenance mode")
	}
	if app.Visibility == api.AppVisibilityInternal {
		return errors.New("UDP app is internal")
	}
	intent, err := r.Store.UDPListenerByAppAndName(ctx, route.AppID, route.ListenerName)
	if err != nil {
		return err
	}
	if intent.ID != route.ListenerID || intent.PublicPort != route.PublicPort || intent.AppID != route.AppID || intent.ListenerName != route.ListenerName || !intent.Enabled || intent.Protocol != "udp" || intent.AccountID != route.AccountID || intent.GuestPort != route.GuestPort {
		return errors.New("UDP listener is disabled or changed")
	}
	if !api.DeclaresPublicWorkloadPort(app.Manifest.Ports, route.ListenerName, api.WorkloadPortUDP, route.GuestPort) {
		return errors.New("UDP listener is not declared by current app manifest")
	}
	return ctx.Err()
}
