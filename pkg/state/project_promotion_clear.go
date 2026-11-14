package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ProjectPromotionClearStore clears a promotion with no predecessor only while
// its candidate is the sole live revision in the scope. Dark revisions count:
// ordinary failure recovery would otherwise give one of them traffic.
type ProjectPromotionClearStore interface {
	ClearProjectPromotionDeployment(context.Context, string, string, string) error
}

const projectPromotionClearMessage = "environment promotion rollback"

// projectPromotionClearDetail says why a promotion's only release was
// retired. A bare "environment promotion rollback" with no error code read as
// an unexplained failure (deploy-path review P7).
func projectPromotionClearDetail(promotionID string) string {
	return projectPromotionClearMessage + ": promotion " + promotionID +
		" was rolled back and this was the first release in its environment, so there was no earlier release to restore; it was retired, not failed by the app"
}

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
	d.ErrorCode = api.CodePromotionRolledBack
	m.failDeploymentLocked(d, projectPromotionClearDetail(promotionID))
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
	message := projectPromotionClearDetail(promotionID)
	stages, err := failedDeploymentStageStateTx(ctx, tx, deploymentID, message)
	if err != nil {
		return err
	}
	changed, err := sqlc.New().ClearProjectPromotionDeployment(ctx, tx, sqlc.ClearProjectPromotionDeploymentParams{
		AppID: mustPgUUID(appID), DeploymentID: mustPgUUID(deploymentID),
		Reason: "environment promotion/" + promotionID, StageState: stages, Message: message,
	})
	if err != nil {
		return mapErr(err)
	}
	if changed != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}
