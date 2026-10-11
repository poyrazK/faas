package sched

import (
	"context"
	"errors"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/qualificationwire"
	"github.com/onebox-faas/faas/pkg/state"
)

// Restore is independently negotiated from capture and retirement. A vmmd
// that only supports cold-boot qualification cannot silently satisfy it.
type EnvironmentQualificationRestoreVMM interface {
	RestoreEnvironmentQualification(context.Context, state.EnvironmentQualificationExecution, AppSpec) (*WakeOutcome, error)
}

var _ EnvironmentQualificationRestoreVMM = (*VMMClient)(nil)
var _ EnvironmentQualificationRestoreVMM = (*VMMRouter)(nil)

func (c *VMMClient) RestoreEnvironmentQualification(ctx context.Context, frame state.EnvironmentQualificationExecution, app AppSpec) (*WakeOutcome, error) {
	if c == nil || c.cli == nil {
		return nil, state.ErrConflict
	}
	encoded, err := qualificationwire.RestoreExecutionToProto(frame)
	if err != nil {
		return nil, err
	}
	ctx, err = qualificationOutgoing(ctx, frame)
	if err != nil {
		return nil, err
	}
	if err := c.requireSecretAliasSupport(ctx, app); err != nil {
		return nil, err
	}
	resp, err := c.cli.RestoreEnvironmentQualification(ctx, &vmmdpb.RestoreEnvironmentQualificationRequest{
		Execution: encoded, App: app.toProto(), Plan: string(app.Plan), AccountId: app.AccountID,
	})
	if err != nil {
		return nil, qualificationLiftError(err)
	}
	if resp == nil || len(resp.ProtoReflect().GetUnknown()) != 0 || resp.GetWake() == nil || len(resp.GetWake().ProtoReflect().GetUnknown()) != 0 {
		return nil, state.ErrConflict
	}
	returned, err := qualificationwire.RestoreExecutionFromProto(resp.GetExecution())
	wake := resp.GetWake()
	if err != nil || returned != frame || wake.GetInstance() != frame.InstanceID ||
		wake.GetMethod() != vmmdpb.WakeMethod_WAKE_RESTORE || wake.GetRequestedMethod() != vmmdpb.WakeMethod_WAKE_RESTORE ||
		appNeedsSecretAliasSupport(app) && !wake.GetSupportsSecretAliases() {
		return nil, errors.Join(state.ErrConflict, err)
	}
	return outcomeFromProto(wake), nil
}

func (r *VMMRouter) RestoreEnvironmentQualification(ctx context.Context, frame state.EnvironmentQualificationExecution, app AppSpec) (*WakeOutcome, error) {
	cli, err := r.resolveFor(ctx, frame.NodeID)
	if err != nil {
		return nil, err
	}
	vmm, ok := cli.(EnvironmentQualificationRestoreVMM)
	if !ok {
		return nil, state.ErrConflict
	}
	return vmm.RestoreEnvironmentQualification(ctx, frame, app)
}
