package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) ListProductWorkspaceActivity(ctx context.Context, s ProductWorkspaceActivityScope, filter, raw string) (api.ProductWorkspaceActivityPage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.ProductWorkspaceActivityPage{}, err
	}
	v, err := m.helpViewLocked(ProductHelpScope(s))
	if err != nil {
		return api.ProductWorkspaceActivityPage{}, err
	}
	cursor, err := workspaceActivityCursor(s, v.permissions, filter, raw)
	if err != nil {
		return api.ProductWorkspaceActivityPage{}, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	c := workspaceActivityCollector{cursor: cursor, filter: filter}
	d := m.productLocked()
	// Work/help share the existing customer-safe notification source rules, but
	// the activity timeline has no notification window or read-state mutations.
	for id := range d.receipts {
		if source, ok := m.notificationActivitySourceLocked(ProductNotificationScope(s), v, "work/"+id, now); ok {
			e := workspaceActivityEvent(source.value.Kind, id, source.value.OccurredAt)
			e.Feature = source.value.Feature
			c.add(e)
		}
	}
	for id := range d.help {
		if source, ok := m.notificationActivitySourceLocked(ProductNotificationScope(s), v, "help/"+id, now); ok {
			e := workspaceActivityEvent(source.value.Kind, id, source.value.OccurredAt)
			e.HelpKind = source.value.HelpKind
			c.add(e)
		}
	}
	if v.permissions.ManagePlan {
		for id, row := range d.planChanges {
			p := row.receipt.Preview
			if row.account == s.AccountID && row.app == s.AppID && row.tenant == s.TenantID && p.BeforeCustomer.AuthOrganizationID == s.OrganizationID && p.AfterCustomer.AuthOrganizationID == s.OrganizationID {
				e := workspaceActivityEvent("plan_changed", id, row.receipt.EffectiveAt)
				e.Plan = &api.ProductWorkspaceActivityPlan{Before: p.BeforeCustomer.Plan, After: p.AfterCustomer.Plan}
				c.add(e)
			}
		}
	}
	if v.permissions.ViewBilling {
		for id, row := range d.documents {
			doc := row.document
			if row.account == s.AccountID && row.app == s.AppID && doc.TenantID == s.TenantID {
				kind := "statement_finalized"
				if doc.Kind == "credit_note" {
					kind = "credit_recorded"
				}
				e := workspaceActivityEvent(kind, id, doc.CreatedAt)
				e.Billing = &api.ProductWorkspaceActivityBill{Period: doc.Period, Currency: doc.Currency, AmountMillicents: doc.AmountMillicents}
				c.add(e)
			}
		}
	}
	return workspaceActivityPage(s, now, filter, cursor, c.events), ctx.Err()
}
