package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) ListProductCustomerActivity(ctx context.Context, account, app, tenant, raw string) (api.ProductCustomerActivityPage, error) {
	cursor, err := productActivityRequest(account, app, tenant, raw)
	if err != nil {
		return api.ProductCustomerActivityPage{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.ProductCustomerActivityPage{}, err
	}
	if _, err := m.onboardingOwnerLocked(account, app); err != nil {
		return api.ProductCustomerActivityPage{}, err
	}
	t, ok := m.platformTenants[tenant]
	if !ok || t.AccountID != account {
		return api.ProductCustomerActivityPage{}, ErrNotFound
	}
	d := m.productLocked()
	if _, ok := d.customers[app+"/"+tenant]; !ok {
		return api.ProductCustomerActivityPage{}, ErrNotFound
	}
	observed := time.Now().UTC()
	c := productActivityCollector{cursor: cursor}
	m.productActivityOnboardingsLocked(&c, account, app, tenant)
	for _, row := range d.changes {
		if row.account == account && row.app == app && row.receipt.After.TenantID == tenant {
			e := productActivityEvent("customer_assignment_changed", row.receipt.ID, row.receipt.EffectiveAt)
			e.Assignment = &api.ProductActivityAssignment{Before: row.receipt.Before, After: row.receipt.After}
			c.add(e)
		}
	}
	for _, row := range d.planChanges {
		if row.account == account && row.app == app && row.tenant == tenant {
			e := productActivityEvent("customer_plan_changed", row.receipt.ID, row.receipt.EffectiveAt)
			before := row.receipt.Preview.BeforeCustomer
			e.Assignment = &api.ProductActivityAssignment{Before: &before, After: row.receipt.Preview.AfterCustomer, ActorRole: row.receipt.ActorRole}
			c.add(e)
		}
	}
	for id, row := range d.receipts {
		if row.account == account && row.app == app && row.tenant == tenant {
			e := productActivityEvent("work_admitted", id, row.receipt.AdmittedAt)
			terms := row.receipt
			e.Terms = &terms
			c.add(e)
			if row.resolvedAt != nil && row.resolvedState != "" {
				e = productActivityEvent("work_resolved", id, *row.resolvedAt)
				e.Terms, e.Outcome = &terms, row.resolvedState
				c.add(e)
			}
			if row.waivedAt != nil {
				e = productActivityEvent("charge_waived", id, *row.waivedAt)
				e.Terms, e.Reason = &terms, row.waiverReason
				c.add(e)
			}
		}
	}
	for id, row := range d.documents {
		if row.account == account && row.app == app && row.document.TenantID == tenant {
			doc := row.document
			doc.Snapshot = nil
			e := productActivityEvent("billing_document_created", id, doc.CreatedAt)
			e.Document = &doc
			c.add(e)
			if h, ok := d.handoffs[id]; ok {
				e = productActivityEvent("invoice_handoff_recorded", id, h.CreatedAt)
				e.Handoff, e.Period = &h, doc.Period
				c.add(e)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return api.ProductCustomerActivityPage{}, err
	}
	return cloneProductBilling(productActivityPage(account, app, tenant, observed, c.events)), nil
}

func (m *MemStore) productActivityOnboardingsLocked(c *productActivityCollector, account, app, tenant string) {
	d := m.productLocked()
	for id, row := range d.onboardings {
		if row.account != account || row.app != app || row.receipt.Tenant.ID != tenant {
			continue
		}
		e := productActivityEvent("onboarding_completed", id, row.receipt.CreatedAt)
		e.Assignment = &api.ProductActivityAssignment{After: row.receipt.Customer}
		c.add(e)
		if row.access != nil {
			c.add(productActivityEvent("workspace_visit_observed", id, *row.access))
		}
		var first *api.ProductOnboardingSuccess
		for operation, r := range d.receipts {
			if r.account != account || r.app != app || r.tenant != tenant || r.receipt.AdmittedAt.Before(row.receipt.CreatedAt) || r.resolvedState != api.OperationSucceeded || r.resolvedAt == nil {
				continue
			}
			if first == nil || r.resolvedAt.Before(first.VerifiedAt) || (r.resolvedAt.Equal(first.VerifiedAt) && operation < first.OperationID) {
				first = &api.ProductOnboardingSuccess{OperationID: operation, Feature: r.receipt.Feature, VerifiedAt: *r.resolvedAt}
			}
		}
		if first != nil {
			e = productActivityEvent("onboarding_first_success", id, first.VerifiedAt)
			e.Milestone = first
			c.add(e)
		}
	}
}
