// adr: 948
package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity/validatorbundle"
	"github.com/onebox-faas/faas/pkg/state"
)

type validatorPromotionKey struct{}
type validatorPromotionTransfer func(context.Context, state.Deployment, string) error

func (s *server) validatorPromotionContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, validatorPromotionKey{}, validatorPromotionTransfer(func(ctx context.Context, source state.Deployment, target string) error {
		if !s.durableEntityValidatorReleaseGateEnabled || !s.durableEntityApps[source.AppID] {
			return nil
		}
		if s.durableEntityValidatorArtifacts == nil {
			return validatorbundle.ErrArtifactUnavailable
		}
		return s.durableEntityValidatorArtifacts.Transfer(ctx, source.AppID, source.ID, target)
	}))
}
func transferPromotionValidator(ctx context.Context, source state.Deployment, target string) error {
	if transfer, ok := ctx.Value(validatorPromotionKey{}).(validatorPromotionTransfer); ok {
		return transfer(ctx, source, target)
	}
	return nil
}
func (s *server) checkProjectValidatorMembers(ctx context.Context, members []state.ProjectReleaseMember) error {
	if !s.durableEntityValidatorReleaseGateEnabled {
		return nil
	}
	for _, member := range members {
		if !s.durableEntityApps[member.AppID] {
			continue
		}
		app, err := s.store.AppByID(ctx, member.AppID)
		if err != nil {
			return err
		}
		deployment, err := s.store.DeploymentByID(ctx, member.DeploymentID)
		if err != nil {
			return err
		}
		if problem := s.durableEntityValidatorReleaseProblem(ctx, app, deployment); problem != nil {
			return problem
		}
	}
	return nil
}

func (s *server) deploymentValidatorInfo(ctx context.Context, d state.Deployment, app state.App) *api.DurableEntityValidatorDeploymentInfo {
	if !s.durableEntityApps[app.ID] {
		return nil
	}
	info := &api.DurableEntityValidatorDeploymentInfo{Status: "disabled"}
	if !s.durableEntityRestoreIsolationEnabled {
		return info
	}
	info.Status = "unavailable"
	info.Source = "registry"
	if s.durableEntityValidatorArtifacts != nil {
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
