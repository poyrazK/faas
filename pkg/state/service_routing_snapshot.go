// adr: 531
package state

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type ServiceDeploymentWeight struct {
	ID             string
	TrafficPercent int
}

// This optional extension belongs to the same read-only transaction as access
// policy. It cannot fall back to pool reads with a different committed view.
type ServiceRoutingPolicyReader interface {
	ResolveServiceRelease(context.Context, string, string, string, string) (string, string, error)
	ServiceDeploymentOverrideAllowed(context.Context, string, string) (bool, error)
	ServiceDeploymentWeights(context.Context, string) ([]ServiceDeploymentWeight, error)
}

func (s servicePolicyReader) ResolveServiceRelease(ctx context.Context, caller, source, target, requested string) (string, string, error) {
	if requested != "" {
		if _, err := uuid.Parse(requested); err != nil {
			return "", "", ErrInvalidArgument
		}
	}
	if source == "" {
		return s.serviceReleaseWithoutMembership(ctx, caller, target, requested)
	}
	rows, err := sqlc.New().ReadServicePolicyReleaseCandidates(ctx, s.tx, sqlc.ReadServicePolicyReleaseCandidatesParams{
		CallerAppID: uuidToPgtype(caller), CallerDeploymentID: uuidToPgtype(source),
		TargetAppID: uuidToPgtype(target), RequestedReleaseID: uuidToPgtype(requested)})
	if err != nil {
		return "", "", err
	}
	if len(rows) == 0 {
		if requested != "" {
			return "", "", ErrNotFound
		}
		return s.serviceReleaseWithoutMembership(ctx, caller, target, requested)
	}
	if len(rows) != 1 || !rows[0].TargetLive {
		return "", "", ErrConflict
	}
	return pgUUIDString(rows[0].ID), pgUUIDString(rows[0].DeploymentID), nil
}

func (s servicePolicyReader) serviceReleaseWithoutMembership(ctx context.Context, caller, target, requested string) (string, string, error) {
	active, err := sqlc.New().ReadServicePolicyActiveRelease(ctx, s.tx, sqlc.ReadServicePolicyActiveReleaseParams{
		CallerAppID: uuidToPgtype(caller), TargetAppID: uuidToPgtype(target)})
	if err != nil {
		return "", "", err
	}
	if active || requested != "" {
		return "", "", ErrConflict
	}
	return "", "", nil
}

func (s servicePolicyReader) ServiceDeploymentOverrideAllowed(ctx context.Context, app, deployment string) (bool, error) {
	return sqlc.New().ReadServicePolicyDeploymentOverride(ctx, s.tx, sqlc.ReadServicePolicyDeploymentOverrideParams{
		AppID: uuidToPgtype(app), DeploymentID: uuidToPgtype(deployment)})
}

func (s servicePolicyReader) ServiceDeploymentWeights(ctx context.Context, app string) ([]ServiceDeploymentWeight, error) {
	rows, err := sqlc.New().ReadServicePolicyDeploymentWeights(ctx, s.tx, sqlc.ReadServicePolicyDeploymentWeightsParams{
		AppID: uuidToPgtype(app), RowLimit: int32(api.TrafficPolicyMaxDeployments + 1)})
	if err != nil {
		return nil, err
	}
	if len(rows) > api.TrafficPolicyMaxDeployments {
		return nil, errors.New("service policy deployment limit exceeded")
	}
	weights := make([]ServiceDeploymentWeight, 0, len(rows))
	for _, row := range rows {
		weights = append(weights, ServiceDeploymentWeight{ID: pgUUIDString(row.ID), TrafficPercent: int(row.TrafficPercent)})
	}
	return weights, nil
}
