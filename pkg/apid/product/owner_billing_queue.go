package product

import (
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) ownerBillingQueue(r *http.Request, c *ownerContext, q url.Values) error {
	store, ok := s.Host.Store().(state.ProductOwnerBillingQueueStore)
	if !ok {
		return nil
	}
	now := time.Now().UTC()
	latest := productcontrol.Period(time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0))
	period := q.Get("billing_month")
	if !q.Has("billing_month") {
		period = latest
	}
	page, err := store.ListProductOwnerBillingQueue(r.Context(), c.account.ID, c.app.ID, period, q.Get("billing_cursor"))
	if err != nil {
		return err
	}
	view := &dashboard.ProductControlBillingQueue{Period: period, LatestPeriod: latest}
	for _, item := range page.Items {
		params := url.Values{"billing_month": {period}}
		anchor := "#billing"
		if item.Unresolved > 0 {
			params.Set("runs", "review")
			anchor = "#runs"
		} else {
			view.CanReviewBatch = true
		}
		view.Items = append(view.Items, dashboard.ProductControlBillingQueueItem{
			TenantID: item.TenantID, Name: item.Name, Unresolved: item.Unresolved,
			PlanFee: ownerMoney(item.Currency, item.PlanFee), RunCharges: ownerMoney(item.Currency, item.RunCharges),
			URL: ownerURL(c, item.TenantID, period, params) + anchor,
		})
	}
	if page.NextCursor != "" {
		params := url.Values{"billing_month": {period}, "billing_cursor": {page.NextCursor}}
		view.NextURL = c.data.BaseURL + "?" + params.Encode() + "#month-end"
	}
	c.data.BillingQueue = view
	return s.ownerInvoiceQueue(r, c, period, q.Get("invoice_cursor"))
}
