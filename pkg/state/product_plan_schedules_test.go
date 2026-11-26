package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

type productPlanScheduleTestStore interface {
	productPlanTestStore
	ProductPlanScheduleStore
	OperationDefinitionByID(context.Context, string, string) (OperationDefinition, error)
}

type prepareScheduledProductAdmission func(*testing.T, *Operation, OperationDefinition, OperationAdmission, time.Time) error

func TestProductPlanSchedulesMemory(t *testing.T) {
	s := NewMemStore()
	productPlanSchedulesContract(t, s, func(_ *testing.T, op *Operation, def OperationDefinition, adm OperationAdmission, now time.Time) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.prepareProductOperationLocked(op, def, adm, now)
	})
}

func productPlanSchedulesContract(t *testing.T, s productPlanScheduleTestStore, prepare prepareScheduledProductAdmission) {
	t.Helper()
	t.Run("reviewed revision activates at first admission and bills at the boundary", func(t *testing.T) {
		ctx := t.Context()
		f := newProductPlanFixture(ctx, t, s)
		account, app, tenant := f.scope.AccountID, f.scope.AppID, f.scope.TenantID
		pro := f.spec.Plans["pro"]
		pro.MonthlyFeeMillicents = 4_900_000
		f.spec.Plans["pro"] = pro
		catalog, err := s.PutProductCatalog(ctx, account, app, f.spec)
		if err != nil {
			t.Fatal(err)
		}
		before := f.customer
		before.Revision = catalog.Revision
		if _, err = s.SetProductCustomer(ctx, account, app, before); err != nil {
			t.Fatal(err)
		}
		target := before
		target.Plan = "pro"
		p, req := reviewProductPlanSchedule(t, s, f.scope, target)
		boundary := *p.StartsNextMonthAt
		if boundary.Day() != 1 || boundary.Hour() != 0 || boundary.Minute() != 0 || boundary.Second() != 0 ||
			p.Pricing.ThisMonthBeforeMillicents != 0 || p.Pricing.ThisMonthAfterMillicents != 0 ||
			p.Pricing.NextMonthAfterMillicents != pro.MonthlyFeeMillicents {
			t.Fatalf("scheduled terms = %+v at %v", p.Pricing, boundary)
		}
		r, err := s.ApplyProductCustomerChange(ctx, account, app, req)
		if err != nil || !r.EffectiveAt.Equal(boundary) {
			t.Fatalf("schedule receipt = %+v, %v", r, err)
		}
		if retry, err := s.ApplyProductCustomerChange(ctx, account, app, req); err != nil || retry.ID != r.ID {
			t.Fatalf("schedule retry = %+v, %v", retry, err)
		}
		req.ID = uuid.NewString()
		if _, err := s.ApplyProductCustomerChange(ctx, account, app, req); !errors.Is(err, ErrConflict) {
			t.Fatalf("second pending schedule = %v, want conflict", err)
		}
		scope := ProductBillingScope{account, app, tenant, p.Period}
		assertProductScheduleStatus(t, s, scope, r.ID, productPlanPending)
		// Publishing a later revision must preserve the explicitly reviewed target.
		pro.Label = "New Pro"
		f.spec.Plans["pro"] = pro
		if _, err := s.PutProductCatalog(ctx, account, app, f.spec); err != nil {
			t.Fatal(err)
		}
		def, err := s.OperationDefinitionByID(ctx, account, f.admission.DefinitionID)
		if err != nil {
			t.Fatal(err)
		}
		op := Operation{AccountID: account, AppID: app, PlatformTenantID: tenant, DefinitionID: def.ID}
		beforeOp, afterOp := op, op
		if err := prepare(t, &beforeOp, def, f.admission, boundary.Add(-time.Microsecond)); err != nil {
			t.Fatal(err)
		}
		if err := prepare(t, &afterOp, def, f.admission, boundary); err != nil {
			t.Fatal(err)
		}
		if beforeOp.ProductControl.Plan != "starter" || beforeOp.ProductControl.UnitPriceMillicents != 25_000 {
			t.Fatalf("last admission before boundary = %+v", beforeOp.ProductControl)
		}
		if afterOp.ProductControl.Plan != "pro" || afterOp.ProductControl.Revision != catalog.Revision ||
			afterOp.ProductControl.UnitPriceMillicents != 10_000 || afterOp.ProductControl.Period != productcontrol.Period(boundary) {
			t.Fatalf("first admission at boundary = %+v", afterOp.ProductControl)
		}
		assertProductScheduleStatus(t, s, scope, r.ID, productPlanApplied)
		scope.Period = p.Pricing.NextPeriod
		bill, err := s.ReviewProductBilling(ctx, scope)
		if err != nil || len(bill.Fees) != 1 || bill.Fees[0].AmountMillicents != pro.MonthlyFeeMillicents {
			t.Fatalf("first month's scheduled fee = %+v, %v", bill.Fees, err)
		}
		if err = s.ResolveProductPlanSchedules(ctx, boundary.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		assertProductScheduleStatus(t, s, scope, r.ID, productPlanApplied)
	})
	t.Run("cancellation and an intervening assignment prevent activation", func(t *testing.T) {
		ctx := t.Context()
		f := newProductPlanFixture(ctx, t, s)
		account, app, tenant := f.scope.AccountID, f.scope.AppID, f.scope.TenantID
		target := f.customer
		target.Plan = "pro"
		p, req := reviewProductPlanSchedule(t, s, f.scope, target)
		first, err := s.ApplyProductCustomerChange(ctx, account, app, req)
		if err != nil {
			t.Fatal(err)
		}
		scope := ProductBillingScope{account, app, tenant, p.Period}
		for i := 0; i < 2; i++ {
			got, err := s.CancelProductPlanSchedule(ctx, scope, first.ID)
			if err != nil || got.Status != productPlanCancelled {
				t.Fatalf("cancel attempt %d = %+v, %v", i, got, err)
			}
		}
		assertProductScheduleStatus(t, s, scope, first.ID, productPlanCancelled)
		if err := s.ResolveProductPlanSchedules(ctx, first.EffectiveAt); err != nil {
			t.Fatal(err)
		}
		customer, err := s.GetProductCustomer(ctx, account, app, tenant)
		if err != nil || customer.Plan != "starter" {
			t.Fatalf("customer after cancelled schedule = %+v, %v", customer, err)
		}
		_, req = reviewProductPlanSchedule(t, s, f.scope, target)
		second, err := s.ApplyProductCustomerChange(ctx, account, app, req)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range []api.ProductCustomer{target, f.customer} {
			if _, err := s.SetProductCustomer(ctx, account, app, c); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.ResolveProductPlanSchedules(ctx, second.EffectiveAt); err != nil {
			t.Fatal(err)
		}
		assertProductScheduleStatus(t, s, scope, second.ID, productPlanNeedsReview)
		customer, err = s.GetProductCustomer(ctx, account, app, tenant)
		if err != nil || customer.Plan != "starter" {
			t.Fatalf("customer after stale schedule = %+v, %v", customer, err)
		}
	})
}

func reviewProductPlanSchedule(t *testing.T, s ProductPlanScheduleStore, scope ProductPlanScope, target api.ProductCustomer) (api.ProductCustomerChangePreview, api.ProductCustomerChangeRequest) {
	t.Helper()
	p, err := s.PreviewScheduledProductCustomerChange(t.Context(), scope.AccountID, scope.AppID, target)
	if err != nil || p.StartsNextMonthAt == nil || p.Pricing == nil {
		t.Fatalf("preview = %+v, %v", p, err)
	}
	return p, api.ProductCustomerChangeRequest{ID: uuid.NewString(), ExpectedHash: p.Hash, Customer: p.After, NextMonth: true}
}

func assertProductScheduleStatus(t *testing.T, s ProductPlanScheduleStore, scope ProductBillingScope, id, status string) {
	t.Helper()
	got, err := s.GetProductPlanSchedule(t.Context(), scope)
	if err != nil || got.ID != id || got.Status != status {
		t.Fatalf("schedule = %+v, %v, want %s %s", got, err, id, status)
	}
}
