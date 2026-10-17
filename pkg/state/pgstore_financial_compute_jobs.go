package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Hold this account lock through the job insert. Activation and job definition
// creation and legacy writers share the same policy activation boundary.
func financialComputeJobCreationAllowed(ctx context.Context, tx sqlc.DBTX, account string) error {
	id, err := parsePgUUID(account)
	if err != nil {
		return ErrInvalidArgument
	}
	q := sqlc.New()
	if _, err := q.FinancialBudgetAccountLock(ctx, tx, id); err != nil {
		return financialBudgetStoreError(err)
	}
	return nil
}
