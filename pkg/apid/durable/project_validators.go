// adr: 948
package durable

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity/validatorbundle"
	"github.com/onebox-faas/faas/pkg/state"
)

type validatorPromotionKey struct{}
type validatorPromotionTransfer func(context.Context, state.Deployment, string) error

func (s *Service) PromotionContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, validatorPromotionKey{}, validatorPromotionTransfer(func(ctx context.Context, source state.Deployment, target string) error {
		if !s.ValidatorReleaseGateEnabled || !s.Apps[source.AppID] {
			return nil
		}
		if s.ValidatorArtifacts == nil {
			return validatorbundle.ErrArtifactUnavailable
		}
		return s.ValidatorArtifacts.Transfer(ctx, source.AppID, source.ID, target)
	}))
}
func TransferPromotionValidator(ctx context.Context, source state.Deployment, target string) error {
	if transfer, ok := ctx.Value(validatorPromotionKey{}).(validatorPromotionTransfer); ok {
		return transfer(ctx, source, target)
	}
	return nil
}
func (s *Service) CheckProjectValidatorMembers(ctx context.Context, members []state.ProjectReleaseMember) error {
	if !s.ValidatorReleaseGateEnabled {
		return nil
	}
	for _, member := range members {
		if !s.Apps[member.AppID] {
			continue
		}
		app, err := s.Host.Store().AppByID(ctx, member.AppID)
		if err != nil {
			return err
		}
		deployment, err := s.Host.Store().DeploymentByID(ctx, member.DeploymentID)
		if err != nil {
			return err
		}
		if problem := s.ValidatorReleaseProblem(ctx, app, deployment); problem != nil {
			return problem
		}
	}
	return nil
}

func (s *Service) DeploymentValidatorInfo(ctx context.Context, d state.Deployment, app state.App) *api.DurableEntityValidatorDeploymentInfo {
	if !s.Apps[app.ID] {
		return nil
	}
	info := &api.DurableEntityValidatorDeploymentInfo{Status: "disabled"}
	if !s.RestoreIsolationEnabled {
		return info
	}
	info.Status = "unavailable"
	info.Source = "registry"
	if s.ValidatorArtifacts != nil {
		info.Source = "object_storage"
	}
	ctx, cancel := context.WithTimeout(ctx, api.DurableEntityReleaseTimeout)
	defer cancel()
	b, err := s.resolveDurableEntityValidatorBundle(ctx, app.ID, d.ID)
	if err == nil && d.AppID == app.ID {
		info.Status = "ready"
		info.SHA256 = b.SHA256
	}
	return info
}
