package stripex

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// EnsurePlanProducts is the idempotent product/price setup for the four
// plans. The lookup keys (Stripe `lookup_key` once we wire the SDK) are:
//
//	"<plan>-monthly"  → monthly subscription price (paid plans only)
//	"gb_ram_hour"     → metered overage price (all paid plans share)
//
// Free has no monthly line — overage isn't billed; meterd's hard stop
// is the only signal (spec §4.7).
//
// The body is a placeholder until stripe-go lands. Returning the same
// placeholder IDs on every call makes the call idempotent for the dev
// loop; the production swap is the SDK call + go.mod bump.
//
// TODO(m7-real-stripe): replace with product.Search + price.Search and
// fall back to product.Create + price.Create when not found.
func (c *Client) EnsurePlanProducts(_ context.Context) error {
	for _, p := range []api.Plan{api.PlanFree, api.PlanHobby, api.PlanPro, api.PlanScale} {
		if p == api.PlanFree {
			continue
		}
		c.PlanPriceIDs[string(p)+":monthly"] = "price_" + string(p) + "_monthly_dev"
	}
	c.PlanPriceIDs["gb_ram_hour"] = "price_gb_ram_hour_dev"
	return nil
}

// CreateCustomer is the wrapper around stripe.Customer.Create. We
// record the returned `cus_…` on the account row via
// UpdateAccountStripeCustomerID so the webhook + push paths can join.
// Real SDK call is gated behind a placeholder; the wire-up is identical
// when the SDK lands.
//
// TODO(m7-real-stripe): replace with stripe.Customer.Create using the
// account email + plan metadata.
func (c *Client) CreateCustomer(ctx context.Context, acct state.Account) (string, error) {
	stripeID := "cus_dev_" + acct.ID
	if err := c.store.UpdateAccountStripeCustomerID(ctx, acct.ID, stripeID); err != nil {
		return "", err
	}
	return stripeID, nil
}
