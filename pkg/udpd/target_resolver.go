package udpd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// TargetSource provides authoritative app, intent and live-instance projections.
// The public edge cannot mutate lifecycle state through this interface.
type TargetSource interface {
	AppByID(context.Context, string) (state.App, error)
	UDPListenerByAppAndName(context.Context, string, string) (state.UDPListener, error)
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
	if r == nil || r.Store == nil {
		return gateway.Target{}, errors.New("UDP resolver requires a state source")
	}
	if route.AppID == "" || route.AccountID == "" || route.ListenerName == "" || route.ListenerID == "" || route.PublicPort < api.UDPListenerPublicPortMin || route.PublicPort > api.UDPListenerPublicPortMax || route.GuestPort < 1 || route.GuestPort > 65535 {
		return gateway.Target{}, errors.New("invalid UDP app route")
	}
	app, err := r.Store.AppByID(ctx, route.AppID)
	if err != nil {
		return gateway.Target{}, err
	}
	if app.Status == state.AppDeleted || app.AccountID != route.AccountID {
		return gateway.Target{}, errors.New("UDP app ownership no longer matches listener")
	}
	if app.MaintenanceMode {
		return gateway.Target{}, errors.New("UDP app is in maintenance mode")
	}
	intent, err := r.Store.UDPListenerByAppAndName(ctx, route.AppID, route.ListenerName)
	if err != nil {
		return gateway.Target{}, err
	}
	if intent.ID != route.ListenerID || intent.PublicPort != route.PublicPort || intent.AppID != route.AppID || intent.ListenerName != route.ListenerName || !intent.Enabled || intent.Protocol != "udp" || intent.AccountID != route.AccountID || intent.GuestPort != route.GuestPort {
		return gateway.Target{}, errors.New("UDP listener is disabled or changed")
	}
	declared := false
	for _, port := range app.Manifest.Ports {
		name := strings.ToLower(strings.TrimSpace(port.Name))
		if name == "" {
			name = fmt.Sprintf("udp-%d", port.Port)
		}
		if port.EffectiveProtocol() == api.WorkloadPortUDP && port.Port == route.GuestPort && name == route.ListenerName {
			declared = true
			break
		}
	}
	if !declared {
		return gateway.Target{}, errors.New("UDP listener is not declared by current app manifest")
	}
	instances, err := r.Store.ListInstancesForApp(ctx, route.AppID)
	if err != nil {
		return gateway.Target{}, err
	}
	running := make([]state.Instance, 0, len(instances))
	for _, instance := range instances {
		if instance.AppID == route.AppID && instance.State == string(state.StateRunning) && instance.ID != "" && instance.NodeID != "" {
			running = append(running, instance)
		}
	}
	if len(running) > 0 {
		instance := running[(r.cursor.Add(1)-1)%uint64(len(running))]
		return gateway.Target{AppID: route.AppID, InstanceID: instance.ID, NodeID: instance.NodeID, DeploymentID: instance.DeploymentID, WakeID: instance.WakeID, Port: route.GuestPort}, nil
	}
	if r.Admitter == nil {
		return gateway.Target{}, errors.New("UDP app has no running instance and admission is unavailable")
	}
	instance, node, deployment, wake, _, capacity, _, err := r.Admitter.AdmitInstance(ctx, route.AppID, "", "", "gateway")
	if err != nil {
		return gateway.Target{}, fmt.Errorf("admit UDP app: %w", err)
	}
	if capacity || instance == "" || node == "" {
		return gateway.Target{}, errors.New("UDP admission produced no routable instance")
	}
	return gateway.Target{AppID: route.AppID, InstanceID: instance, NodeID: node, DeploymentID: deployment, WakeID: wake, Port: route.GuestPort}, nil
}
