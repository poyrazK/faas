package state

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

// Limit the number of exact billing reviews performed for one page.
const productOwnerBillingQueuePageSize = 25

type ProductOwnerBillingQueueStore interface {
	ListProductOwnerBillingQueue(ctx context.Context, account, app, period, cursor string) (ProductOwnerBillingQueue, error)
}

type ProductOwnerBillingQueue struct {
	Items      []ProductOwnerBillingQueueItem
	NextCursor string
}

type ProductOwnerBillingQueueItem struct {
	TenantID, Name, Currency        string
	PlanFee, RunCharges, Unresolved int64
}

type productOwnerBillingCandidate struct {
	tenantID, name string
}

func productOwnerBillingQueueCursor(account, app, period, raw string, now time.Time) (productOwnerCursor, error) {
	return productOwnerMonthCursor("billing-queue", account, app, period, raw, now)
}

func productOwnerMonthCursor(kind, account, app, period, raw string, now time.Time) (productOwnerCursor, error) {
	if productcontrol.ValidatePeriod(period) != nil || period >= productcontrol.Period(now) {
		return productOwnerCursor{}, ErrInvalidArgument
	}
	cursor, err := decodeProductOwnerCursor(raw, productOwnerCursorScope(kind, account, app, "", period), false)
	if err == nil && raw != "" && cursor.Period != period {
		return cursor, ErrInvalidArgument
	}
	cursor.Period = period
	return cursor, err
}

func productOwnerBillingQueue(account, app string, cursor productOwnerCursor, candidates []productOwnerBillingCandidate, review func(ProductBillingScope) (api.ProductBillingReview, error)) (ProductOwnerBillingQueue, error) {
	page := ProductOwnerBillingQueue{Items: []ProductOwnerBillingQueueItem{}}
	if len(candidates) > productOwnerBillingQueuePageSize {
		candidates = candidates[:productOwnerBillingQueuePageSize]
		// Advance past candidates whose exact fee is zero or whose statement
		// was finalized between discovery and review.
		cursor.ID = candidates[len(candidates)-1].tenantID
		page.NextCursor = encodeProductOwnerCursor(cursor)
	}
	for _, candidate := range candidates {
		billing, err := review(ProductBillingScope{AccountID: account, AppID: app, TenantID: candidate.tenantID, Period: cursor.Period})
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return ProductOwnerBillingQueue{}, err
		}
		if billing.Finalized != nil || (billing.Starts == 0 && billing.AmountMillicents == 0) {
			continue
		}
		var fee int64
		for _, line := range billing.Fees {
			fee += line.AmountMillicents
		}
		page.Items = append(page.Items, ProductOwnerBillingQueueItem{
			TenantID: candidate.tenantID, Name: candidate.name, Currency: billing.Currency,
			PlanFee: fee, RunCharges: billing.AmountMillicents - fee, Unresolved: billing.Unresolved,
		})
	}
	return page, nil
}
