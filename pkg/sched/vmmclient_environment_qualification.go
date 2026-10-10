package sched

import (
	"context"
	"errors"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/qualificationwire"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/metadata"
)

var _ EnvironmentQualificationVMM = (*VMMClient)(nil)
var _ EnvironmentQualificationVMM = (*VMMRouter)(nil)

func qualificationOutgoing(ctx context.Context, frame state.EnvironmentQualificationExecution) (context.Context, error) {
	fields, _ := wire.FromContext(ctx)
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	for _, field := range []struct {
		key  string
		seen *string
		want string
	}{{"x-faas-node-id", &fields.NodeID, frame.NodeID}, {"x-faas-instance-id", &fields.InstanceID, frame.InstanceID},
		{"x-faas-app-id", &fields.AppID, frame.AppID}, {"x-faas-deployment-id", &fields.DeploymentID, frame.DeploymentID}, {"x-faas-wake-id", &fields.WakeID, frame.WakeID}} {
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

func (c *VMMClient) CreateEnvironmentQualification(ctx context.Context, frame state.EnvironmentQualificationExecution, app AppSpec) (*WakeOutcome, error) {
	if c == nil || c.cli == nil {
		return nil, state.ErrConflict
	}
	encoded, err := qualificationwire.ExecutionToProto(frame)
	if err != nil {
		return nil, err
	}
	ctx, err = qualificationOutgoing(ctx, frame)
	if err != nil {
		return nil, err
	}
	if err := c.requireImageHealthcheckSupport(ctx, app); err != nil {
		return nil, err
	}
	if err := c.requireSecretAliasSupport(ctx, app); err != nil {
		return nil, err
	}
	resp, err := c.cli.CreateEnvironmentQualification(ctx, &vmmdpb.CreateEnvironmentQualificationRequest{
		Execution: encoded, App: app.toProto(), Plan: string(app.Plan), AccountId: app.AccountID})
	if err != nil {
		return nil, qualificationLiftError(err)
	}
	returned, err := qualificationwire.ExecutionFromProto(resp.GetExecution())
	wake := resp.GetWake()
	if err != nil || returned != frame || wake == nil || wake.GetInstance() != frame.InstanceID || wake.GetMethod() != vmmdpb.WakeMethod_WAKE_COLD_BOOT ||
		wake.GetRequestedMethod() != vmmdpb.WakeMethod_WAKE_COLD_BOOT || appNeedsSecretAliasSupport(app) && !wake.GetSupportsSecretAliases() || app.ImageHealthcheckRequired && (!wake.GetSupportsImageHealthcheck() || !wake.GetImageHealthcheckVerified() || !wake.GetSupportsImageHealthcheckMonitoring()) {
		// Never use the generic secret-alias cleanup helper here. The runtime
		// owner will retire the original frame through the dedicated RPC.
		return nil, errors.Join(state.ErrConflict, err)
	}
	return outcomeFromProto(wake), nil
}

func (c *VMMClient) RetireEnvironmentQualification(ctx context.Context, frame state.EnvironmentQualificationExecution) (EnvironmentQualificationRetirementEvidence, error) {
	var zero EnvironmentQualificationRetirementEvidence
	if c == nil || c.cli == nil {
		return zero, state.ErrConflict
	}
	encoded, err := qualificationwire.RetirementExecutionToProto(frame)
	if err != nil {
		return zero, err
	}
	ctx, err = qualificationOutgoing(ctx, frame)
	if err != nil {
		return zero, err
	}
	resp, err := c.cli.RetireEnvironmentQualification(ctx, &vmmdpb.RetireEnvironmentQualificationRequest{Execution: encoded})
	if err != nil {
		return zero, qualificationLiftError(err)
	}
	returned, err := qualificationwire.RetirementExecutionFromProto(resp.GetExecution())
	if err != nil || returned != frame {
		return zero, errors.Join(state.ErrConflict, err)
	}
	proof, err := qualificationwire.NativeRetirementFromProto(resp.GetRetirement())
	if err != nil {
		return zero, err
	}
	return EnvironmentQualificationRetirementEvidence{Execution: returned, Retirement: proof}, nil
}

func qualificationLiftError(err error) error {
	lifted := liftErr(err)
	if problem := api.AsProblem(lifted); problem != nil {
		switch problem.Code {
		case api.CodeEnvironmentQualificationUnconfirmed:
			return errors.Join(lifted, state.ErrConflict)
		case api.CodeValidation:
			return errors.Join(lifted, state.ErrInvalidArgument)
		}
	}
	return lifted
}

// Placement and cleanup both resolve the recorded node, never today's app owner.
func (r *VMMRouter) CreateEnvironmentQualification(ctx context.Context, frame state.EnvironmentQualificationExecution, app AppSpec) (*WakeOutcome, error) {
	cli, err := r.resolveFor(ctx, frame.NodeID)
	if err != nil {
		return nil, err
	}
	qualified, ok := cli.(EnvironmentQualificationVMM)
	if !ok {
		return nil, state.ErrConflict
	}
	return qualified.CreateEnvironmentQualification(ctx, frame, app)
}

func (r *VMMRouter) RetireEnvironmentQualification(ctx context.Context, frame state.EnvironmentQualificationExecution) (EnvironmentQualificationRetirementEvidence, error) {
	cli, err := r.resolveFor(ctx, frame.NodeID)
	if err != nil {
		return EnvironmentQualificationRetirementEvidence{}, err
	}
	qualified, ok := cli.(EnvironmentQualificationVMM)
	if !ok {
		return EnvironmentQualificationRetirementEvidence{}, state.ErrConflict
	}
	return qualified.RetireEnvironmentQualification(ctx, frame)
}
