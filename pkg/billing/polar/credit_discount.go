package polar

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/billing"
)

var _ billing.CreditDiscounter = (*Provider)(nil)

// CreditDiscountName is the name Polar shows the customer for a credit
// discount.
const CreditDiscountName = "Gregale credit"

// CreditDiscountTarget reads the subscription a credit discount goes on
// (ADR-20261104212028). Polar refuses subscription changes while a
// cancellation or plan change is scheduled.
func (p *Provider) CreditDiscountTarget(ctx context.Context, subscriptionID string) (billing.CreditDiscountTarget, error) {
	sub, err := p.readSubscription(ctx, subscriptionID)
	if err != nil {
		return billing.CreditDiscountTarget{}, err
	}
	discountID := sub.DiscountID
	if discountID == "" && sub.Discount != nil {
		discountID = sub.Discount.ID
	}
	status := strings.ToLower(sub.Status)
	return billing.CreditDiscountTarget{
		Accepting:  (status == "active" || status == "trialing") && !sub.CancelAtPeriodEnd && sub.PendingUpdate == nil,
		DiscountID: discountID,
		Currency:   strings.ToLower(strings.TrimSpace(sub.Currency)),
		RenewsAt:   sub.CurrentPeriodEnd,
	}, nil
}

// CreateCreditDiscount creates a one-time fixed discount without a code, so
// only the API can apply it, redeemable once. Polar does not honour
// Idempotency-Key, so the request is sent once: a retry could create a second
// discount.
func (p *Provider) CreateCreditDiscount(ctx context.Context, subjectID string, amountCents int64, currency string) (string, error) {
	currency = strings.ToLower(strings.TrimSpace(currency))
	if amountCents <= 0 || len(currency) != 3 {
		return "", fmt.Errorf("polar: credit discount: invalid amount %d %q", amountCents, currency)
	}
	body := map[string]any{
		"name":            CreditDiscountName,
		"type":            "fixed",
		"duration":        "once",
		"amounts":         map[string]int64{currency: amountCents},
		"max_redemptions": 1,
		"metadata": map[string]any{
			"faas_subject_id": subjectID,
			"faas_purpose":    "account_credit",
		},
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := p.doJSONOnce(ctx, http.MethodPost, "/v1/discounts", body, &out, ""); err != nil {
		return "", fmt.Errorf("polar: create credit discount subject=%s: %w", subjectID, err)
	}
	if out.ID == "" {
		return "", fmt.Errorf("polar: create credit discount subject=%s: empty id", subjectID)
	}
	return out.ID, nil
}

// SetSubscriptionDiscount puts discountID on the subscription, or removes the
// discount when it is "". Setting the same value twice changes nothing, so
// the request may be retried.
func (p *Provider) SetSubscriptionDiscount(ctx context.Context, subscriptionID, discountID string) error {
	if strings.TrimSpace(subscriptionID) == "" {
		return errors.New("polar: set subscription discount: empty subscription id")
	}
	var value any
	if discountID != "" {
		value = discountID
	}
	path := "/v1/subscriptions/" + url.PathEscape(subscriptionID)
	var sub subscriptionResponse
	if err := p.doJSON(ctx, http.MethodPatch, path, map[string]any{"discount_id": value}, &sub, ""); err != nil {
		if hasStatus(err, http.StatusNotFound) {
			return fmt.Errorf("polar: set subscription discount %s: %w", subscriptionID, billing.ErrSubscriptionGone)
		}
		return fmt.Errorf("polar: set subscription discount %s: %w", subscriptionID, err)
	}
	return nil
}

// DeleteDiscount deletes a credit discount no subscription carries.
func (p *Provider) DeleteDiscount(ctx context.Context, discountID string) error {
	if strings.TrimSpace(discountID) == "" {
		return nil
	}
	err := p.doJSON(ctx, http.MethodDelete, "/v1/discounts/"+url.PathEscape(discountID), nil, nil, "")
	if err != nil && !hasStatus(err, http.StatusNotFound) {
		return fmt.Errorf("polar: delete discount %s: %w", discountID, err)
	}
	return nil
}
