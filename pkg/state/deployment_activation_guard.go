package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// DeploymentActivationGuardStore activates a revision only while the expected
// deployment still owns all traffic in its scope. All normal activation checks
// remain in force, and repeating an already completed activation is safe.
type DeploymentActivationGuardStore interface {
	MarkDeploymentLiveIfServing(context.Context, string, string) error
}

func (m *MemStore) MarkDeploymentLiveIfServing(ctx context.Context, id, expectedServingID string) error {
	return m.markDeploymentLive(ctx, id, false, false, expectedServingID)
}

func (s *PgStore) MarkDeploymentLiveIfServing(ctx context.Context, id, expectedServingID string) error {
	return s.markDeploymentLive(ctx, id, false, false, expectedServingID)
}

func (m *MemStore) deploymentActivationServingMatchesLocked(target Deployment, expected string) bool {
	var serving []Deployment
	for _, other := range m.deployments {
		if other.AppID == target.AppID && normalizedDeploymentScope(other.Scope) == normalizedDeploymentScope(target.Scope) &&
			other.Status == DeployLive && other.DeletedAt == nil && other.TrafficPercent > 0 {
			serving = append(serving, other)
		}
	}
	return len(serving) == 1 && serving[0].TrafficPercent == 100 &&
		(sameDeploymentID(serving[0].ID, expected) || sameDeploymentID(serving[0].ID, target.ID))
}

func deploymentActivationServingMatchesTx(ctx context.Context, db sqlc.DBTX, target Deployment, expected string) error {
	if expected == "" {
		return ErrConflict
	}
	matches, err := sqlc.New().DeploymentActivationServingMatches(ctx, db, sqlc.DeploymentActivationServingMatchesParams{
		AppID: mustPgUUID(target.AppID), Scope: normalizedDeploymentScope(target.Scope),
		TargetID: mustPgUUID(target.ID), ExpectedID: mustPgUUID(expected),
	})
	if err != nil {
		return err
	}
	if !matches {
		return ErrConflict
	}
	return nil
}
