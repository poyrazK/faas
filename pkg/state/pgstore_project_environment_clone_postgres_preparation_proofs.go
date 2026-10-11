package state

import (
	"context"

	"github.com/jackc/pgx/v5"
)

func checkCapturedClonePostgresPreparationsTx(ctx context.Context, tx pgx.Tx, clone ProjectEnvironmentClone) error {
	ids := capturedCloneManagedPostgresBindings(clone.capturedValues)
	if len(ids) == 0 {
		return nil
	}
	op, err := lockCloneWorkloadOperationTx(ctx, tx, clone.AccountID, clone.ProjectID, clone.CloneOperationID)
	if err != nil {
		return err
	}
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return err
	}
	preparations, err := capturedClonePostgresPreparationsTx(ctx, tx, op, records, clone.capturedValues)
	if err != nil {
		return err
	}
	preparedIDs := map[string]bool{}
	for _, id := range clone.PreparedManagedBindingIDs {
		preparedIDs[id] = true
	}
	for _, sourceID := range ids {
		if !preparedIDs[preparations[sourceID].Binding.ID] {
			return clonePostgresPreparationProofError(sourceID)
		}
	}
	return nil
}

func capturedClonePostgresPreparationsTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, records []projectCloneWorkloadRecord, captured map[string]projectCloneWorkloadValues) (map[string]ProjectEnvironmentClonePostgresBindingPreparation, error) {
	result := map[string]ProjectEnvironmentClonePostgresBindingPreparation{}
	ids := capturedCloneManagedPostgresBindings(captured)
	if len(ids) == 0 {
		return result, nil
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return nil, err
	}
	for _, sourceID := range ids {
		// Locks on actual database, binding and secret rows retain the proof
		// until the enclosing materialization/publication transaction commits.
		want, err := clonePostgresBindingTargetTx(ctx, tx, op, views, sourceID)
		if err != nil {
			return nil, clonePostgresPreparationProofError(sourceID, err)
		}
		_, prepared, err := clonePostgresBindingLedgerTx(ctx, tx, want)
		if err != nil {
			return nil, clonePostgresPreparationProofError(sourceID, err)
		}
		if prepared == nil || validateClonePreparedPostgresIdentity(op, sourceID, want.AppID, *prepared) != nil {
			return nil, clonePostgresPreparationProofError(sourceID)
		}
		result[sourceID] = *prepared
	}
	return result, nil
}
