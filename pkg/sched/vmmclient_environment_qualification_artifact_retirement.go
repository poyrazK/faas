package sched

import (
	"context"
	"errors"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/qualificationwire"
	"github.com/onebox-faas/faas/pkg/state"
)

// Artifact retirement is a distinct host capability. The scheduler supplies
// only state-store receipts for a completed restore and successful smoke.
type EnvironmentQualificationArtifactRetirementVMM interface {
	RetireEnvironmentQualificationArtifacts(context.Context, state.EnvironmentQualificationExecution,
		state.EnvironmentQualificationExecution, state.EnvironmentQualificationSmokeReceipt, string) error
}

var _ EnvironmentQualificationArtifactRetirementVMM = (*VMMClient)(nil)
var _ EnvironmentQualificationArtifactRetirementVMM = (*VMMRouter)(nil)

func (c *VMMClient) RetireEnvironmentQualificationArtifacts(ctx context.Context,
	capture, restored state.EnvironmentQualificationExecution, smoke state.EnvironmentQualificationSmokeReceipt, captureID string) error {
	if c == nil || c.cli == nil || captureID == "" {
		return state.ErrConflict
	}
	encodedCapture, err := qualificationwire.RetirementExecutionToProto(capture)
	if err != nil {
		return err
	}
	encodedRestore, err := qualificationwire.RetirementExecutionToProto(restored)
	if err != nil {
		return err
	}
	ctx, err = qualificationOutgoing(ctx, capture)
	if err != nil {
		return err
	}
	resp, err := c.cli.RetireEnvironmentQualificationArtifacts(ctx, &vmmdpb.RetireEnvironmentQualificationArtifactsRequest{
		CaptureExecution: encodedCapture, RestoreExecution: encodedRestore,
		SmokeReceipt: &vmmdpb.EnvironmentQualificationSmokeReceipt{RequestId: smoke.RequestID, Attempt: smoke.Attempt,
			GraphId: smoke.GraphID, CaptureInstanceId: smoke.CaptureInstanceID, InstanceId: smoke.InstanceID, Resource: smoke.Resource,
			PolicyId: smoke.PolicyID, PolicySha256: smoke.PolicySHA256, ResultSha256: smoke.ResultSHA256,
			RecordedAtUnixMillis: smoke.RecordedAt.UnixMilli()}, CaptureId: captureID})
	if err != nil {
		return qualificationLiftError(err)
	}
	if resp == nil || len(resp.ProtoReflect().GetUnknown()) != 0 || !resp.GetRetired() || resp.GetCaptureId() != captureID {
		return errors.Join(state.ErrConflict, errors.New("qualification artifact retirement acknowledgement differs from request"))
	}
	return nil
}

func (r *VMMRouter) RetireEnvironmentQualificationArtifacts(ctx context.Context,
	capture, restored state.EnvironmentQualificationExecution, smoke state.EnvironmentQualificationSmokeReceipt, captureID string) error {
	cli, err := r.resolveFor(ctx, capture.NodeID)
	if err != nil {
		return err
	}
	qualified, ok := cli.(EnvironmentQualificationArtifactRetirementVMM)
	if !ok {
		return state.ErrConflict
	}
	return qualified.RetireEnvironmentQualificationArtifacts(ctx, capture, restored, smoke, captureID)
}
