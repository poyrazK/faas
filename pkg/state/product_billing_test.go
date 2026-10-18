package state

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type productBillingTestStore interface {
	productContractStore
	ProductBillingStore
}

type productBillingFixture struct {
	scope        ProductBillingScope
	other, third ProductBillingScope
	ops          map[string]Operation
	settle       func(Operation)
}

func seedProductBilling(t *testing.T, s productBillingTestStore, insert, settle func(Operation)) productBillingFixture {
	t.Helper()
	ctx := t.Context()
	account, err := s.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, App{AccountID: account.ID, Slug: "billing-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	spec := api.ProductCatalogSpec{Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}}, Plans: map[string]api.ProductPlan{
		"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}},
		"pro":     {Label: "Pro", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 20, UnitPriceMillicents: 10000}}},
	}}
	catalog, err := s.PutProductCatalog(ctx, account.ID, app.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(time.Now().UTC().Year(), time.Now().UTC().Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	f := productBillingFixture{scope: ProductBillingScope{AccountID: account.ID, AppID: app.ID, Period: start.Format("2006-01")}, ops: map[string]Operation{}, settle: settle}
	for i, name := range []string{"alice", "bob", "carol"} {
		tenant, _, err := s.CreatePlatformTenant(ctx, account.ID, name, name, 100)
		if err != nil {
			t.Fatal(err)
		}
		plan := "starter"
		if i == 1 {
			plan = "pro"
		}
		if _, err := s.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: plan}); err != nil {
			t.Fatal(err)
		}
		scope := f.scope
		scope.TenantID = tenant.ID
		switch i {
		case 0:
			f.scope = scope
		case 1:
			f.other = scope
		case 2:
			f.third = scope
		}
	}
	for _, tc := range []struct {
		name  string
		scope ProductBillingScope
		plan  string
		state api.OperationState
	}{
		{"success", f.scope, "starter", api.OperationSucceeded}, {"discounted", f.scope, "pro", api.OperationSucceeded},
		{"failed", f.scope, "starter", api.OperationFailed}, {"cancelled", f.scope, "starter", api.OperationCancelled},
		{"unknown", f.scope, "starter", api.OperationRequiresReconciliation}, {"expired", f.scope, "starter", api.OperationAccepted},
		{"bob", f.other, "pro", api.OperationSucceeded}, {"carol", f.third, "starter", api.OperationSucceeded},
	} {
		op := Operation{AccountID: account.ID, PlatformTenantID: tc.scope.TenantID, AppID: app.ID, OperationResponse: api.OperationResponse{ID: uuid.NewString(), State: tc.state, UpdatedAt: start.Add(time.Hour)}, ProductControl: &api.ProductOperationReceipt{Revision: catalog.Revision, Feature: "export", Plan: tc.plan, Currency: "EUR", Period: f.scope.Period, UnitPriceMillicents: spec.Plans[tc.plan].Features["export"].UnitPriceMillicents, AdmittedAt: start}}
		insert(op)
		settle(op)
		f.ops[tc.name] = op
	}
	return f
}

func TestProductControlBillingMemory(t *testing.T) {
	s := NewMemStore()
	insert := func(op Operation) { s.mu.Lock(); defer s.mu.Unlock(); s.insertProductOperationLocked(op) }
	settle := func(op Operation) { s.mu.Lock(); defer s.mu.Unlock(); s.settleProductOperationLocked(op) }
	f := seedProductBilling(t, s, insert, settle)
	productBillingContract(t, s, f)
}

func productBillingContract(t *testing.T, s productBillingTestStore, f productBillingFixture) {
	t.Helper()
	ctx := t.Context()
	scope := f.scope
	review, err := s.ReviewProductBilling(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if review.Starts != 6 || review.Unresolved != 2 || review.AmountMillicents != 35000 || !review.Closed || len(review.Lines) != 2 {
		t.Fatalf("receipt review: %+v", review)
	}
	if _, err := s.FinalizeProductBilling(ctx, scope, review.ReviewHash); !errors.Is(err, ErrProductBillingUnresolved) {
		t.Fatalf("closed unresolved month finalized: %v", err)
	}
	if _, err := s.FinalizeProductBilling(ctx, scope, strings.Repeat("0", 64)); !errors.Is(err, ErrProductBillingReviewChanged) {
		t.Fatalf("stale consent: %v", err)
	}
	current := scope
	current.Period = time.Now().UTC().Format("2006-01")
	open, err := s.ReviewProductBilling(ctx, current)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinalizeProductBilling(ctx, current, open.ReviewHash); !errors.Is(err, ErrProductBillingOpenPeriod) {
		t.Fatalf("open month finalized: %v", err)
	}
	for _, tc := range []struct {
		scope  ProductBillingScope
		amount int64
	}{{f.other, 10000}, {f.third, 25000}} {
		got, err := s.ReviewProductBilling(ctx, tc.scope)
		if err != nil || got.AmountMillicents != tc.amount || got.Starts != 1 {
			t.Fatalf("customer isolation: %+v %v", got, err)
		}
	}
	foreign := scope
	foreign.AccountID = uuid.NewString()
	if _, err := s.ReviewProductBilling(ctx, foreign); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign owner: %v", err)
	}
	if _, err := s.WaiveProductReceipt(ctx, f.other, f.ops["unknown"].ID, "operator write-off"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign receipt waiver: %v", err)
	}
	if _, err := s.WaiveProductReceipt(ctx, scope, f.ops["success"].ID, "operator write-off"); !errors.Is(err, ErrConflict) {
		t.Fatalf("verified charge waived: %v", err)
	}
	for _, name := range []string{"unknown", "expired"} {
		id := f.ops[name].ID
		receipt, err := s.WaiveProductReceipt(ctx, scope, id, "internal pilot write-off")
		if err != nil || receipt.WaivedAt == nil {
			t.Fatalf("waiver: %+v %v", receipt, err)
		}
		again, err := s.WaiveProductReceipt(ctx, scope, id, "internal pilot write-off")
		if err != nil || !reflect.DeepEqual(receipt, again) {
			t.Fatalf("waiver retry: %+v %v", again, err)
		}
		if _, err := s.WaiveProductReceipt(ctx, scope, id, "changed reason"); !errors.Is(err, ErrConflict) {
			t.Fatalf("waiver rewritten: %v", err)
		}
	}
	if _, err := s.FinalizeProductBilling(ctx, scope, review.ReviewHash); !errors.Is(err, ErrProductBillingReviewChanged) {
		t.Fatalf("old review finalized changed evidence: %v", err)
	}
	review, err = s.ReviewProductBilling(ctx, scope)
	if err != nil || review.Unresolved != 0 || review.AmountMillicents != 35000 {
		t.Fatalf("resolved review: %+v %v", review, err)
	}
	var wg sync.WaitGroup
	docs := make(chan api.ProductBillingDocument, 8)
	for range 8 {
		wg.Go(func() {
			doc, err := s.FinalizeProductBilling(ctx, scope, review.ReviewHash)
			if err != nil {
				t.Errorf("finalize retry: %v", err)
				return
			}
			docs <- doc
		})
	}
	wg.Wait()
	close(docs)
	var statement api.ProductBillingDocument
	for doc := range docs {
		if statement.ID != "" && statement.ID != doc.ID {
			t.Fatal("concurrent finalization created two statements")
		}
		statement = doc
	}
	if statement.Kind != "statement" || statement.AmountMillicents != 35000 || statement.Snapshot == nil {
		t.Fatalf("statement: %+v", statement)
	}
	// A later verified success retains execution truth, but cannot revive a
	// waived charge or alter the previously reviewed invoice document.
	late := f.ops["unknown"]
	late.State = api.OperationSucceeded
	late.UpdatedAt = time.Now().UTC()
	f.settle(late)
	evidence, err := s.GetProductReceipt(ctx, scope.AccountID, scope.AppID, late.ID)
	if err != nil || evidence.SucceededAt == nil || evidence.WaivedAt == nil {
		t.Fatalf("late verified proof: %+v %v", evidence, err)
	}
	final, err := s.ReviewProductBilling(ctx, scope)
	if err != nil || final.ReviewHash != review.ReviewHash || final.Finalized == nil || final.Finalized.ID != statement.ID {
		t.Fatalf("late outcome rewrote statement: %+v %v", final, err)
	}
	usage, err := s.GetProductUsage(ctx, scope.AccountID, scope.AppID, scope.TenantID, scope.Period)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, line := range usage.Lines {
		total += line.AmountMillicents
	}
	if total != 35000 {
		t.Fatalf("waived success charged in usage: %d", total)
	}
	failed, err := s.GetProductReceipt(ctx, scope.AccountID, scope.AppID, f.ops["failed"].ID)
	if err != nil || failed.ResolvedState != api.OperationFailed || failed.ResolvedAt == nil || failed.Operation != nil {
		t.Fatalf("failure evidence did not survive result expiry: %+v %v", failed, err)
	}
	receipts, err := s.ListProductBillingReceipts(ctx, scope, "")
	if err != nil || len(receipts.Receipts) != 6 {
		t.Fatalf("receipt ledger: %+v %v", receipts, err)
	}
	productBillingCreditsAndHandoffs(t, s, f, statement)
}

func productBillingCreditsAndHandoffs(t *testing.T, s productBillingTestStore, f productBillingFixture, statement api.ProductBillingDocument) {
	t.Helper()
	ctx := t.Context()
	scope := f.scope
	op := f.ops["success"]
	requests := []api.ProductCreditRequest{{ID: uuid.NewString(), TenantID: scope.TenantID, OperationID: op.ID, AmountMillicents: 20000, Reason: "pilot service credit"}, {ID: uuid.NewString(), TenantID: scope.TenantID, OperationID: op.ID, AmountMillicents: 20000, Reason: "pilot service credit"}}
	var wg sync.WaitGroup
	winners := make(chan api.ProductBillingDocument, 2)
	for _, req := range requests {
		wg.Go(func() {
			doc, err := s.CreditProductReceipt(ctx, scope, req)
			if err == nil {
				winners <- doc
			} else if !errors.Is(err, ErrConflict) {
				t.Errorf("credit: %v", err)
			}
		})
	}
	wg.Wait()
	close(winners)
	var credit api.ProductBillingDocument
	count := 0
	for doc := range winners {
		count++
		credit = doc
	}
	if count != 1 || credit.AmountMillicents != -20000 {
		t.Fatalf("concurrent credits over-refunded: %d %+v", count, credit)
	}
	req := requests[0]
	if req.ID != credit.ID {
		req = requests[1]
	}
	again, err := s.CreditProductReceipt(ctx, scope, req)
	if err != nil || again.ID != credit.ID || again.AmountMillicents != credit.AmountMillicents {
		t.Fatalf("credit retry: %+v %v", again, err)
	}
	changed := req
	changed.AmountMillicents = 1
	if _, err := s.CreditProductReceipt(ctx, scope, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("credit identity reused: %v", err)
	}
	changed.ID = uuid.NewString()
	changed.AmountMillicents = 5001
	if _, err := s.CreditProductReceipt(ctx, scope, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("over-credit accepted: %v", err)
	}
	changed.OperationID = f.ops["unknown"].ID
	changed.AmountMillicents = 1
	if _, err := s.CreditProductReceipt(ctx, scope, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("waived operation credited: %v", err)
	}
	if _, err := s.CreditProductReceipt(ctx, f.other, req); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("customer credit confused: %v", err)
	}
	handoff, err := s.HandoffProductBilling(ctx, scope, statement.ID, "pilot-invoice-001")
	if err != nil || handoff.AmountMillicents != 35000 {
		t.Fatalf("invoice handoff: %+v %v", handoff, err)
	}
	retained, err := s.GetProductBillingHandoff(ctx, scope, statement.ID)
	if err != nil || !reflect.DeepEqual(retained, handoff) {
		t.Fatalf("lost-response handoff recovery: %+v %v", retained, err)
	}
	if _, err := s.HandoffProductBilling(ctx, scope, statement.ID, "pilot-invoice-001"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HandoffProductBilling(ctx, scope, statement.ID, "replacement-invoice"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate invoice allowed: %v", err)
	}
	if _, err := s.HandoffProductBilling(ctx, scope, credit.ID, "pilot-invoice-001"); !errors.Is(err, ErrConflict) {
		t.Fatalf("external invoice reference reused: %v", err)
	}
	if _, err := s.HandoffProductBilling(ctx, scope, credit.ID, "pilot-credit-001"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetProductBillingHandoff(ctx, f.other, statement.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign handoff disclosed: %v", err)
	}
	// A large credit ledger stays bounded and exposes a cursor; later pages
	// must be fetched explicitly, without silently dropping adjustments.
	for i := range api.ProductControlBillingPageSize {
		small := api.ProductCreditRequest{ID: uuid.NewString(), TenantID: scope.TenantID, OperationID: f.ops["discounted"].ID, AmountMillicents: 1, Reason: fmt.Sprintf("pilot adjustment %d", i)}
		if _, err := s.CreditProductReceipt(ctx, scope, small); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.ListProductBillingDocuments(ctx, scope, "")
	if err != nil || len(page.Documents) != api.ProductControlBillingPageSize || page.NextCursor == "" {
		t.Fatalf("bounded documents: %d %q %v", len(page.Documents), page.NextCursor, err)
	}
	next, err := s.ListProductBillingDocuments(ctx, scope, page.NextCursor)
	if err != nil || len(next.Documents) != 2 || next.NextCursor != "" {
		t.Fatalf("remaining documents: %+v %v", next, err)
	}
	// Returned snapshots are private copies, including their nested lines.
	for _, doc := range append(page.Documents, next.Documents...) {
		if doc.Kind == "statement" {
			doc.Snapshot.Lines[0].AmountMillicents = 0
		}
	}
	final, err := s.ReviewProductBilling(ctx, scope)
	if err != nil || final.Finalized.AmountMillicents != 35000 || final.Finalized.Snapshot.AmountMillicents != 35000 {
		t.Fatalf("credit rewrote finalized amount: %+v %v", final, err)
	}
}

var _ ProductBillingStore = (*MemStore)(nil)
var _ ProductBillingStore = (*PgStore)(nil)
