package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

type ProductCustomerActivityStore interface {
	ListProductCustomerActivity(context.Context, string, string, string, string) (api.ProductCustomerActivityPage, error)
}

func productActivityRequest(account, app, tenant, raw string) (productcontrol.ActivityCursor, error) {
	if err := validateProductBillingScope(ProductBillingScope{AccountID: account, AppID: app, TenantID: tenant, Period: productcontrol.Period(time.Now())}); err != nil {
		return productcontrol.ActivityCursor{}, err
	}
	c, err := productcontrol.DecodeActivityCursor(raw, productcontrol.ActivityScope(account, app, tenant))
	if err != nil {
		return c, ErrInvalidArgument
	}
	return c, nil
}

func productActivityEvent(kind, id string, at time.Time) api.ProductCustomerActivityEvent {
	return api.ProductCustomerActivityEvent{ID: kind + ":" + id, Kind: kind, SourceID: id, OccurredAt: at.UTC()}
}

// Keep at most one page and a lookahead, even when the memory store contains
// years of history. PostgreSQL applies the same limit before returning rows.
type productActivityCollector struct {
	cursor productcontrol.ActivityCursor
	events []api.ProductCustomerActivityEvent
}

func (c *productActivityCollector) add(e api.ProductCustomerActivityEvent) {
	if c.cursor.ID != "" && !productcontrol.ActivityBefore(api.ProductCustomerActivityEvent{OccurredAt: c.cursor.At, Kind: c.cursor.Kind, SourceID: c.cursor.ID}, e) {
		return
	}
	i := sort.Search(len(c.events), func(i int) bool { return productcontrol.ActivityBefore(e, c.events[i]) })
	if i > api.ProductControlActivityPageSize {
		return
	}
	c.events = append(c.events, api.ProductCustomerActivityEvent{})
	copy(c.events[i+1:], c.events[i:])
	c.events[i] = e
	if len(c.events) > api.ProductControlActivityPageSize+1 {
		c.events = c.events[:api.ProductControlActivityPageSize+1]
	}
}

func productActivityPage(account, app, tenant string, observed time.Time, events []api.ProductCustomerActivityEvent) api.ProductCustomerActivityPage {
	p := api.ProductCustomerActivityPage{AccountID: account, AppID: app, TenantID: tenant, ObservedAt: observed.UTC(), HistoryScope: "retained_product_control_records", Events: events}
	if p.Events == nil {
		p.Events = []api.ProductCustomerActivityEvent{}
	}
	if len(p.Events) > api.ProductControlActivityPageSize {
		p.Events = p.Events[:api.ProductControlActivityPageSize]
		p.NextCursor = productcontrol.EncodeActivityCursor(productcontrol.ActivityScope(account, app, tenant), p.Events[len(p.Events)-1])
	}
	return p
}
