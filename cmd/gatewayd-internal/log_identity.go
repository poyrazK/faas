package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

// resolveRuntimeLogIdentity joins a log frame with the authoritative state
// rows for its instance, deployment, and compute node. The cache belongs to a
// single stream request/worker, so it cannot grow with the lifetime of the
// daemon and a deployment transition naturally gets a fresh key.
func resolveRuntimeLogIdentity(ctx context.Context, store state.Store, frame scheddgrpc.LogFrame, accountID, appID, deploymentID string, cache map[string]api.PlatformIdentity) api.PlatformIdentity {
	deploymentID = firstNonEmpty(frame.DeploymentID, deploymentID)
	identity := api.PlatformIdentity{
		AppID:        appID,
		DeploymentID: deploymentID,
		TenantID:     accountID,
		InstanceID:   frame.InstanceID,
	}
	if frame.InstanceID == "" {
		return identity
	}
	cacheKey := frame.InstanceID + "\x00" + deploymentID
	if cached, ok := cache[cacheKey]; ok {
		return cached
	}
	if store == nil {
		cache[cacheKey] = identity
		return identity
	}

	if instance, err := store.InstanceByID(ctx, frame.InstanceID); err == nil && instanceBelongsToApp(instance.AppID, appID) {
		identity.NodeID = instance.NodeID
		if identity.DeploymentID == "" {
			identity.DeploymentID = instance.DeploymentID
		}
	}
	if identity.DeploymentID != "" {
		if deployment, err := store.DeploymentByID(ctx, identity.DeploymentID); err == nil && deploymentBelongsToApp(deployment.AppID, appID) {
			identity.CommitSHA = deployment.CommitSHA
			identity.DeploymentTag = deployment.Tag
			identity.ImageDigest = deployment.ImageDigest
			if !deployment.CreatedAt.IsZero() {
				identity.DeploymentCreatedAt = deployment.CreatedAt.UTC().Format(time.RFC3339Nano)
			}
		}
	}
	if identity.NodeID != "" {
		if node, err := store.ComputeNodeByID(ctx, identity.NodeID); err == nil && node.Region != nil {
			identity.Region = *node.Region
		}
	}
	// The instance lookup can fill a deployment id that was absent on the
	// incoming frame, so cache under the final identity as well as the original
	// key. This keeps a later frame from repeating the same joins.
	cache[cacheKey] = identity
	cache[frame.InstanceID+"\x00"+identity.DeploymentID] = identity
	return identity
}

func instanceBelongsToApp(instanceAppID, appID string) bool {
	return appID == "" || instanceAppID == "" || instanceAppID == appID
}

func deploymentBelongsToApp(deploymentAppID, appID string) bool {
	return appID == "" || deploymentAppID == "" || deploymentAppID == appID
}
