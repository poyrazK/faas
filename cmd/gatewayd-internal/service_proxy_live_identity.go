package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type serviceProxyLiveIdentityStore interface {
	LiveServiceCallersByHostIP(context.Context, string, string) ([]state.Instance, error)
	AppTaskByID(context.Context, string, string, string) (state.AppTask, error)
	InstanceByID(context.Context, string) (state.Instance, error)
}

type resolveServiceCallerClient interface {
	ResolveServiceCaller(context.Context, *vmmdpb.ResolveServiceCallerRequest, ...grpc.CallOption) (*vmmdpb.ResolveServiceCallerResponse, error)
}

func newServiceProxyLiveLookup(store serviceProxyLiveIdentityStore, clients vmmdClientLookup, localNode *localNodeID) func(context.Context, string, string) ([]state.Instance, error) {
	return func(ctx context.Context, nodeName, hostIP string) ([]state.Instance, error) {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		nodeID, err := localNode.Get(ctx)
		if err != nil {
			return nil, err
		}
		client, closer, ok := clients.ClientFor(ctx, nodeID)
		if !ok {
			localNode.Forget()
			return nil, gateway.ErrServiceProxyUnavailable
		}
		defer func() { _ = closer.Close() }()
		return serviceProxyLiveIdentity(ctx, store, client, nodeName, nodeID, hostIP)
	}
}

// serviceProxyLiveIdentity attributes a guest source address. nodeName is the
// node's manifest name, as the serving rows carry it; nodeID is its
// compute_nodes.id, which a booting instance's row is checked against.
func serviceProxyLiveIdentity(ctx context.Context, store serviceProxyLiveIdentityStore, client resolveServiceCallerClient, nodeName, nodeID, hostIP string) ([]state.Instance, error) {
	live, err := client.ResolveServiceCaller(ctx, &vmmdpb.ResolveServiceCallerRequest{SourceIp: hostIP})
	if status.Code(err) == codes.Unimplemented {
		// Preserve ordinary routing during rolling upgrades. App tasks have no
		// serving row, so an old node cannot authenticate a managed probe.
		return store.LiveServiceCallersByHostIP(ctx, nodeName, hostIP)
	}
	if err != nil {
		return nil, fmt.Errorf("resolve live service caller: %w", err)
	}
	if !live.GetMatched() || live.GetInstanceId() == "" {
		return nil, nil
	}
	if live.GetAppTask() {
		return managedProbeCaller(ctx, store, live, nodeName, hostIP)
	}
	if !live.GetJobTask() && (live.GetAppId() == "" || live.GetDeploymentId() == "") {
		return nil, nil
	}
	instances, err := store.LiveServiceCallersByHostIP(ctx, nodeName, hostIP)
	if err != nil || live.GetJobTask() {
		return instances, err // The existing claimed managed-Job graph check applies.
	}
	if len(instances) == 0 {
		return bootingServiceCaller(ctx, store, live, nodeName, nodeID, hostIP)
	}
	for _, instance := range instances {
		if instance.AppID != live.GetAppId() || instance.DeploymentID != live.GetDeploymentId() {
			return nil, nil
		}
	}
	return instances, nil
}

// bootingServiceCaller attributes a guest whose instance is still booting. A
// cold boot runs the app and waits for its readiness before the instance turns
// running, and only then does its row carry a host address; a service call or
// alias lookup the app made at startup found no caller and was refused, so a
// readiness check that depends on a sibling service failed the deployment.
// vmmd already owns the address for the booting guest (booting_egress.go);
// its row must name the same app and deployment on this node and still be
// booting. It answers no identity on any mismatch.
func bootingServiceCaller(ctx context.Context, store serviceProxyLiveIdentityStore, live *vmmdpb.ResolveServiceCallerResponse, nodeName, nodeID, hostIP string) ([]state.Instance, error) {
	if live.GetAppTask() || live.GetAppId() == "" || live.GetDeploymentId() == "" {
		return nil, nil
	}
	instance, err := store.InstanceByID(ctx, live.GetInstanceId())
	if errors.Is(err, state.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	switch state.State(instance.State) {
	case state.StateColdBooting, state.StateWaking:
	default:
		return nil, nil
	}
	if instance.ID != live.GetInstanceId() || instance.AppID != live.GetAppId() || instance.DeploymentID != live.GetDeploymentId() ||
		nodeID == "" || instance.NodeID != nodeID {
		return nil, nil
	}
	return []state.Instance{{ID: instance.ID, AppID: instance.AppID, DeploymentID: instance.DeploymentID,
		NodeID: nodeName, HostIP: hostIP, State: string(state.StateRunning)}}, nil
}

func managedProbeCaller(ctx context.Context, store serviceProxyLiveIdentityStore, live *vmmdpb.ResolveServiceCallerResponse, nodeName, hostIP string) ([]state.Instance, error) {
	if live.GetAccountId() == "" || live.GetAppId() == "" || live.GetDeploymentId() == "" || live.GetJobTask() {
		return nil, nil
	}
	task, err := store.AppTaskByID(ctx, live.GetAccountId(), live.GetAppId(), live.GetInstanceId())
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	pin := task.BindingVerification
	if task.ID != live.GetInstanceId() || task.AccountID != live.GetAccountId() || task.AppID != live.GetAppId() ||
		task.DeploymentID != live.GetDeploymentId() || task.Status != state.AppTaskRunning || task.CancelRequested != nil ||
		task.LeaseToken == nil || *task.LeaseToken == "" || task.LeaseExpiresAt == nil || !task.LeaseExpiresAt.After(time.Now()) {
		return nil, nil
	}
	if pin == nil {
		// ADR-20261017201008: the source lookup also admits contained commands,
		// with their recorder, run, network and lease fences checked in SQL.
		// Require the exact task ID so a stale serving row cannot authenticate it.
		callers, err := store.LiveServiceCallersByHostIP(ctx, nodeName, hostIP)
		if err != nil {
			return nil, err
		}
		if len(callers) != 1 || callers[0].ID != task.ID || callers[0].AppID != task.AppID || callers[0].DeploymentID != task.DeploymentID {
			return nil, nil
		}
		return callers, nil
	}
	if pin.Type != api.BindingTypeService || pin.ManagedService == nil || !pin.ManagedService.Valid() ||
		pin.ManagedService.CallerDeploymentID != task.DeploymentID || pin.ManagedService.Scope != task.DeploymentScope ||
		pin.Binding != pin.ManagedService.Service || pin.Revision != pin.ManagedService.ContextRevision {
		return nil, nil
	}
	return []state.Instance{{ID: task.ID, AppID: task.AppID, DeploymentID: task.DeploymentID,
		NodeID: nodeName, HostIP: hostIP, State: string(state.StateRunning)}}, nil
}
