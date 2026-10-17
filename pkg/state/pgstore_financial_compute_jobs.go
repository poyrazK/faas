package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Hold this account lock through the job insert. Activation and job definition
// creation must never both pass based on a stale view of the other operation.
func financialComputeJobCreationAllowed(ctx context.Context, tx sqlc.DBTX, account string) error {
	id, err := parsePgUUID(account)
	if err != nil {
		return ErrInvalidArgument
	}
	q := sqlc.New()
	if _, err := q.FinancialBudgetAccountLock(ctx, tx, id); err != nil {
		return financialBudgetStoreError(err)
	}
	enabled, err := q.FinancialComputeAccountStrictEnabled(ctx, tx, id)
	if err != nil {
		return err
	}
	if enabled {
		return financial.ErrComputeUnavailable
	}
	return nil
}
