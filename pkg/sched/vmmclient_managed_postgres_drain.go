package sched

import (
	"context"
	"errors"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/postgresdrainwire"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type ManagedPostgresDrainEvidence struct {
	Frame state.ManagedPostgresDrainFrame
	Proof state.ManagedPostgresDrainProof
}

type ManagedPostgresDrainVMM interface {
	RetireManagedPostgresDrain(context.Context, state.ManagedPostgresDrainFrame) (ManagedPostgresDrainEvidence, error)
}

var _ ManagedPostgresDrainVMM = (*VMMClient)(nil)
var _ ManagedPostgresDrainVMM = (*VMMRouter)(nil)

func managedPostgresDrainOutgoing(ctx context.Context, f state.ManagedPostgresDrainFrame) (context.Context, error) {
	fields, _ := wire.FromContext(ctx)
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	for _, field := range []struct {
		key  string
		seen *string
		want string
	}{
		{"x-faas-node-id", &fields.NodeID, f.NodeID}, {"x-faas-instance-id", &fields.InstanceID, f.InstanceID},
		{"x-faas-app-id", &fields.AppID, f.AppID}, {"x-faas-deployment-id", &fields.DeploymentID, f.DeploymentID}, {"x-faas-wake-id", &fields.WakeID, f.WakeID}} {
		if *field.seen != "" && *field.seen != field.want {
			return nil, state.ErrInvalidArgument
		}
		for _, value := range md.Get(field.key) {
			if value != field.want {
				return nil, state.ErrInvalidArgument
			}
		}
		md.Delete(field.key)
		*field.seen = field.want
	}
	ctx = wire.WithContext(metadata.NewOutgoingContext(ctx, md), fields)
	return wire.WithCorrelationOutgoing(ctx, fields), nil
}

func (c *VMMClient) RetireManagedPostgresDrain(ctx context.Context, frame state.ManagedPostgresDrainFrame) (ManagedPostgresDrainEvidence, error) {
	if c == nil || c.cli == nil {
		return ManagedPostgresDrainEvidence{}, state.ErrConflict
	}
	p, err := postgresdrainwire.FrameToProto(frame)
	if err != nil {
		return ManagedPostgresDrainEvidence{}, err
	}
	ctx, err = managedPostgresDrainOutgoing(ctx, frame)
	if err != nil {
		return ManagedPostgresDrainEvidence{}, err
	}
	response, err := c.cli.RetireManagedPostgresDrain(ctx, &vmmdpb.RetireManagedPostgresDrainRequest{Frame: p})
	if err != nil {
		switch status.Code(err) {
		case codes.FailedPrecondition, codes.Unimplemented:
			return ManagedPostgresDrainEvidence{}, errors.Join(state.ErrConflict, err)
		case codes.InvalidArgument:
			return ManagedPostgresDrainEvidence{}, errors.Join(state.ErrInvalidArgument, err)
		}
		return ManagedPostgresDrainEvidence{}, err
	}
	if response == nil || len(response.ProtoReflect().GetUnknown()) != 0 {
		return ManagedPostgresDrainEvidence{}, state.ErrConflict
	}
	returned, err := postgresdrainwire.FrameFromProto(response.GetFrame())
	if err != nil || !returned.Equal(frame) {
		return ManagedPostgresDrainEvidence{}, errors.Join(state.ErrConflict, err)
	}
	proof, err := postgresdrainwire.ProofFromProto(response.GetProof(), frame)
	if err != nil {
		return ManagedPostgresDrainEvidence{}, err
	}
	return ManagedPostgresDrainEvidence{Frame: returned, Proof: proof}, nil
}

func (r *VMMRouter) RetireManagedPostgresDrain(ctx context.Context, frame state.ManagedPostgresDrainFrame) (ManagedPostgresDrainEvidence, error) {
	if !frame.Valid() {
		return ManagedPostgresDrainEvidence{}, state.ErrInvalidArgument
	}
	client, err := r.resolveFor(ctx, frame.NodeID)
	if err != nil {
		return ManagedPostgresDrainEvidence{}, err
	}
	vmm, ok := client.(ManagedPostgresDrainVMM)
	if !ok {
		return ManagedPostgresDrainEvidence{}, state.ErrConflict
	}
	return vmm.RetireManagedPostgresDrain(ctx, frame)
}
