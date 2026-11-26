package state

import (
	"context"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) invalidateProductPlanScheduleLocked(key string) {
	d := m.productLocked()
	id := d.schedules[key]
	change := d.changes[id]
	if change.scheduleStatus == productPlanPending {
		change.scheduleStatus = productPlanNeedsReview
		d.changes[id] = change
	}
}

func (m *MemStore) applyDueProductPlanLocked(account, app, tenant string, now time.Time) {
	d := m.productLocked()
	key := app + "/" + tenant
	id := d.schedules[key]
	change := d.changes[id]
	if change.scheduleStatus != productPlanPending || change.account != account || change.receipt.EffectiveAt.After(now) {
		return
	}
	current, exists := d.customers[key]
	target := change.receipt.After
	_, targetExists := d.revisions[app+"/"+target.Revision].Spec.Plans[target.Plan]
	if change.receipt.Before == nil || !exists || !targetExists || m.apps[app].Status == AppDeleted || !sameProductAssignment(current, *change.receipt.Before) {
		m.invalidateProductPlanScheduleLocked(key)
		return
	}
	if plan := d.revisions[app+"/"+current.Revision].Spec.Plans[current.Plan]; pendingProductTrialFallback(current, plan, change.receipt.EffectiveAt) {
		entries := d.planHistory[key]
		if len(entries) == 0 || !entries[len(entries)-1].at.After(*current.TrialEndsAt) {
			m.invalidateProductPlanScheduleLocked(key)
			return
		}
	}
	change.scheduleStatus = productPlanApplied
	d.changes[id] = change
	m.assignProductCustomerAtLocked(account, app, target, change.receipt.EffectiveAt)
}

func (m *MemStore) ResolveProductPlanSchedules(_ context.Context, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resolveProductPlanSchedulesLocked(now)
	return nil
}

func (m *MemStore) resolveProductPlanSchedulesLocked(now time.Time) {
	d := m.productLocked()
	for key, id := range d.schedules {
		change := d.changes[id]
		app, tenant, _ := strings.Cut(key, "/")
		m.applyDueProductPlanLocked(change.account, app, tenant, now)
	}
}

func (m *MemStore) GetProductPlanSchedule(_ context.Context, scope ProductBillingScope) (api.ProductPlanSchedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.productBillingScopeLocked(scope); err != nil {
		return api.ProductPlanSchedule{}, err
	}
	m.applyDueProductPlanLocked(scope.AccountID, scope.AppID, scope.TenantID, time.Now())
	d := m.productLocked()
	change := d.changes[d.schedules[scope.AppID+"/"+scope.TenantID]]
	if change.scheduleStatus == "" {
		return api.ProductPlanSchedule{}, ErrNotFound
	}
	return cloneProductBilling(api.ProductPlanSchedule{ProductCustomerChangeReceipt: change.receipt, Status: change.scheduleStatus}), nil
}

func (m *MemStore) CancelProductPlanSchedule(_ context.Context, scope ProductBillingScope, id string) (api.ProductPlanSchedule, error) {
	if !validProductUUID(id) {
		return api.ProductPlanSchedule{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.productBillingScopeLocked(scope); err != nil {
		return api.ProductPlanSchedule{}, err
	}
	m.applyDueProductPlanLocked(scope.AccountID, scope.AppID, scope.TenantID, time.Now())
	d := m.productLocked()
	change := d.changes[id]
	if change.account != scope.AccountID || change.app != scope.AppID || change.receipt.After.TenantID != scope.TenantID || change.scheduleStatus == "" {
		return api.ProductPlanSchedule{}, ErrNotFound
	}
	switch change.scheduleStatus {
	case productPlanPending:
		change.scheduleStatus = productPlanCancelled
		d.changes[id] = change
	case productPlanCancelled:
	default:
		return api.ProductPlanSchedule{}, ErrConflict
	}
	return cloneProductBilling(api.ProductPlanSchedule{ProductCustomerChangeReceipt: change.receipt, Status: change.scheduleStatus}), nil
}
