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
	LiveInstancesByHostIP(context.Context, string, string) ([]state.Instance, error)
	AppTaskByID(context.Context, string, string, string) (state.AppTask, error)
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
		return serviceProxyLiveIdentity(ctx, store, client, nodeName, hostIP)
	}
}

func serviceProxyLiveIdentity(ctx context.Context, store serviceProxyLiveIdentityStore, client resolveServiceCallerClient, nodeName, hostIP string) ([]state.Instance, error) {
	live, err := client.ResolveServiceCaller(ctx, &vmmdpb.ResolveServiceCallerRequest{SourceIp: hostIP})
	if status.Code(err) == codes.Unimplemented {
		// Preserve ordinary routing during rolling upgrades. App tasks have no
		// serving row, so an old node cannot authenticate a managed probe.
		return store.LiveInstancesByHostIP(ctx, nodeName, hostIP)
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
	instances, err := store.LiveInstancesByHostIP(ctx, nodeName, hostIP)
	if err != nil || live.GetJobTask() {
		return instances, err // The existing claimed managed-Job graph check applies.
	}
	for _, instance := range instances {
		if instance.AppID != live.GetAppId() || instance.DeploymentID != live.GetDeploymentId() {
			return nil, nil
		}
	}
	return instances, nil
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
		task.LeaseToken == nil || *task.LeaseToken == "" || task.LeaseExpiresAt == nil || !task.LeaseExpiresAt.After(time.Now()) ||
		pin == nil || pin.Type != api.BindingTypeService || pin.ManagedService == nil || !pin.ManagedService.Valid() ||
		pin.ManagedService.CallerDeploymentID != task.DeploymentID || pin.ManagedService.Scope != task.DeploymentScope ||
		pin.Binding != pin.ManagedService.Service || pin.Revision != pin.ManagedService.ContextRevision {
		return nil, nil
	}
	return []state.Instance{{ID: task.ID, AppID: task.AppID, DeploymentID: task.DeploymentID,
		NodeID: nodeName, HostIP: hostIP, State: string(state.StateRunning)}}, nil
}
