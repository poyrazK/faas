package state

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// WithRollbackReleaseReview records the exact release runs acknowledged by a
// plain rollback. Preparation persists them, bound to that preparation attempt.
func WithRollbackReleaseReview(ctx context.Context, runs []ReleaseRun) context.Context {
	return WithCheckedRollbackReleaseReview(ctx, runs)
}
func (m *MemStore) checkPlainRollbackReleasesLocked(ctx context.Context, d Deployment) error {
	_, prepared := m.rollbackPrepared[d.ID]
	_, synchronous := ctx.Value(rollbackTrafficKey{}).(rollbackTrafficSelection)
	_, switching := databaseSwitchLease(ctx)
	if !prepared && !synchronous && !switching {
		return nil
	}
	if err := m.checkAppRollbackGraphLocked(d.AppID, d.Scope, d.ID); err != nil {
		return err
	}
	if switching {
		return nil
	}
	review := m.plainRollbackReleaseReviews[d.ID]
	if synchronous {
		review = nil
	}
	return checkRollbackReleaseReview(d.ID, m.releaseRunsCrossedByLocked(d.AppID, d.ID), m.releasesUndoneByDatabaseSwitchesLocked(d.AppID, d.Scope), review)
}
func checkPlainRollbackReleasesTx(ctx context.Context, tx pgx.Tx, d Deployment) error {
	_, switching := databaseSwitchLease(ctx)
	q := sqlc.New()
	raw, err := q.ReadPreparedRollbackReleaseReview(ctx, tx, mustPgUUID(d.ID))
	if err != nil {
		return err
	}
	_, synchronous := ctx.Value(rollbackTrafficKey{}).(rollbackTrafficSelection)
	if len(raw) == 0 && !synchronous && !switching {
		return nil
	} // No prepared plain rollback.
	if err := checkAppRollbackGraphTx(ctx, tx, d.AppID, d.Scope, d.ID); err != nil {
		return err
	}
	if switching {
		return nil // The database restore's lease was validated before this guard.
	}
	var review []ReleaseRun
	if !synchronous && len(raw) > 0 {
		if err = json.Unmarshal(raw, &review); err != nil {
			return err
		}
	}
	runs, err := releaseRunsCrossedByDBTX(ctx, tx, d.AppID, d.ID)
	if err != nil {
		return err
	}
	undone, err := q.ListReleasesUndoneByDatabaseSwitches(ctx, tx, sqlc.ListReleasesUndoneByDatabaseSwitchesParams{AppID: mustPgUUID(d.AppID), Scope: normalizedDeploymentScope(d.Scope)})
	if err != nil {
		return err
	}
	return checkRollbackReleaseReview(d.ID, runs, undone, review)
}

// An absent marker returns SQL NULL; a stale/missing review returns [] so an
// older writer cannot accidentally reuse another attempt's acknowledgement.
func persistPlainRollbackReview(ctx context.Context, tx pgx.Tx, id string) error {
	runs := checkedRollbackReleaseReview(ctx)
	if runs == nil {
		runs = []ReleaseRun{}
	}
	raw, err := json.Marshal(runs)
	if err != nil {
		return err
	}
	return sqlc.New().WritePreparedRollbackReleaseReview(ctx, tx, sqlc.WritePreparedRollbackReleaseReviewParams{DeploymentID: mustPgUUID(id), Review: raw})
}
