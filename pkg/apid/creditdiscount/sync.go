package creditdiscount

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// Store is what credit discounts read and write.
type Store interface {
	state.CreditDiscountStore
	BillingIdentity(ctx context.Context, accountID, provider string) (state.BillingIdentity, error)
	ListActiveCreditsForConsumption(ctx context.Context, accountID string) ([]state.AccountCredit, error)
	ConsumeAccountCredit(ctx context.Context, p state.ConsumeAccountCreditParams) (state.ConsumeAccountCreditResult, error)
	GetInvoiceByProviderID(ctx context.Context, accountID, provider, providerInvoiceID string) (state.Invoice, error)
}

// Syncer keeps each subject's credit discount in step with its credit.
type Syncer struct {
	Store    Store
	Provider billing.CreditDiscounter
	// ProviderName is the provider the discounts belong to ("polar").
	ProviderName string
	Log          *slog.Logger
	// Audit records a change; nil records nothing.
	Audit func(ctx context.Context, event, subjectID string, fields map[string]any)
	// RequestSpacing is the pause before each subject in a pass.
	RequestSpacing time.Duration

	locks sync.Map // subject ID → *sync.Mutex
}

// Outcome is what one sync did.
type Outcome struct {
	Plan Plan
	// DiscountID is the discount attached by Attach.
	DiscountID string
}

// Sync attaches, replaces or removes subject's credit discount. One subject
// is synced at a time in this process; two replicas that race leave the
// subscription carrying one discount, which the next sync settles.
func (s *Syncer) Sync(ctx context.Context, subjectID string) (Outcome, error) {
	mu, _ := s.locks.LoadOrStore(subjectID, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()

	identity, err := s.Store.BillingIdentity(ctx, subjectID, s.ProviderName)
	if errors.Is(err, state.ErrNotFound) || (err == nil && identity.SubscriptionID == "") {
		return Outcome{Plan: Plan{Action: Hold, Reason: ReasonNoSubscription}}, nil
	}
	if err != nil {
		return Outcome{}, fmt.Errorf("credit discount: billing identity: %w", err)
	}
	target, err := s.Provider.CreditDiscountTarget(ctx, identity.SubscriptionID)
	if errors.Is(err, billing.ErrSubscriptionGone) {
		return Outcome{Plan: Plan{Action: Hold, Reason: ReasonSubscriptionGone}}, nil
	}
	if err != nil {
		return Outcome{}, fmt.Errorf("credit discount: read subscription: %w", err)
	}
	credits, err := s.Store.ListActiveCreditsForConsumption(ctx, subjectID)
	if err != nil {
		return Outcome{}, fmt.Errorf("credit discount: credits: %w", err)
	}
	attached, err := s.Store.AttachedCreditDiscounts(ctx, subjectID, s.ProviderName)
	if err != nil {
		return Outcome{}, fmt.Errorf("credit discount: attached discounts: %w", err)
	}
	current, foreign, err := s.carried(ctx, target.DiscountID, subjectID)
	if err != nil {
		return Outcome{}, err
	}
	plan := Decide(Available(credits, target.RenewsAt), target, current, foreign)
	out := Outcome{Plan: plan}
	switch plan.Action {
	case Attach:
		out.DiscountID, err = s.attach(ctx, subjectID, identity.SubscriptionID, plan.Amount, target.Currency)
		if err != nil {
			return out, err
		}
	case Detach:
		if err := s.Provider.SetSubscriptionDiscount(ctx, identity.SubscriptionID, ""); err != nil {
			return out, fmt.Errorf("credit discount: remove: %w", err)
		}
		s.audit(ctx, "credit.discount_detached", subjectID, map[string]any{"provider_discount_id": current.ProviderDiscountID})
	}
	// Gregale's discounts the subscription no longer carries were replaced
	// or removed; retire them. Those already applied to an order are still
	// drawn when its webhook arrives.
	if plan.Action != Hold {
		s.retire(ctx, attached, out.DiscountID, keptDiscount(plan, current))
	}
	return out, nil
}

// carried resolves the discount the subscription carries: Gregale's attached
// discount, or foreign when Gregale did not create it.
func (s *Syncer) carried(ctx context.Context, discountID, subjectID string) (*state.CreditDiscount, bool, error) {
	if discountID == "" {
		return nil, false, nil
	}
	d, err := s.Store.CreditDiscountByProviderID(ctx, s.ProviderName, discountID)
	if errors.Is(err, state.ErrNotFound) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("credit discount: carried discount: %w", err)
	}
	if d.SubjectID != subjectID {
		return nil, true, nil
	}
	if d.State != state.CreditDiscountAttached {
		// A once discount an order already used, or one Gregale retired.
		return nil, false, nil
	}
	return &d, false, nil
}

// attach creates the discount, records it, then puts it on the subscription.
// It is recorded before it is attached, so an order can never carry a credit
// discount Gregale does not know, which would leave the credit undrawn.
func (s *Syncer) attach(ctx context.Context, subjectID, subscriptionID string, amount int64, currency string) (string, error) {
	discountID, err := s.Provider.CreateCreditDiscount(ctx, subjectID, amount, currency)
	if err != nil {
		return "", fmt.Errorf("credit discount: create: %w", err)
	}
	record, err := s.Store.CreateCreditDiscount(ctx, state.CreditDiscount{
		SubjectID: subjectID, Provider: s.ProviderName, ProviderSubscriptionID: subscriptionID,
		ProviderDiscountID: discountID, AmountCents: amount, Currency: currency,
	})
	if err != nil {
		s.deleteQuietly(ctx, discountID)
		return "", fmt.Errorf("credit discount: record: %w", err)
	}
	if err := s.Provider.SetSubscriptionDiscount(ctx, subscriptionID, discountID); err != nil {
		if detachErr := s.Store.DetachCreditDiscount(ctx, record.ID); detachErr == nil {
			s.deleteQuietly(ctx, discountID)
		}
		return "", fmt.Errorf("credit discount: attach: %w", err)
	}
	s.audit(ctx, "credit.discount_attached", subjectID, map[string]any{
		"provider_discount_id": discountID, "amount_cents": amount, "currency": currency,
	})
	return discountID, nil
}

// retire marks Gregale's attached discounts other than keep detached and
// deletes them at the provider.
func (s *Syncer) retire(ctx context.Context, attached []state.CreditDiscount, keep ...string) {
	for _, d := range attached {
		if contains(keep, d.ProviderDiscountID) {
			continue
		}
		if err := s.Store.DetachCreditDiscount(ctx, d.ID); err != nil {
			s.logWarn("credit discount: retire", "subject", d.SubjectID, "err", err)
			continue
		}
		s.deleteQuietly(ctx, d.ProviderDiscountID)
	}
}

func keptDiscount(plan Plan, current *state.CreditDiscount) string {
	if plan.Action == Keep && current != nil {
		return current.ProviderDiscountID
	}
	return ""
}

func (s *Syncer) deleteQuietly(ctx context.Context, discountID string) {
	if err := s.Provider.DeleteDiscount(ctx, discountID); err != nil {
		s.logWarn("credit discount: delete at the provider", "provider_discount_id", discountID, "err", err)
	}
}

func (s *Syncer) audit(ctx context.Context, event, subjectID string, fields map[string]any) {
	if s.Audit != nil {
		fields["provider"] = s.ProviderName
		s.Audit(ctx, event, subjectID, fields)
	}
}

func (s *Syncer) logWarn(msg string, args ...any) {
	if s.Log != nil {
		s.Log.Warn(msg, args...)
	}
}

func contains(values []string, v string) bool {
	for _, value := range values {
		if value != "" && value == v {
			return true
		}
	}
	return false
}

// RunOnce syncs every subject with credit left or a discount attached. A
// subject that fails is retried by the next pass.
func (s *Syncer) RunOnce(ctx context.Context) (checked, failed int, err error) {
	subjects, err := s.Store.CreditDiscountSubjects(ctx, s.ProviderName)
	if err != nil {
		return 0, 0, fmt.Errorf("credit discount: subjects: %w", err)
	}
	spacing := s.RequestSpacing
	if spacing == 0 {
		spacing = api.BillingReconcileRequestSpacing
	}
	for _, subject := range subjects {
		if err := pause(ctx, spacing); err != nil {
			return checked, failed, err
		}
		checked++
		if _, err := s.Sync(ctx, subject); err != nil {
			failed++
			s.logWarn("credit discount: sync", "subject", subject, "err", err)
		}
	}
	return checked, failed, nil
}

func pause(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
