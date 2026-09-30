package state

import (
	"context"

	"github.com/jackc/pgx/v5"
)

func checkCapturedCloneObjectPreparationsTx(ctx context.Context, tx pgx.Tx, clone ProjectEnvironmentClone) error {
	ids := capturedCloneManagedObjectCredentials(clone.capturedValues)
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
	preparations, err := capturedCloneObjectPreparationsTx(ctx, tx, op, records, clone.capturedValues)
	if err != nil {
		return err
	}
	for _, sourceID := range ids {
		if validateClonePreparedObjectIdentity(clone, sourceID, preparations[sourceID]) != nil {
			return cloneObjectPreparationProofError(sourceID)
		}
	}
	return nil
}

func capturedCloneObjectPreparationsTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, records []projectCloneWorkloadRecord, captured map[string]projectCloneWorkloadValues) (map[string]ProjectEnvironmentCloneObjectCredentialPreparation, error) {
	result := map[string]ProjectEnvironmentCloneObjectCredentialPreparation{}
	ids := capturedCloneManagedObjectCredentials(captured)
	if len(ids) == 0 {
		return result, nil
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return nil, err
	}
	for _, sourceID := range ids {
		prepared, err := cloneObjectCredentialReceiptDB(ctx, tx, op, sourceID)
		if err != nil {
			return nil, cloneObjectPreparationProofError(sourceID, err)
		}
		request := cloneObjectCredentialReplayRequest(prepared)
		if err := validateCloneObjectCredentialRequest(op, views, request); err != nil {
			return nil, cloneObjectPreparationProofError(sourceID)
		}
		if err := validateCloneCredentialBucketTx(ctx, tx, op, views, request); err != nil {
			return nil, cloneObjectPreparationProofError(sourceID, err)
		}
		prepared, err = cloneObjectCredentialPreparationDB(ctx, tx, op, sourceID)
		if err != nil {
			return nil, cloneObjectPreparationProofError(sourceID, err)
		}
		result[sourceID] = prepared
	}
	return result, nil
}
