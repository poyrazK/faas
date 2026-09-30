package state

import (
	"context"

	"github.com/jackc/pgx/v5"
)

func checkCapturedCloneObjectPreparationsTx(ctx context.Context, tx pgx.Tx, clone ProjectEnvironmentClone) error {
	ids := capturedCloneManagedObjectCredentials(clone)
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
	views, err := cloneBindingViews(records)
	if err != nil {
		return err
	}
	for _, sourceID := range ids {
		prepared, err := cloneObjectCredentialReceiptDB(ctx, tx, op, sourceID)
		if err != nil {
			return cloneObjectPreparationProofError(sourceID, err)
		}
		if validateClonePreparedObjectIdentity(clone, sourceID, prepared) != nil {
			return cloneObjectPreparationProofError(sourceID)
		}
		request := cloneObjectCredentialReplayRequest(prepared)
		if err := validateCloneObjectCredentialRequest(op, views, request); err != nil {
			return cloneObjectPreparationProofError(sourceID)
		}
		if err := validateCloneCredentialBucketTx(ctx, tx, op, views, request); err != nil {
			return cloneObjectPreparationProofError(sourceID, err)
		}
		if _, err := cloneObjectCredentialPreparationDB(ctx, tx, op, sourceID); err != nil {
			return cloneObjectPreparationProofError(sourceID, err)
		}
	}
	return nil
}
