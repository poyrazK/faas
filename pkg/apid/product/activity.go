package product

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) CustomerActivity(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, app, ok := s.owned(w, r, acct)
	if !ok {
		return
	}
	q, err := ownerQuery(r, "tenant", "cursor")
	var page api.ProductCustomerActivityPage
	if err == nil {
		page, err = s.customerActivity(r.Context(), acct, app, q.Get("tenant"), q.Get("cursor"))
	}
	respond(w, page, err)
}

func (s *Service) OwnerCustomerActivity(w http.ResponseWriter, r *http.Request, acct state.Account) {
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	q, err := ownerQuery(r, "tenant", "cursor")
	var page api.ProductCustomerActivityPage
	if err == nil {
		page, err = s.customerActivity(r.Context(), acct, c.app, q.Get("tenant"), q.Get("cursor"))
	}
	if err != nil {
		s.ownerError(w, r, c, err)
		return
	}
	c.data.TenantID, c.data.Period = page.TenantID, productcontrol.Period(time.Now())
	c.data.Activity = customerActivityView(c, page)
	s.OwnerRender(w, r, acct, c.data)
}

func (s *Service) customerActivity(parent context.Context, acct state.Account, app state.App, tenant, cursor string) (api.ProductCustomerActivityPage, error) {
	var page api.ProductCustomerActivityPage
	if !ownerUUID(tenant) {
		return page, state.ErrInvalidArgument
	}
	store, ok := s.Host.Store().(state.ProductCustomerActivityStore)
	if !ok {
		return page, state.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(parent, api.ProductControlActivityMaxDuration)
	defer cancel()
	page, err := store.ListProductCustomerActivity(ctx, acct.ID, app.ID, tenant, cursor)
	if err != nil {
		return page, err
	}
	if page.AccountID != acct.ID || page.AppID != app.ID || page.TenantID != tenant {
		return page, state.ErrConflict
	}
	if err = productcontrol.ValidateActivityPage(page, cursor); err != nil {
		return page, err
	}
	for i := range page.Events {
		page.Events[i].ReviewURL = productcontrol.ActivityReviewURL(app.Slug, tenant, page.Events[i])
	}
	raw, err := json.Marshal(page)
	if err == nil && len(raw)+1 > api.ProductControlActivityMaxResponseBytes {
		err = state.NewOperationLimitError("product_activity_response_bytes", api.ProductControlActivityMaxResponseBytes, int64(len(raw)+1))
	}
	if err == nil {
		err = ctx.Err()
	}
	return page, err
}

func customerActivityView(c *ownerContext, page api.ProductCustomerActivityPage) *dashboard.ProductControlActivity {
	v := &dashboard.ProductControlActivity{Report: page}
	for _, e := range page.Events {
		raw, _ := json.MarshalIndent(e, "", "  ")
		v.Events = append(v.Events, dashboard.ProductControlActivityEvent{ProductCustomerActivityEvent: e, Label: productcontrol.ActivityKindLabel(e.Kind), Summary: productcontrol.ActivitySummary(e), SourceJSON: string(raw)})
	}
	if page.NextCursor != "" {
		v.NextURL = c.data.BaseURL + "/activity?" + url.Values{"tenant": {page.TenantID}, "cursor": {page.NextCursor}}.Encode()
	}
	return v
}
