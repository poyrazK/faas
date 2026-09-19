package scheddgrpc

import (
	"github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

func platformIdentityToProto(identity api.PlatformIdentity) *scheddpb.PlatformIdentity {
	if identity == (api.PlatformIdentity{}) {
		return nil
	}
	return &scheddpb.PlatformIdentity{
		AppId:               identity.AppID,
		DeploymentId:        identity.DeploymentID,
		TenantId:            identity.TenantID,
		InstanceId:          identity.InstanceID,
		NodeId:              identity.NodeID,
		Region:              identity.Region,
		CommitSha:           identity.CommitSHA,
		DeploymentTag:       identity.DeploymentTag,
		DeploymentCreatedAt: identity.DeploymentCreatedAt,
		ImageDigest:         identity.ImageDigest,
	}
}

func platformIdentityFromProto(identity *scheddpb.PlatformIdentity) api.PlatformIdentity {
	if identity == nil {
		return api.PlatformIdentity{}
	}
	return api.PlatformIdentity{
		AppID:               identity.GetAppId(),
		DeploymentID:        identity.GetDeploymentId(),
		TenantID:            identity.GetTenantId(),
		InstanceID:          identity.GetInstanceId(),
		NodeID:              identity.GetNodeId(),
		Region:              identity.GetRegion(),
		CommitSHA:           identity.GetCommitSha(),
		DeploymentTag:       identity.GetDeploymentTag(),
		DeploymentCreatedAt: identity.GetDeploymentCreatedAt(),
		ImageDigest:         identity.GetImageDigest(),
	}
}

func wakeResultIdentity(res sched.WakeResult) api.PlatformIdentity {
	identity := res.Identity
	if identity.DeploymentID == "" {
		identity.DeploymentID = res.DeploymentID
	}
	if identity.InstanceID == "" {
		identity.InstanceID = res.InstanceID
	}
	if identity.NodeID == "" {
		identity.NodeID = res.NodeID
	}
	return identity
}
