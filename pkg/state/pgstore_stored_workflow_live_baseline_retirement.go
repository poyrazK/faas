// adr: 1305
package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func baselineRetirementSQL(ctx context.Context, db sqlc.DBTX, appID, name, alias string) (*api.StoredWorkflowLiveBaselineRetirement, error) {
	row, err := sqlc.New().GetStoredWorkflowLiveBaselineRetirement(ctx, db, sqlc.GetStoredWorkflowLiveBaselineRetirementParams{AppID: mustPgUUID(appID), Name: name, BaselineName: alias})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var receipt api.StoredWorkflowLiveBaselineRetirement
	if err = json.Unmarshal(row.Receipt, &receipt); err != nil {
		return nil, err
	}
	return &receipt, nil
}
func (s *PgStore) RetireStoredWorkflowLiveBaseline(ctx context.Context, appID, name, alias, id, actor, key string, request api.RetireStoredWorkflowLiveBaselineRequest) (receipt api.StoredWorkflowLiveBaselineRetirement, err error) {
	if err = validateBaselineRetirement(alias, retirementIdentityValues(id, actor, key), request.ExpectedRevision); err != nil {
		return receipt, err
	}
	id = uuid.MustParse(id).String()
	defer func() {
		mapped := baselineStoreError(err)
		if errors.Is(mapped, ErrStoredWorkflowLiveBaselineConflict) && ctx.Err() == nil {
			row, lookupErr := sqlc.New().GetStoredWorkflowLiveBaselineRetirementByID(ctx, s.pool, mustPgUUID(id))
			var prior api.StoredWorkflowLiveBaselineRetirement
			if lookupErr == nil && json.Unmarshal(row.Receipt, &prior) == nil && retirementReplay(prior, appID, name, alias, id, actor, key, request.ExpectedRevision) {
				receipt = prior
				err = nil
				return
			}
		}
		err = mapped
	}()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return receipt, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err = q.LockWorkflowRunAdmission(ctx, tx, appID); err != nil {
		return receipt, err
	}
	row, lookupErr := q.GetStoredWorkflowLiveBaselineRetirementByID(ctx, tx, mustPgUUID(id))
	if lookupErr == nil {
		if err = json.Unmarshal(row.Receipt, &receipt); err != nil {
			return receipt, err
		}
		if receipt.AppID != appID || receipt.Name != name {
			return api.StoredWorkflowLiveBaselineRetirement{}, ErrNotFound
		}
		if !retirementReplay(receipt, appID, name, alias, id, actor, key, request.ExpectedRevision) {
			return api.StoredWorkflowLiveBaselineRetirement{}, ErrStoredWorkflowLiveBaselineConflict
		}
		return receipt, nil
	}
	if !errors.Is(lookupErr, pgx.ErrNoRows) {
		return receipt, lookupErr
	}
	// Retirement needs no live deployment or usable evidence. Lock ownership
	// before fencing the automation row, following the existing lock order.
	if _, err = q.LockEventWorkflowTarget(ctx, tx, mustPgUUID(appID)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrNotFound
		}
		return receipt, err
	}
	if err = q.FenceStoredWorkflowLiveBaselineMutation(ctx, tx, sqlc.FenceStoredWorkflowLiveBaselineMutationParams{AppID: mustPgUUID(appID), Name: name}); err != nil {
		return receipt, err
	}
	retired, err := baselineRetirementSQL(ctx, tx, appID, name, alias)
	if err != nil {
		return receipt, err
	}
	if retired != nil {
		return receipt, baselineRetiredError()
	}
	prior, err := getBaselineHeadSQL(ctx, tx, appID, name, alias)
	if err != nil {
		return receipt, err
	}
	if prior.Revision != request.ExpectedRevision {
		return receipt, ErrStoredWorkflowLiveBaselineConflict
	}
	receipt = retiredBaseline(prior, id, actor, key)
	raw, err := json.Marshal(receipt)
	if err != nil {
		return receipt, err
	}
	if err = q.InsertStoredWorkflowLiveBaselineRetirement(ctx, tx, sqlc.InsertStoredWorkflowLiveBaselineRetirementParams{RetirementID: mustPgUUID(id), AppID: mustPgUUID(appID), Name: name, BaselineName: alias, Receipt: raw}); err != nil {
		return api.StoredWorkflowLiveBaselineRetirement{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return api.StoredWorkflowLiveBaselineRetirement{}, err
	}
	return receipt, nil
}
