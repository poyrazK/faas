package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

type ProductWorkspaceActivityScope struct{ ProductPlanScope }

type ProductWorkspaceActivityStore interface {
	ListProductWorkspaceActivity(context.Context, ProductWorkspaceActivityScope, string, string) (api.ProductWorkspaceActivityPage, error)
}

func workspaceActivityCursor(s ProductWorkspaceActivityScope, p api.ProductPermissions, filter, raw string) (productcontrol.WorkspaceActivityCursor, error) {
	if !productcontrol.WorkspaceActivityFilter(filter) {
		return productcontrol.WorkspaceActivityCursor{}, ErrInvalidArgument
	}
	scope := productcontrol.WorkspaceActivityScope(s.AccountID, s.AppID, s.TenantID, s.AuthAppID, s.OrganizationID, s.Role, filter, p)
	c, err := productcontrol.DecodeWorkspaceActivityCursor(raw, scope)
	if err != nil {
		return c, ErrInvalidArgument
	}
	return c, nil
}

func workspaceActivityEvent(kind, id string, at time.Time) api.ProductWorkspaceActivityEvent {
	return api.ProductWorkspaceActivityEvent{ID: kind + "/" + id, Kind: kind, ReferenceID: id, OccurredAt: at.UTC().Truncate(time.Microsecond)}
}

type workspaceActivityCollector struct {
	cursor productcontrol.WorkspaceActivityCursor
	filter string
	events []api.ProductWorkspaceActivityEvent
}

func (c *workspaceActivityCollector) add(e api.ProductWorkspaceActivityEvent) {
	if c.filter != "all" && productcontrol.WorkspaceActivityCategory(e.Kind) != c.filter {
		return
	}
	boundary := api.ProductWorkspaceActivityEvent{OccurredAt: c.cursor.At, Kind: c.cursor.Kind, ReferenceID: c.cursor.ID}
	if c.cursor.ID != "" && !productcontrol.WorkspaceActivityBefore(boundary, e) {
		return
	}
	i := sort.Search(len(c.events), func(i int) bool { return productcontrol.WorkspaceActivityBefore(e, c.events[i]) })
	if i > api.ProductControlWorkspaceActivityPageSize {
		return
	}
	c.events = append(c.events, api.ProductWorkspaceActivityEvent{})
	copy(c.events[i+1:], c.events[i:])
	c.events[i] = e
	if len(c.events) > api.ProductControlWorkspaceActivityPageSize+1 {
		c.events = c.events[:api.ProductControlWorkspaceActivityPageSize+1]
	}
}

func workspaceActivityPage(s ProductWorkspaceActivityScope, now time.Time, filter string, c productcontrol.WorkspaceActivityCursor, events []api.ProductWorkspaceActivityEvent) api.ProductWorkspaceActivityPage {
	p := api.ProductWorkspaceActivityPage{AccountID: s.AccountID, AppID: s.AppID, TenantID: s.TenantID, Filter: filter, ObservedAt: now.UTC(), HistoryScope: "retained_product_control_records", Events: events}
	if p.Events == nil {
		p.Events = []api.ProductWorkspaceActivityEvent{}
	}
	if len(p.Events) > api.ProductControlWorkspaceActivityPageSize {
		p.Events = p.Events[:api.ProductControlWorkspaceActivityPageSize]
		p.NextCursor = productcontrol.EncodeWorkspaceActivityCursor(c.Scope, p.Events[len(p.Events)-1])
	}
	return p
}
