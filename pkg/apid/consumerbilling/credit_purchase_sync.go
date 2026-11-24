package consumerbilling

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

const (
	creditSyncInterval = 30 * time.Second
	creditSyncLease    = 2 * time.Minute
	creditSyncBatch    = 20
)

// StartCreditPurchaseSync reconciles outstanding payments even after an owner
// disables new purchases or removes the customer billing entry point.
func (s *Service) StartCreditPurchaseSync(ctx context.Context, log *slog.Logger) {
	go func() {
		tick := time.NewTicker(creditSyncInterval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if err := s.SyncCreditPurchases(ctx); err != nil {
					log.Warn("consumer credit purchase sync", "error", err)
				}
			}
		}
	}()
}

// SyncCreditPurchases leases orders for verification with the account's
// Stripe connection. The store commits a paid receipt and grant together.
func (s *Service) SyncCreditPurchases(ctx context.Context) error {
	store, ok := s.Host.Store().(state.APIConsumerCreditPackStore)
	if !ok {
		return nil
	}
	now := s.now()
	purchases, err := store.ClaimConsumerCreditPurchases(ctx, now, now.Add(creditSyncLease), creditSyncBatch)
	if err != nil {
		return fmt.Errorf("claim credit purchases: %w", err)
	}
	var problems []error
	for _, purchase := range purchases {
		purchaseCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := s.syncCreditPurchase(purchaseCtx, store, purchase)
		cancel()
		if err != nil {
			problems = append(problems, fmt.Errorf("credit purchase %s: %w", purchase.ID, err))
		}
	}
	return errors.Join(problems...)
}

func (s *Service) syncCreditPurchase(ctx context.Context, store state.APIConsumerCreditPackStore, purchase state.APIConsumerCreditPurchase) error {
	// No URL is returned to the buyer before a session is persisted.
	if purchase.SessionID == "" {
		return store.ExpireConsumerCreditPurchase(ctx, purchase.ID, "")
	}
	key, live, problem := s.Host.StripeKey(ctx, purchase.AccountID)
	if problem != nil || live != purchase.Livemode {
		return errors.New("the Stripe connection for this payment is unavailable")
	}
	session, err := s.stripe().GetCreditPackCheckout(ctx, key, purchase.SessionID)
	if err != nil {
		return errors.New("could not read Stripe Checkout")
	}
	if !matchesCreditCheckout(purchase, session) {
		return errors.New("Stripe Checkout does not match the saved purchase")
	}
	switch {
	case session.Status == "complete" && session.PaymentStatus == "paid":
		return store.CreditConsumerPurchase(ctx, purchase.ID, purchase.SessionID)
	case session.Status == "expired" && session.PaymentStatus == "unpaid":
		return store.ExpireConsumerCreditPurchase(ctx, purchase.ID, purchase.SessionID)
	default:
		return nil
	}
}
