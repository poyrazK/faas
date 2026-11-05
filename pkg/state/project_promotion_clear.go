package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ProjectPromotionClearStore clears a promotion with no predecessor only while
// its candidate is the sole live revision in the scope. Dark revisions count:
// ordinary failure recovery would otherwise give one of them traffic.
type ProjectPromotionClearStore interface {
	ClearProjectPromotionDeployment(context.Context, string, string, string) error
}

const projectPromotionClearMessage = "environment promotion rollback"

func (m *MemStore) ClearProjectPromotionDeployment(ctx context.Context, appID, deploymentID, promotionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[deploymentID]
	if !ok {
		return ErrNotFound
	}
	if promotionID == "" || d.AppID != appID || d.Reason != "environment promotion/"+promotionID ||
		d.Status != DeployLive || d.DeletedAt != nil || d.TrafficPercent != 0 && d.TrafficPercent != 100 {
		return ErrConflict
	}
	for id, other := range m.deployments {
		if id != d.ID && other.AppID == appID && normalizedDeploymentScope(other.Scope) == normalizedDeploymentScope(d.Scope) && other.Status == DeployLive {
			return ErrConflict
		}
	}
	if err := m.checkBindingReleaseFailureLocked(d); err != nil {
		return err
	}
	m.failDeploymentLocked(d, projectPromotionClearMessage)
	m.markDeploymentSnapshotsStaleLocked(d.ID)
	return nil
}

func (s *PgStore) ClearProjectPromotionDeployment(ctx context.Context, appID, deploymentID, promotionID string) error {
	if promotionID == "" {
		return ErrConflict
	}
	tx, err := s.beginDeploymentTrafficMutation(ctx, deploymentID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	stages, err := failedDeploymentStageStateTx(ctx, tx, deploymentID, projectPromotionClearMessage)
	if err != nil {
		return err
	}
	changed, err := sqlc.New().ClearProjectPromotionDeployment(ctx, tx, sqlc.ClearProjectPromotionDeploymentParams{
		AppID: mustPgUUID(appID), DeploymentID: mustPgUUID(deploymentID),
		Reason: "environment promotion/" + promotionID, StageState: stages,
	})
	if err != nil {
		return mapErr(err)
	}
	if changed != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}
