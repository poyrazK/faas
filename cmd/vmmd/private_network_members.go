package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

func privateNetworkMemberSource(store state.Store, nodeID string) func(context.Context, fcvm.PrivateNetworkMemberRequest) (fcvm.PrivateNetworkMemberIdentity, error) {
	return func(ctx context.Context, req fcvm.PrivateNetworkMemberRequest) (fcvm.PrivateNetworkMemberIdentity, error) {
		members, ok := store.(state.PrivateNetworkMemberStore)
		if !ok {
			return fcvm.PrivateNetworkMemberIdentity{}, fmt.Errorf("private network member store unavailable")
		}
		attachments, ok := store.(state.AppPrivateNetworkAttachmentStore)
		if !ok {
			return fcvm.PrivateNetworkMemberIdentity{}, fmt.Errorf("private attachment store unavailable")
		}
		attachment, err := attachments.GetAppPrivateNetworkAttachment(ctx, req.AccountID, req.AppID)
		if err != nil {
			return fcvm.PrivateNetworkMemberIdentity{}, err
		}
		if attachment.NetworkID != req.NetworkID || req.RequireReady && attachment.Status != "ready" {
			return fcvm.PrivateNetworkMemberIdentity{}, state.ErrConflict
		}
		instance, err := store.InstanceByID(ctx, req.InstanceID)
		if err != nil {
			return fcvm.PrivateNetworkMemberIdentity{}, err
		}
		if instance.AppID != req.AppID || instance.DeploymentID != req.DeploymentID || instance.NodeID != nodeID {
			return fcvm.PrivateNetworkMemberIdentity{}, state.ErrConflict
		}
		switch state.State(instance.State) {
		case state.StateWaking, state.StateColdBooting, state.StateRunning, state.StateWarm, state.StateFrozen:
		default:
			return fcvm.PrivateNetworkMemberIdentity{}, state.ErrConflict
		}
		deployment, err := store.DeploymentByID(ctx, req.DeploymentID)
		if err != nil {
			return fcvm.PrivateNetworkMemberIdentity{}, err
		}
		eligible := req.Eligible && deployment.Status == state.DeployLive && deployment.TrafficPercent > 0
		networks, ok := store.(state.PrivateNetworkStore)
		if !ok {
			return fcvm.PrivateNetworkMemberIdentity{}, fmt.Errorf("private network store unavailable")
		}
		service, err := networks.AllocatePrivateNetworkAddress(ctx, req.AccountID, req.NetworkID, "app", req.AppID)
		if err != nil {
			return fcvm.PrivateNetworkMemberIdentity{}, err
		}
		if req.ExpectedService.IsValid() && req.ExpectedService != service.Address {
			return fcvm.PrivateNetworkMemberIdentity{}, state.ErrConflict
		}
		return members.EnsurePrivateNetworkMember(ctx, req.AccountID, req.AppID, req.NetworkID, privateNetworkMemberOwner(nodeID, req.InstanceID), eligible)
	}
}

// A scheduler row can move between nodes while its previous physical owner is
// still being fenced. Sharing that row's ID must not share a runtime address.
func privateNetworkMemberOwner(nodeID, instanceID string) string {
	sum := sha256.Sum256([]byte("private-network-member\x00" + nodeID + "\x00" + instanceID))
	return "vm-" + hex.EncodeToString(sum[:])
}

func privateNetworkMemberRelease(store state.Store, nodeID string) func(context.Context, string, string, string, string, bool) error {
	return func(ctx context.Context, account, app, network, instance string, serviceOnly bool) error {
		members, ok := store.(state.PrivateNetworkMemberStore)
		if !ok {
			return fmt.Errorf("private network member store unavailable")
		}
		return members.ReleasePrivateNetworkMember(ctx, account, app, network, privateNetworkMemberOwner(nodeID, instance), serviceOnly)
	}
}
