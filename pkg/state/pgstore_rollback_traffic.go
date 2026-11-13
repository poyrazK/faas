package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func rollbackTrafficTargetTx(ctx context.Context, tx pgx.Tx, d Deployment) (Deployment, error) {
	selection, ok := ctx.Value(rollbackTrafficKey{}).(rollbackTrafficSelection)
	if !ok {
		return d, nil
	}
	if !rollbackTrafficTargetValid(d, selection) {
		return d, ErrCheckedRollbackChanged
	}
	q := sqlc.New()
	enforced, err := q.SynchronousRollbackRequiresBindingCheck(ctx, tx, sqlc.SynchronousRollbackRequiresBindingCheckParams{AppID: mustPgUUID(d.AppID), Scope: normalizedDeploymentScope(d.Scope)})
	if err != nil {
		return d, err
	}
	if enforced {
		return d, ErrBindingReleaseRequired
	}
	if _, err := q.CheckedRollbackForTarget(ctx, tx, mustPgUUID(d.ID)); err == nil {
		return d, ErrCheckedRollbackRequired
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return d, err
	}
	r := api.RollbackOperation{AppID: d.AppID, Scope: normalizedDeploymentScope(d.Scope), TargetDeploymentID: d.ID, CurrentDeploymentID: selection.currentID}
	if err := rollbackCurrentMatches(ctx, tx, r); err != nil {
		return d, err
	}
	if err := preserveRollbackSuccessTx(ctx, tx, d); err != nil {
		return d, err
	}
	if err := q.ResetSynchronousRollbackTarget(ctx, tx, mustPgUUID(d.ID)); err != nil {
		return d, err
	}
	return resetRollbackTrafficTarget(d), nil
}
