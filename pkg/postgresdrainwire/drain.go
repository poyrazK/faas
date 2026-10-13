// Package postgresdrainwire validates the narrow internal retirement protocol.
package postgresdrainwire

import (
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func FrameToProto(f state.ManagedPostgresDrainFrame) (*vmmdpb.ManagedPostgresDrainFrame, error) {
	if !f.Valid() {
		return nil, state.ErrInvalidArgument
	}
	p := &vmmdpb.ManagedPostgresDrainFrame{Id: f.ID, CutoverId: f.CutoverID, AppId: f.AppID, DeploymentId: f.DeploymentID,
		InstanceId: f.InstanceID, NodeId: f.NodeID, WakeId: f.WakeID, OwnerNodeId: f.OwnerNodeID, OwnerRevision: f.OwnerRevision, FencedAt: timestamppb.New(f.FencedAt)}
	if err := p.FencedAt.CheckValid(); err != nil {
		return nil, state.ErrInvalidArgument
	}
	return p, nil
}

func FrameFromProto(p *vmmdpb.ManagedPostgresDrainFrame) (state.ManagedPostgresDrainFrame, error) {
	if p == nil || len(p.ProtoReflect().GetUnknown()) != 0 || p.FencedAt == nil || len(p.FencedAt.ProtoReflect().GetUnknown()) != 0 || p.FencedAt.CheckValid() != nil {
		return state.ManagedPostgresDrainFrame{}, state.ErrInvalidArgument
	}
	f := state.ManagedPostgresDrainFrame{ID: p.Id, CutoverID: p.CutoverId, AppID: p.AppId, DeploymentID: p.DeploymentId, InstanceID: p.InstanceId,
		NodeID: p.NodeId, WakeID: p.WakeId, OwnerNodeID: p.OwnerNodeId, OwnerRevision: p.OwnerRevision, FencedAt: p.FencedAt.AsTime()}
	if !f.Valid() {
		return state.ManagedPostgresDrainFrame{}, state.ErrInvalidArgument
	}
	return f, nil
}

func ProofToProto(p state.ManagedPostgresDrainProof, f state.ManagedPostgresDrainFrame) (*vmmdpb.ManagedPostgresDrainProof, error) {
	if !p.ValidFor(f) {
		return nil, state.ErrConflict
	}
	return &vmmdpb.ManagedPostgresDrainProof{DrainId: p.DrainID, NativeGeneration: p.NativeGeneration, KernelBootId: p.KernelBootID,
		ProcessesExited: p.ProcessesExited, ResourcesRemoved: p.ResourcesRemoved}, nil
}

func ProofFromProto(p *vmmdpb.ManagedPostgresDrainProof, f state.ManagedPostgresDrainFrame) (state.ManagedPostgresDrainProof, error) {
	if p == nil || len(p.ProtoReflect().GetUnknown()) != 0 {
		return state.ManagedPostgresDrainProof{}, state.ErrConflict
	}
	proof := state.ManagedPostgresDrainProof{DrainID: p.DrainId, NativeGeneration: p.NativeGeneration, KernelBootID: p.KernelBootId,
		ProcessesExited: p.ProcessesExited, ResourcesRemoved: p.ResourcesRemoved}
	if !proof.ValidFor(f) {
		return state.ManagedPostgresDrainProof{}, state.ErrConflict
	}
	return proof, nil
}
