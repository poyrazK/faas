package stripe

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
	stripe "github.com/stripe/stripe-go"
)

// EnsurePlanProducts is the idempotent product/price setup for the four
// plans (spec §4.7). The lookup keys are:
//
//	"<plan>-monthly"  → monthly subscription price (paid plans only)
//	"gb_ram_hour"     → metered overage price (all paid plans share)
//
// Free has no monthly line — overage isn't billed; meterd's hard stop
// is the only signal (spec §4.7).
//
// stripe-go v70 has no `lookup_key` on PlanParams (that's v74+), so
// idempotency comes from plan.List + Nickname match + plan.New fallback:
// every redelivered boot hits the same Stripe state, finds the plan by
// Nickname, and skips the New call.
func (c *Client) EnsurePlanProducts(ctx context.Context) error {
	if c.api == nil {
		return fmt.Errorf("stripex: cannot EnsurePlanProducts without apiKey")
	}
	// One shared metered price for all paid plans. The PlanPriceIDs
	// key matches the wire format used by meterd's PushUsageRecord.
	metered := "faas-metered-gb-ram-hour"
	id, err := c.findOrCreatePlan(ctx, metered, &stripe.PlanParams{
		Nickname:       stripe.String(metered),
		Currency:       stripe.String("usd"),
		Interval:       stripe.String("month"),
		UsageType:      stripe.String("metered"),
		AggregateUsage: stripe.String("sum"),
		BillingScheme:  stripe.String("per_unit"),
		Amount:         stripe.Int64(0),
	})
	if err != nil {
		return err
	}
	c.PlanPriceIDs[metered] = id

	// One monthly Plan per paid tier.
	for _, p := range []api.Plan{api.PlanHobby, api.PlanPro, api.PlanScale} {
		nick := "faas-plan-" + string(p)
		id, err := c.findOrCreatePlan(ctx, nick, &stripe.PlanParams{
			Nickname:  stripe.String(nick),
			Currency:  stripe.String("usd"),
			Interval:  stripe.String("month"),
			UsageType: stripe.String("licensed"),
			Amount:    stripe.Int64(billing.PlanMonthlyMillicents(p)),
		})
		if err != nil {
			return err
		}
		c.PlanPriceIDs[string(p)+":monthly"] = id
	}
	return nil
}

// findOrCreatePlan is the idempotency primitive for EnsurePlanProducts.
// Lists active plans, returns the first one whose Nickname matches;
// falls back to creating a new Plan with the supplied params when no
// match is found. A redelivered boot sees the same Nickname → no-op.
func (c *Client) findOrCreatePlan(_ context.Context, nickname string, params *stripe.PlanParams) (string, error) {
	iter := c.api.Plans.List(&stripe.PlanListParams{Active: stripe.Bool(true)})
	for iter.Plan() != nil {
		p := iter.Plan()
		if p.Nickname == nickname {
			return p.ID, nil
		}
		if !iter.Next() {
			break
		}
	}
	created, err := c.api.Plans.New(params)
	if err != nil {
		return "", fmt.Errorf("stripex: Plans.New %s: %w", nickname, err)
	}
	return created.ID, nil
}

// CreateCustomer is the wrapper around stripe.Customers.New. We record
// the returned `cus_…` on the account row via
// UpdateAccountProviderCustomerID so the webhook + push paths can join.
// Metadata carries the faas account id + plan so the Stripe dashboard
// can pivot without a separate lookup.
func (c *Client) CreateCustomer(ctx context.Context, acct state.Account) (string, error) {
	if c.api == nil {
		return "", fmt.Errorf("stripex: cannot CreateCustomer without apiKey")
	}
	cus, err := c.api.Customers.New(stripeCustomerParams(acct))
	if err != nil {
		return "", fmt.Errorf("stripex: Customers.New account %s: %w", acct.ID, err)
	}
	if err := c.store.UpdateAccountProviderCustomerID(ctx, acct.ID, cus.ID); err != nil {
		return "", err
	}
	return cus.ID, nil
}

// SyncCustomerBillingInfo applies the current legal identity to an existing
// Stripe customer. The operation is safe to repeat: name/address are set to
// the same values and the single EU VAT identity is replaced only when its
// value changes. Empty values clear local provider metadata where Stripe
// supports clearing; tax IDs are deleted explicitly before adding the new
// value.
func (c *Client) SyncCustomerBillingInfo(ctx context.Context, acct state.Account) error {
	if c.api == nil {
		return fmt.Errorf("stripex: cannot SyncCustomerBillingInfo without apiKey")
	}
	if acct.ProviderCustomerID == "" {
		return fmt.Errorf("stripex: account %s has no provider customer", acct.ID)
	}
	updateParams := stripeCustomerParams(acct)
	// Stripe manages tax IDs through the customer tax_ids sub-resource;
	// tax_id_data is accepted on customer creation but not on update.
	updateParams.TaxIDData = nil
	if _, err := c.api.Customers.Update(acct.ProviderCustomerID, updateParams); err != nil {
		return fmt.Errorf("stripex: Customers.Update account %s: %w", acct.ID, err)
	}
	// The account model deliberately stores one tax identifier without a
	// country-specific type. EU VAT is the supported first slice; the PDF and
	// tax-network follow-ups can add a typed field without changing this API.
	iter := c.api.TaxIDs.List(&stripe.TaxIDListParams{Customer: stripe.String(acct.ProviderCustomerID)})
	matchedTaxID := false
	for iter.Next() {
		tax := iter.TaxID()
		if tax == nil {
			continue
		}
		if acct.TaxID != "" && tax.Value == acct.TaxID && string(tax.Type) == "eu_vat" {
			matchedTaxID = true
			continue
		}
		if _, err := c.api.TaxIDs.Del(tax.ID, &stripe.TaxIDParams{Customer: stripe.String(acct.ProviderCustomerID)}); err != nil {
			return fmt.Errorf("stripex: remove stale tax id account %s: %w", acct.ID, err)
		}
	}
	if err := iter.Err(); err != nil {
		return fmt.Errorf("stripex: list tax ids account %s: %w", acct.ID, err)
	}
	if acct.TaxID != "" && !matchedTaxID {
		if _, err := c.api.TaxIDs.New(&stripe.TaxIDParams{
			Customer: stripe.String(acct.ProviderCustomerID),
			Type:     stripe.String("eu_vat"),
			Value:    stripe.String(acct.TaxID),
		}); err != nil {
			return fmt.Errorf("stripex: add tax id account %s: %w", acct.ID, err)
		}
	}
	return nil
}

func stripeCustomerParams(acct state.Account) *stripe.CustomerParams {
	params := &stripe.CustomerParams{
		Email: stripe.String(acct.Email),
		Name:  stripe.String(acct.BusinessName),
		Params: stripe.Params{
			Metadata: map[string]string{
				"faas_account_id": acct.ID,
				"faas_plan":       string(acct.Plan),
			},
		},
	}
	if acct.BillingAddress != "" {
		params.Address = &stripe.AddressParams{Line1: stripe.String(acct.BillingAddress)}
	}
	if acct.TaxID != "" {
		params.TaxIDData = []*stripe.CustomerTaxIDDataParams{{
			Type:  stripe.String("eu_vat"),
			Value: stripe.String(acct.TaxID),
		}}
	}
	return params
}

// planMonthlyMillicents moved to pkg/billing/plans.go (PlanMonthlyMillicents).
