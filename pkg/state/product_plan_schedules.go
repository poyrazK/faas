package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	productPlanPending     = "pending"
	productPlanApplied     = "applied"
	productPlanCancelled   = "cancelled"
	productPlanNeedsReview = "needs_review"
)

type ProductPlanScheduleStore interface {
	PreviewScheduledProductCustomerChange(context.Context, string, string, api.ProductCustomer) (api.ProductCustomerChangePreview, error)
	GetProductPlanSchedule(context.Context, ProductBillingScope) (api.ProductPlanSchedule, error)
	CancelProductPlanSchedule(context.Context, ProductBillingScope, string) (api.ProductPlanSchedule, error)
	ResolveProductPlanSchedules(context.Context, time.Time) error
}

func scheduleProductPreview(p *api.ProductCustomerChangePreview) error {
	if p.Before == nil || p.Before.Suspended != p.After.Suspended ||
		(p.Before.Plan == p.After.Plan && p.Before.Revision == p.After.Revision) {
		return ErrInvalidArgument
	}
	_, effective := productMonthBounds(p.Period)
	p.StartsNextMonthAt = &effective
	return nil
}

func sameProductAssignment(a, b api.ProductCustomer) bool {
	return a.Plan == b.Plan && a.Revision == b.Revision && a.Suspended == b.Suspended && a.AuthOrganizationID == b.AuthOrganizationID
}

func pendingProductTrialFallback(customer api.ProductCustomer, plan api.ProductPlan, effective time.Time) bool {
	return plan.RequiresPaymentMethod && customer.TrialResolvedAt == nil && customer.PaymentMethodAt == nil &&
		customer.TrialEndsAt != nil && !customer.TrialEndsAt.After(effective)
}
