package operation

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ObserveProductDefinitionDoctor shares the existing bounded, read-only node
// observation. Product entitlement and feature-to-definition selection stay in
// Product Control; qualification remains unverified by this observation.
func (s *Service) ObserveProductDefinitionDoctor(ctx context.Context, acct state.Account, def state.OperationDefinition, tenant state.PlatformTenant) (api.OperationDoctorResponse, error) {
	if def.AccountID != acct.ID || tenant.AccountID != acct.ID {
		return api.OperationDoctorResponse{}, state.ErrNotFound
	}
	dep, err := s.Host.Store().DeploymentByID(ctx, def.DeploymentID)
	if err != nil {
		return api.OperationDoctorResponse{}, err
	}
	if dep.AppID != def.AppID || dep.Scope != def.Scope {
		return api.OperationDoctorResponse{}, state.ErrNotFound
	}
	report := s.observeOperationDoctor(ctx, acct, dep, tenant, []state.OperationDefinition{def})
	report.SubmissionState = report.ObservedSubmissionState()
	return report, ctx.Err()
}
