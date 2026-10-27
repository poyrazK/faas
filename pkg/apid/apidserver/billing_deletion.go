package apidserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// endSubscriptionForDeletion stops a deleted account's or organization's
// subscription from renewing (Hunt 17 H17-2). Deletion never touched the
// provider, so a deleted customer kept paying the plan fee every month after
// its data was gone. The subscription is cancelled at period end: the
// current, already paid period runs out and nothing is charged again.
//
// A subscription the provider reports as gone or no longer live needs
// nothing. Callers keep the deletion from proceeding on an error, so a
// provider outage can delay a deletion but never leave a deleted customer
// paying.
func (s *server) endSubscriptionForDeletion(ctx context.Context, subject state.Account) error {
	if s.billingProvider == nil {
		return nil
	}
	billingAcct, err := s.accountForActiveBillingProvider(ctx, subject)
	if err != nil {
		return fmt.Errorf("load billing identity: %w", err)
	}
	subscriptionID := billingAcct.StripeSubscriptionItem
	if subscriptionID == "" {
		return nil
	}
	if reader, ok := s.billingProvider.(billing.SubscriptionStateProvider); ok {
		current, err := reader.SubscriptionState(ctx, subscriptionID)
		if errors.Is(err, billing.ErrSubscriptionGone) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read billing subscription: %w", err)
		}
		if !current.Status.Live() {
			return nil
		}
	}
	if _, err := s.billingProvider.CancelAtPeriodEnd(ctx, billingAcct); err != nil && !errors.Is(err, billing.ErrAlreadyCancelled) {
		return fmt.Errorf("cancel billing subscription at period end: %w", err)
	}
	s.audit.Emit(ctx, "billing.subscription_cancel_for_deletion", &subject.ID, map[string]any{
		"subscription_id": subscriptionID,
	})
	return nil
}
