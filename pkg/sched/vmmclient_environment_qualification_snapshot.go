package sched

import (
	"context"
	"errors"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/qualificationwire"
	"github.com/onebox-faas/faas/pkg/state"
)

// Snapshot capability remains separate from creation and retirement so older
// native nodes fail closed without consuming generic snapshot authority.
type EnvironmentQualificationSnapshotVMM interface {
	CaptureEnvironmentQualification(context.Context, state.EnvironmentQualificationExecution) (EnvironmentQualificationSnapshotEvidence, error)
}

type EnvironmentQualificationSnapshotEvidence struct {
	Execution state.EnvironmentQualificationExecution
	Snapshot  state.EnvironmentQualificationSnapshot
}

var _ EnvironmentQualificationSnapshotVMM = (*VMMClient)(nil)
var _ EnvironmentQualificationSnapshotVMM = (*VMMRouter)(nil)

func (c *VMMClient) CaptureEnvironmentQualification(ctx context.Context, frame state.EnvironmentQualificationExecution) (EnvironmentQualificationSnapshotEvidence, error) {
	var zero EnvironmentQualificationSnapshotEvidence
	if c == nil || c.cli == nil {
		return zero, state.ErrConflict
	}
	encoded, err := qualificationwire.ExecutionToProto(frame)
	if err != nil {
		return zero, err
	}
	ctx, err = qualificationOutgoing(ctx, frame)
	if err != nil {
		return zero, err
	}
	resp, err := c.cli.CaptureEnvironmentQualification(ctx, &vmmdpb.CaptureEnvironmentQualificationRequest{Execution: encoded})
	if err != nil {
		return zero, qualificationLiftError(err)
	}
	if resp == nil || len(resp.ProtoReflect().GetUnknown()) != 0 {
		return zero, state.ErrConflict
	}
	returned, err := qualificationwire.ExecutionFromProto(resp.GetExecution())
	if err != nil || returned != frame {
		return zero, errors.Join(state.ErrConflict, err)
	}
	proof, err := qualificationwire.SnapshotFromProto(frame, resp.GetSnapshot())
	if err != nil {
		return zero, err
	}
	return EnvironmentQualificationSnapshotEvidence{Execution: returned, Snapshot: proof}, nil
}

func (r *VMMRouter) CaptureEnvironmentQualification(ctx context.Context, frame state.EnvironmentQualificationExecution) (EnvironmentQualificationSnapshotEvidence, error) {
	cli, err := r.resolveFor(ctx, frame.NodeID)
	if err != nil {
		return EnvironmentQualificationSnapshotEvidence{}, err
	}
	vmm, ok := cli.(EnvironmentQualificationSnapshotVMM)
	if !ok {
		return EnvironmentQualificationSnapshotEvidence{}, state.ErrConflict
	}
	return vmm.CaptureEnvironmentQualification(ctx, frame)
}
