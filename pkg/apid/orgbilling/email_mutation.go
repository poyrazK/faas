package orgbilling

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// CustomerEmailSyncError distinguishes a provider refusal from a local
// mutation failure, preserving the billing-email route's error contract.
type CustomerEmailSyncError struct{ Err error }

func (e *CustomerEmailSyncError) Error() string {
	return fmt.Sprintf("sync billing customer email: %v", e.Err)
}
func (e *CustomerEmailSyncError) Unwrap() error { return e.Err }

// ChangeEmail serializes the entire provider/local change on the financial
// subject, including requests handled by different apid processes. The store
// supplies the current qualified binding while its subject lock is held.
func ChangeEmail(ctx context.Context, store any, orgID, provider, email string, syncer billing.CustomerEmailUpdater) error {
	writer, ok := store.(state.OrgBillingEmailMutationStore)
	if !ok {
		return state.ErrInvalidArgument
	}
	return writer.UpdateOrgBillingEmailWithSync(ctx, orgID, provider, email, func(ctx context.Context, target state.Account) error {
		if syncer == nil || target.ProviderCustomerID == "" {
			return nil
		}
		if err := syncer.UpdateCustomerEmail(ctx, target); err != nil {
			return &CustomerEmailSyncError{Err: err}
		}
		return nil
	})
}
