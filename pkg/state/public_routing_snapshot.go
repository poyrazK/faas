// adr: 375
package state

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type PublicRoutingPolicyReader interface {
	VerifyPublicRoutingOwner(context.Context, string, string, string) error
	PublicDeploymentWeights(context.Context, string, string) ([]ServiceDeploymentWeight, error)
	ResolvePublicProjectRelease(context.Context, string, string, string) (string, string, error)
	PublicRevisionAllowed(context.Context, string, string, string) (bool, error)
	PublicHostPinAllowed(context.Context, string, string, string) (bool, error)
}

type PublicRoutingSnapshotStore interface {
	WithPublicRoutingSnapshot(context.Context, func(PublicRoutingPolicyReader) error) error
}

// Reuse the transaction mechanism while exposing only public routing reads.
func (s *PgStore) WithPublicRoutingSnapshot(ctx context.Context, read func(PublicRoutingPolicyReader) error) error {
	if read == nil {
		return ErrInvalidArgument
	}
	return s.WithServicePolicySnapshot(ctx, func(reader ServicePolicyReader) error {
		return read(reader.(PublicRoutingPolicyReader))
	})
}

func (s servicePolicyReader) VerifyPublicRoutingOwner(ctx context.Context, app, account, project string) error {
	verified, err := sqlc.New().ReadPublicRoutingOwner(ctx, s.tx, sqlc.ReadPublicRoutingOwnerParams{
		AppID: uuidToPgtype(app), AccountID: uuidToPgtype(account), ProjectID: uuidToPgtype(project)})
	if err != nil {
		return err
	}
	if !verified {
		return ErrNotFound
	}
	return nil
}

func (s servicePolicyReader) PublicDeploymentWeights(ctx context.Context, app, scope string) ([]ServiceDeploymentWeight, error) {
	rows, err := sqlc.New().ReadPublicRoutingWeights(ctx, s.tx, sqlc.ReadPublicRoutingWeightsParams{
		AppID: uuidToPgtype(app), Scope: normalizedDeploymentScope(scope), RowLimit: int32(api.TrafficPolicyMaxDeployments + 1)})
	if err != nil {
		return nil, err
	}
	if len(rows) > api.TrafficPolicyMaxDeployments {
		return nil, errors.New("public routing deployment limit exceeded")
	}
	weights := make([]ServiceDeploymentWeight, 0, len(rows))
	for _, row := range rows {
		weights = append(weights, ServiceDeploymentWeight{ID: pgUUIDString(row.ID), TrafficPercent: int(row.TrafficPercent)})
	}
	return weights, nil
}

func (s servicePolicyReader) ResolvePublicProjectRelease(ctx context.Context, app, scope, requested string) (string, string, error) {
	rows, err := sqlc.New().ReadPublicRoutingRelease(ctx, s.tx, sqlc.ReadPublicRoutingReleaseParams{
		AppID: uuidToPgtype(app), Scope: normalizedDeploymentScope(scope), RequestedReleaseID: uuidToPgtype(requested)})
	if err != nil {
		return "", "", err
	}
	if len(rows) == 0 {
		if requested != "" {
			return "", "", ErrNotFound
		}
		return "", "", nil
	}
	if len(rows) != 1 || !rows[0].DeploymentID.Valid || !rows[0].TargetLive {
		return "", "", ErrConflict
	}
	return pgUUIDString(rows[0].ID), pgUUIDString(rows[0].DeploymentID), nil
}

func (s servicePolicyReader) PublicRevisionAllowed(ctx context.Context, app, scope, deployment string) (bool, error) {
	return sqlc.New().ReadPublicRoutingRevision(ctx, s.tx, sqlc.ReadPublicRoutingRevisionParams{
		AppID: uuidToPgtype(app), Scope: normalizedDeploymentScope(scope), DeploymentID: uuidToPgtype(deployment)})
}

func (s servicePolicyReader) PublicHostPinAllowed(ctx context.Context, app, scope, deployment string) (bool, error) {
	return sqlc.New().ReadPublicRoutingHostPin(ctx, s.tx, sqlc.ReadPublicRoutingHostPinParams{
		AppID: uuidToPgtype(app), Scope: normalizedDeploymentScope(scope), DeploymentID: uuidToPgtype(deployment)})
}
