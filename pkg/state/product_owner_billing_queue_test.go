package state

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

type productOwnerBillingQueueTestStore interface {
	productPlanScheduleTestStore
	ProductOwnerBillingQueueStore
	ProductOwnerInvoiceQueueStore
	ProductOwnerPaymentQueueStore
	ProductOwnerOutstandingBalancesStore
	ProductStripeInvoiceSendStore
	StatementInvoiceExportStore
	StatementInvoicePaymentStore
}

type productOwnerBillingQueueFixture struct {
	assignment func(ProductBillingScope, time.Time)
	schedule   func(api.ProductCustomerChangeReceipt, time.Time)
	trial      func(ProductBillingScope, time.Time, time.Time)
	receipt    func(Operation)
}

func TestProductOwnerBillingQueueMemory(t *testing.T) {
	s := NewMemStore()
	productOwnerBillingQueueContract(t, s, productOwnerBillingQueueFixture{
		assignment: func(scope ProductBillingScope, at time.Time) {
			key := scope.AppID + "/" + scope.TenantID
			s.productLocked().planHistory[key][0].at = at
		},
		schedule: func(receipt api.ProductCustomerChangeReceipt, at time.Time) {
			change := s.productLocked().changes[receipt.ID]
			change.receipt.EffectiveAt = at
			s.productLocked().changes[receipt.ID] = change
		},
		trial: func(scope ProductBillingScope, start, end time.Time) {
			s.SeedProductTrialForTest(scope.AppID, scope.TenantID, start, end)
		},
		receipt: s.SeedProductOperationForTest,
	})
}

func productOwnerBillingQueueContract(t *testing.T, s productOwnerBillingQueueTestStore, fixtures productOwnerBillingQueueFixture) {
	t.Helper()
	ctx := t.Context()
	f := newProductPlanFixture(ctx, t, s)
	account, app := f.scope.AccountID, f.scope.AppID
	pro := f.spec.Plans["pro"]
	pro.MonthlyFeeMillicents = 900_000
	f.spec.Plans["pro"] = pro
	catalog, err := s.PutProductCatalog(ctx, account, app, f.spec)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	end := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	start := end.AddDate(0, -1, 0)
	scope := ProductBillingScope{AccountID: account, AppID: app, TenantID: f.scope.TenantID, Period: productcontrol.Period(start)}

	// Set up the reviewed schedule with the public store methods, then
	// simulate an already closed billing month without sleeping or running work.
	target := f.customer
	target.Plan, target.Revision = "pro", catalog.Revision
	_, req := reviewProductPlanSchedule(t, s, f.scope, target)
	change, err := s.ApplyProductCustomerChange(ctx, account, app, req)
	if err != nil {
		t.Fatal(err)
	}
	fixtures.assignment(scope, start.Add(-time.Hour))
	fixtures.schedule(change, start)

	newPaidWorkspace := func(name string, assignedAt time.Time) ProductBillingScope {
		t.Helper()
		tenant, _, err := s.CreatePlatformTenant(ctx, account, name, name, 100)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.SetProductCustomer(ctx, account, app, api.ProductCustomer{TenantID: tenant.ID, Plan: "pro", Revision: catalog.Revision}); err != nil {
			t.Fatal(err)
		}
		other := scope
		other.TenantID = tenant.ID
		fixtures.assignment(other, assignedAt)
		return other
	}

	trial := newPaidWorkspace("full-trial", start.Add(-time.Hour))
	fixtures.trial(trial, start.Add(-time.Hour), end.Add(time.Hour))
	mixed := newPaidWorkspace("charged-runs", start.Add(24*time.Hour))
	for _, state := range []api.OperationState{api.OperationSucceeded, api.OperationAccepted} {
		at := start.Add(48 * time.Hour)
		fixtures.receipt(Operation{AccountID: account, AppID: app, PlatformTenantID: mixed.TenantID,
			OperationResponse: api.OperationResponse{ID: uuid.NewString(), State: state, UpdatedAt: at},
			ProductControl: &api.ProductOperationReceipt{Plan: "pro", Feature: "export", Revision: catalog.Revision, Currency: "EUR",
				Period: scope.Period, AdmittedAt: at, UnitPriceMillicents: 10_000},
		})
	}

	list := func() map[string]ProductOwnerBillingQueueItem {
		t.Helper()
		page, err := s.ListProductOwnerBillingQueue(ctx, account, app, scope.Period, "")
		if err != nil || page.NextCursor != "" {
			t.Fatalf("list returned %+v, %v", page, err)
		}
		items := map[string]ProductOwnerBillingQueueItem{}
		for _, item := range page.Items {
			if _, duplicate := items[item.TenantID]; duplicate {
				t.Fatalf("duplicate queue row for %s", item.TenantID)
			}
			items[item.TenantID] = item
		}
		return items
	}

	items := list()
	charge, usage := items[scope.TenantID], items[mixed.TenantID]
	mixedReview, err := s.ReviewProductBilling(ctx, mixed)
	if err != nil || len(mixedReview.Fees) != 1 || mixedReview.Fees[0].ProratedFrom == nil {
		t.Fatalf("mixed review = %+v, %v", mixedReview, err)
	}
	if len(items) != 2 || charge.PlanFee != 900_000 || charge.RunCharges != 0 || charge.Unresolved != 0 ||
		usage.PlanFee != mixedReview.Fees[0].AmountMillicents || usage.RunCharges != 10_000 || usage.Unresolved != 1 {
		t.Fatalf("incorrect queue: %+v", items)
	}
	assertProductScheduleStatus(t, s, scope, change.ID, productPlanApplied)
	review, err := s.ReviewProductBilling(ctx, scope)
	if err != nil || !review.Closed || review.Starts != 0 || review.AmountMillicents != charge.PlanFee {
		t.Fatalf("fee-only review = %+v, %v", review, err)
	}
	statement, err := s.FinalizeProductBilling(ctx, scope, review.ReviewHash)
	if err != nil || statement.AmountMillicents != charge.PlanFee {
		t.Fatalf("fee-only statement = %+v, %v", statement, err)
	}
	retry, err := s.FinalizeProductBilling(ctx, scope, review.ReviewHash)
	if err != nil || retry.ID != statement.ID {
		t.Fatalf("retry = %+v, %v", retry, err)
	}
	if pending := list(); len(pending) != 1 || pending[mixed.TenantID].Unresolved != 1 {
		t.Fatalf("queue after finalization = %+v", pending)
	}
	if _, err := s.ListProductOwnerBillingQueue(ctx, account, app, productcontrol.Period(now), ""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("open month returned %v", err)
	}
	if _, err := s.ListProductOwnerBillingQueue(ctx, uuid.NewString(), app, scope.Period, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign account returned %v", err)
	}

	testProductOwnerInvoiceQueue(t, s, scope, statement, trial, func() ProductBillingScope {
		return newPaidWorkspace("external-invoice", start.Add(-time.Hour))
	})

	t.Run("outstanding balances", func(t *testing.T) {
		testProductOwnerOutstandingBalances(t, s, scope, func(name string, monthOffset int) ProductBillingScope {
			month := start.AddDate(0, monthOffset, 0)
			workspace := newPaidWorkspace(name, month.Add(-time.Hour))
			workspace.Period = productcontrol.Period(month)
			return workspace
		})
	})
}

func TestProductOwnerBillingQueueCursorAdvancesPastFilteredCandidates(t *testing.T) {
	now := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	const period = "2026-10"
	account, app := uuid.NewString(), uuid.NewString()
	cursor, err := productOwnerBillingQueueCursor(account, app, period, "", now)
	if err != nil {
		t.Fatal(err)
	}
	candidates := make([]productOwnerBillingCandidate, productOwnerBillingQueuePageSize+1)
	for i := range candidates {
		candidates[i] = productOwnerBillingCandidate{tenantID: uuid.NewString()}
	}
	calls := 0
	page, err := productOwnerBillingQueue(account, app, cursor, candidates, func(ProductBillingScope) (api.ProductBillingReview, error) {
		calls++
		return api.ProductBillingReview{Closed: true}, nil
	})
	if err != nil || len(page.Items) != 0 || calls != productOwnerBillingQueuePageSize || page.NextCursor == "" {
		t.Fatalf("filtered page = %+v after %d reviews, %v", page, calls, err)
	}
	next, err := productOwnerBillingQueueCursor(account, app, period, page.NextCursor, now)
	if err != nil || next.ID != candidates[productOwnerBillingQueuePageSize-1].tenantID {
		t.Fatalf("next cursor = %+v, %v", next, err)
	}
	if _, err := productOwnerBillingQueueCursor(account, app, "2026-09", page.NextCursor, now); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("cursor accepted in a different month: %v", err)
	}
}
