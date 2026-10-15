package sched

import (
	"context"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type admittedRuntimeClient interface {
	RuntimeAdmissionIdentity(context.Context) (runtimeadmission.Identity, error)
	CreateAdmittedRuntime(context.Context, *vmmdpb.CreateAdmittedRuntimeRequest) (*WakeOutcome, error)
}

func (r *VMMRouter) RuntimeAdmissionIdentity(ctx context.Context, nodeID string) (runtimeadmission.Identity, error) {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return runtimeadmission.Identity{}, err
	}
	admitted, ok := cli.(admittedRuntimeClient)
	if !ok {
		return runtimeadmission.Identity{}, runtimeadmission.ErrUnavailable
	}
	identity, err := admitted.RuntimeAdmissionIdentity(ctx)
	if err != nil {
		return runtimeadmission.Identity{}, err
	}
	if err := identity.Validate(); err != nil {
		return runtimeadmission.Identity{}, err
	}
	if identity.NodeID != nodeID {
		return runtimeadmission.Identity{}, runtimeadmission.ErrStale
	}
	return identity, nil
}

func (r *VMMRouter) CreateAdmittedRuntime(ctx context.Context, nodeID string, req *vmmdpb.CreateAdmittedRuntimeRequest) (*WakeOutcome, error) {
	if req.GetBinding().GetNodeId() != nodeID {
		return nil, runtimeadmission.ErrStale
	}
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	admitted, ok := cli.(admittedRuntimeClient)
	if !ok {
		return nil, runtimeadmission.ErrUnavailable
	}
	return admitted.CreateAdmittedRuntime(ctx, req)
}
