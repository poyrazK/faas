package paddle

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/PaddleHQ/paddle-go-sdk/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
)

// faasPlanNamePrefix is the product-name prefix EnsurePlanProducts
// matches on during list-then-create. Mirrors Stripe's Nickname
// pattern (Paddle has no Nickname — Name is the canonical handle).
// Idempotent: a redelivered boot that finds these products active
// skips creation entirely.
const faasPlanNamePrefix = "faas-plan-"

// planToProductName yields the Name Paddle sees when EnsurePlanProducts
// creates the product. Names are prefixed `faas-plan-` so redelivered
// boots find the existing product via ListProducts(Name=) — list-then-
// create is the Paddle-canonical idempotent pattern (Stripe uses
// Nicknames; Paddle has none).
func planToProductName(plan api.Plan) string {
	return faasPlanNamePrefix + string(plan)
}

// planProducts iterates the platform's billable plans. `Free` is
// intentionally excluded — it has no recurring line item (apid tracks
// it at the account-status level, not in the catalog). Adding Free
// would fail at CreateProduct because there's no monthly price to
// attach.
func planProducts() []api.Plan {
	return []api.Plan{api.PlanHobby, api.PlanPro, api.PlanScale}
}

// planMonthlyMillicents + planOverageMillicents removed: the financial-
// model source of truth is pkg/api/limits.go (planLimits.PriceMillicents
// + OverageMillicentsPerGBHour). The shared wrappers
// billing.PlanMonthlyMillicents and billing.PlanOverageMillicentsPerGBHour
// in pkg/billing/plans.go delegate there, so the per-provider call sites
// below don't drift from the financial model.

// millicentsToPaddleAmount converts integer millicents (project's
// 1000-decimal currency unit) to Paddle's "lowest denomination in a
// string" wire format. EUR uses cents, so 1 millicent = 0.001 cents
// which doesn't fit a String → round to nearest cent via the fact
// that all platform prices are exact cent multiples: €9 = 900 cents,
// 900 cents × 1000 mc/cent = 900_000 millicents. The helper then
// divides by 1000 to land at cents (the wire unit Paddle wants).
//
// Paddle's Money struct docstring: "Amount in the lowest
// denomination for the currency, e.g. 10 USD = 1000 (cents).
// Although represented as a string, this value must be a valid
// integer." For EUR that's integer cents. Source of truth:
// paddle-go-sdk/v5@v5.2.0 currencies.go.
func millicentsToPaddleAmount(mc int64) string {
	return strconv.FormatInt(mc/1000, 10)
}

// planPriceSpec is the shape we POST to /prices. Paddle's price-
// create accepts a small set of dimensions; we keep Description as
// the cross-check that matches the product name so re-EnsurePlanProducts
// stays idempotent (the list-then-create loop iterates and matches
// by description).
type planPriceSpec struct {
	description string
	millicents  int64
}

// ensureProducts is the EnsurePlanProducts body. Lists active products
// with the faas-plan- prefix; for each plan that doesn't have one,
// creates the product + monthly recurring price + flat-rate overage
// line item. Paddle has no metered-subscription equivalent to Stripe,
// so overage is a flat-rate monthly line-item price posted at month-
// rollover (see usage.go).
//
// On a successful run the catalog maps plan → pro_… (product ID)
// AND plan → pri_… (monthly price) AND plan → pri_… (overage price).
// meterd's quota + dunning timers use the overage price handle.
func (p *Provider) ensureProducts(ctx context.Context) error {
	if p.client == nil {
		return errors.New("paddle: SDK not initialized")
	}

	// 1. List active products with the faas-plan- prefix. Status
	//    filter is Paddle's documented list-filter knob, so the query
	//    avoids matching archived products from prior re-catalogues.
	//    Collection[*Product] is the SDK's iterator shape.
	products, err := p.client.ListProducts(ctx, &paddle.ListProductsRequest{
		Status: []string{string(paddle.StatusActive)},
	})
	if err != nil {
		return fmt.Errorf("paddle: ListProducts: %w", err)
	}
	byName := map[string]*paddle.Product{}
	if products != nil {
		_ = products.Iter(ctx, func(prod *paddle.Product) (bool, error) {
			if prod == nil {
				return true, nil
			}
			if len(prod.Name) >= len(faasPlanNamePrefix) &&
				prod.Name[:len(faasPlanNamePrefix)] == faasPlanNamePrefix {
				byName[prod.Name] = prod
			}
			return true, nil
		})
	}

	// 2. For each plan, ensure the product exists, then ensure each
	//    of its prices exists (monthly + overage). Catalog writes
	//    happen under catalog.mu.
	p.catalog.mu.Lock()
	defer p.catalog.mu.Unlock()

	for _, plan := range planProducts() {
		prod, ok := byName[planToProductName(plan)]
		if !ok {
			desc := fmt.Sprintf("one-box FaaS %s plan", plan)
			created, cerr := p.client.CreateProduct(ctx, &paddle.CreateProductRequest{
				Name:        planToProductName(plan),
				TaxCategory: paddle.TaxCategoryStandard,
				Description: &desc,
			})
			if cerr != nil {
				return fmt.Errorf("paddle: CreateProduct plan=%s: %w", plan, cerr)
			}
			if created == nil || created.ID == "" {
				return fmt.Errorf("paddle: CreateProduct returned empty ID for plan=%s", plan)
			}
			prod = created
		}
		p.catalog.planCustomers[plan] = prod.ID

		monthly := planPriceSpec{
			description: planToProductName(plan) + "-monthly",
			millicents:  billing.PlanMonthlyMillicents(plan),
		}
		overage := planPriceSpec{
			description: planToProductName(plan) + "-overage",
			millicents:  billing.PlanOverageMillicentsPerGBHour(),
		}
		if err := p.ensurePriceForProduct(ctx, prod.ID, plan, monthly, p.catalog.planMonthly); err != nil {
			return err
		}
		if err := p.ensurePriceForProduct(ctx, prod.ID, plan, overage, p.catalog.planOverage); err != nil {
			return err
		}
	}
	return nil
}

// ensurePriceForProduct looks up an existing price by description on
// a product, creating it if absent. outMap is either planMonthly or
// planOverage; the chosen key is plan. Idempotency: ListPrices by
// product_id (the SDK has no description filter), then a local
// description match — creates only on mismatch.
func (p *Provider) ensurePriceForProduct(
	ctx context.Context,
	productID string,
	plan api.Plan,
	spec planPriceSpec,
	outMap map[api.Plan]string,
) error {
	prices, err := p.client.ListPrices(ctx, &paddle.ListPricesRequest{
		ProductID: []string{productID},
	})
	if err != nil {
		return fmt.Errorf("paddle: ListPrices product=%s: %w", productID, err)
	}
	if prices != nil {
		matchErr := prices.IterErr(ctx, func(price *paddle.Price) error {
			if price == nil || price.Description != spec.description {
				return nil
			}
			outMap[plan] = price.ID
			return paddle.ErrStopIteration
		})
		if matchErr != nil {
			return fmt.Errorf("paddle: scan prices product=%s: %w", productID, matchErr)
		}
	}
	if _, ok := outMap[plan]; ok {
		return nil // already ensured
	}

	amount := millicentsToPaddleAmount(spec.millicents)
	created, cerr := p.client.CreatePrice(ctx, &paddle.CreatePriceRequest{
		ProductID:   productID,
		Description: spec.description,
		UnitPrice: paddle.Money{
			Amount:       amount,
			CurrencyCode: paddle.CurrencyCodeEUR,
		},
		BillingCycle: &paddle.Duration{
			Interval:  paddle.IntervalMonth,
			Frequency: 1,
		},
	})
	if cerr != nil {
		return fmt.Errorf("paddle: CreatePrice product=%s plan=%s: %w", productID, plan, cerr)
	}
	if created == nil || created.ID == "" {
		return fmt.Errorf("paddle: CreatePrice returned empty ID for product=%s plan=%s", productID, plan)
	}
	outMap[plan] = created.ID
	return nil
}

// snapshotPlans / snapshotOverage are read-side helpers used by
// EnsurePlanProducts' status log line. Held under catalog.RLock so
// they see a consistent point-in-time view.
func (p *Provider) snapshotPlans() map[api.Plan]string {
	p.catalog.mu.RLock()
	defer p.catalog.mu.RUnlock()
	out := map[api.Plan]string{}
	for k, v := range p.catalog.planMonthly {
		out[k] = v
	}
	return out
}

func (p *Provider) snapshotOverage() map[api.Plan]string {
	p.catalog.mu.RLock()
	defer p.catalog.mu.RUnlock()
	out := map[api.Plan]string{}
	for k, v := range p.catalog.planOverage {
		out[k] = v
	}
	return out
}
