package state

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type productOwnerTestStore interface {
	productBillingTestStore
	ProductOwnerStore
}

func TestProductControlOwnerMemory(t *testing.T) {
	s := NewMemStore()
	insert := func(op Operation) { s.mu.Lock(); defer s.mu.Unlock(); s.insertProductOperationLocked(op) }
	settle := func(op Operation) { s.mu.Lock(); defer s.mu.Unlock(); s.settleProductOperationLocked(op) }
	f := seedProductBilling(t, s, insert, settle)
	productOwnerContract(t, s, f, insert)
}

func productOwnerContract(t *testing.T, s productOwnerTestStore, f productBillingFixture, insert func(Operation)) {
	t.Helper()
	ctx := t.Context()
	scope := f.scope
	current, err := s.GetProductCustomer(ctx, scope.AccountID, scope.AppID, scope.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	current.AuthOrganizationID = uuid.NewString()
	if _, err = s.SetProductCustomer(ctx, scope.AccountID, scope.AppID, current); err != nil {
		t.Fatal(err)
	}
	target := api.ProductCustomer{TenantID: scope.TenantID, Plan: "pro"}
	p, err := s.PreviewProductCustomerChange(ctx, scope.AccountID, scope.AppID, target)
	if err != nil || p.Before == nil || p.After.AuthOrganizationID != current.AuthOrganizationID || p.Before.Plan != "starter" || p.After.Plan != "pro" {
		t.Fatalf("preview: %+v %v", p, err)
	}
	req := api.ProductCustomerChangeRequest{ID: uuid.NewString(), ExpectedHash: p.Hash, Customer: p.After}
	// A normal legacy assignment write between preview and confirmation must
	// invalidate the confirmation, including identity-only changes.
	changed := current
	changed.AuthOrganizationID = uuid.NewString()
	if _, err = s.SetProductCustomer(ctx, scope.AccountID, scope.AppID, changed); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyProductCustomerChange(ctx, scope.AccountID, scope.AppID, req); !errors.Is(err, ErrConflict) && !errors.Is(err, ErrProductCustomerChangeStale) {
		t.Fatalf("stale identity accepted: %v", err)
	}
	if _, err = s.SetProductCustomer(ctx, scope.AccountID, scope.AppID, current); err != nil {
		t.Fatal(err)
	}
	r, err := s.ApplyProductCustomerChange(ctx, scope.AccountID, scope.AppID, req)
	if err != nil {
		t.Fatal(err)
	}
	if r.Before == nil || r.After.AuthOrganizationID != current.AuthOrganizationID || r.After.Plan != "pro" {
		t.Fatalf("receipt: %+v", r)
	}
	// A retry after a subsequent change returns its original receipt without
	// applying the older plan again.
	later := current
	later.Suspended = true
	if _, err = s.SetProductCustomer(ctx, scope.AccountID, scope.AppID, later); err != nil {
		t.Fatal(err)
	}
	retry, err := s.ApplyProductCustomerChange(ctx, scope.AccountID, scope.AppID, req)
	if err != nil || !reflect.DeepEqual(r, retry) {
		t.Fatalf("lost-response recovery: %+v %v", retry, err)
	}
	got, err := s.GetProductCustomer(ctx, scope.AccountID, scope.AppID, scope.TenantID)
	if err != nil || got != later {
		t.Fatalf("retry overwrote later assignment: %+v %v", got, err)
	}
	r.Before.Plan = "mutated"
	again, err := s.ApplyProductCustomerChange(ctx, scope.AccountID, scope.AppID, req)
	if err != nil || again.Before.Plan != "starter" {
		t.Fatalf("receipt alias: %+v %v", again, err)
	}
	bad := req
	bad.Customer.Suspended = true
	if _, err = s.ApplyProductCustomerChange(ctx, scope.AccountID, scope.AppID, bad); !errors.Is(err, ErrConflict) {
		t.Fatalf("reused ID: %v", err)
	}
	if _, err = s.PreviewProductCustomerChange(ctx, uuid.NewString(), scope.AppID, target); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign owner: %v", err)
	}
	badTarget := target
	badTarget.AuthOrganizationID = uuid.NewString()
	if _, err = s.PreviewProductCustomerChange(ctx, scope.AccountID, scope.AppID, badTarget); !errors.Is(err, ErrConflict) {
		t.Fatalf("identity replacement: %v", err)
	}

	// Catalog publication (including portal-realm changes) stales a preview
	// even when the selected immutable price revision is unchanged.
	p, err = s.PreviewProductCustomerChange(ctx, scope.AccountID, scope.AppID, target)
	if err != nil {
		t.Fatal(err)
	}
	spec := p.Catalog.Spec
	spec.Plans["extra"] = api.ProductPlan{Label: "Extra", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 30, UnitPriceMillicents: 9000}}}
	if _, err = s.PutProductCatalog(ctx, scope.AccountID, scope.AppID, spec); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyProductCustomerChange(ctx, scope.AccountID, scope.AppID, api.ProductCustomerChangeRequest{ID: uuid.NewString(), ExpectedHash: p.Hash, Customer: p.After}); !errors.Is(err, ErrProductCustomerChangeStale) {
		t.Fatalf("stale catalog accepted: %v", err)
	}
	p, err = s.PreviewProductCustomerChange(ctx, scope.AccountID, scope.AppID, target)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Go(func() {
			_, err := s.ApplyProductCustomerChange(ctx, scope.AccountID, scope.AppID, api.ProductCustomerChangeRequest{ID: uuid.NewString(), ExpectedHash: p.Hash, Customer: p.After})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	success, stale := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrProductCustomerChangeStale) {
			stale++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || stale != 1 {
		t.Fatalf("confirmation race: %d applied, %d stale", success, stale)
	}
	productOwnerAssignmentContract(t, s, f, req.ID, current.AuthOrganizationID)
	usage, err := s.GetProductUsage(ctx, scope.AccountID, scope.AppID, scope.TenantID, scope.Period)
	var starts, amount int64
	for _, line := range usage.Lines {
		starts += line.Starts
		amount += line.AmountMillicents
	}
	if err != nil || starts != 6 || amount != 35000 {
		t.Fatalf("plan change rewrote admissions: %+v %v", usage, err)
	}
	productOwnerReadContract(t, s, f, insert)
}

func productOwnerAssignmentContract(t *testing.T, s productOwnerTestStore, f productBillingFixture, usedID, existingOrg string) {
	t.Helper()
	ctx := t.Context()
	scope := f.scope
	// A collision in a different tenant's scope must roll back its assignment,
	// including when PostgreSQL discovers the UUID conflict after the upsert.
	before, err := s.GetProductCustomer(ctx, scope.AccountID, scope.AppID, f.other.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.PreviewProductCustomerChange(ctx, scope.AccountID, scope.AppID, api.ProductCustomer{TenantID: f.other.TenantID, Plan: "starter"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyProductCustomerChange(ctx, scope.AccountID, scope.AppID, api.ProductCustomerChangeRequest{ID: usedID, ExpectedHash: p.Hash, Customer: p.After}); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-tenant ID collision: %v", err)
	}
	after, err := s.GetProductCustomer(ctx, scope.AccountID, scope.AppID, f.other.TenantID)
	if err != nil || before != after {
		t.Fatalf("conflict changed assignment: %+v %v", after, err)
	}
	tenant, _, err := s.CreatePlatformTenant(ctx, scope.AccountID, "new-console-customer", "New console customer", 100)
	if err != nil {
		t.Fatal(err)
	}
	target := api.ProductCustomer{TenantID: tenant.ID, Plan: "starter", AuthOrganizationID: existingOrg}
	p, err = s.PreviewProductCustomerChange(ctx, scope.AccountID, scope.AppID, target)
	if err != nil || p.Before != nil {
		t.Fatalf("first preview: %+v %v", p, err)
	}
	if _, err = s.ApplyProductCustomerChange(ctx, scope.AccountID, scope.AppID, api.ProductCustomerChangeRequest{ID: uuid.NewString(), ExpectedHash: p.Hash, Customer: p.After}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate organization: %v", err)
	}
	if _, err = s.GetProductCustomer(ctx, scope.AccountID, scope.AppID, tenant.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed first assignment persisted: %v", err)
	}
	target.AuthOrganizationID = uuid.NewString()
	p, err = s.PreviewProductCustomerChange(ctx, scope.AccountID, scope.AppID, target)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := s.ApplyProductCustomerChange(ctx, scope.AccountID, scope.AppID, api.ProductCustomerChangeRequest{ID: uuid.NewString(), ExpectedHash: p.Hash, Customer: p.After})
	if err != nil || receipt.Before != nil || receipt.After.AuthOrganizationID != target.AuthOrganizationID {
		t.Fatalf("first assignment: %+v %v", receipt, err)
	}
}

func productOwnerReadContract(t *testing.T, s productOwnerTestStore, f productBillingFixture, insert func(Operation)) {
	t.Helper()
	ctx := t.Context()
	scope := f.scope
	page, err := s.ListProductOwnerAttention(ctx, scope.AccountID, scope.AppID, "")
	if err != nil || len(page.Items) != 3 {
		t.Fatalf("attention: %+v %v", page, err)
	}
	for _, item := range page.Items {
		if item.TenantID == scope.TenantID && (item.Unresolved != 2 || item.NeedsReview != 2) {
			t.Fatalf("missing independent evidence: %+v", item)
		}
	}
	if _, err = s.ListProductOwnerAttention(ctx, uuid.NewString(), scope.AppID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign attention: %v", err)
	}
	for _, name := range []string{"unknown", "expired"} {
		if _, err = s.WaiveProductReceipt(ctx, scope, f.ops[name].ID, "console write-off"); err != nil {
			t.Fatal(err)
		}
	}
	review, err := s.ReviewProductBilling(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := s.FinalizeProductBilling(ctx, scope, review.ReviewHash)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetProductOwnerDocument(ctx, f.other, doc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign document: %v", err)
	}
	credit := api.ProductCreditRequest{ID: uuid.NewString(), TenantID: scope.TenantID, OperationID: f.ops["success"].ID, AmountMillicents: 1234, Reason: "service credit"}
	if _, err = s.CreditProductReceipt(ctx, scope, credit); err != nil {
		t.Fatal(err)
	}
	page, err = s.ListProductOwnerAttention(ctx, scope.AccountID, scope.AppID, "")
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("finalized period remains queued: %+v %v", page, err)
	}
	runs, err := s.ListProductOwnerRuns(ctx, scope, "")
	if err != nil || len(runs.Runs) != 6 {
		t.Fatalf("runs: %+v %v", runs, err)
	}
	for _, run := range runs.Runs {
		if run.Evidence.OperationID == credit.OperationID && run.CreditedMillicents != 1234 {
			t.Fatalf("credit projection: %+v", run)
		}
	}
	// Newer receipts and a timestamp tie exercise keyset pagination. No result
	// rows exist; independent commercial evidence must still be complete.
	for i := 0; i < api.ProductControlOwnerPageSize; i++ {
		op := f.ops["success"]
		op.ID = uuid.NewString()
		receipt := *op.ProductControl
		receipt.AdmittedAt = receipt.AdmittedAt.Add(time.Hour)
		op.ProductControl = &receipt
		insert(op)
	}
	first, err := s.ListProductOwnerRuns(ctx, scope, "")
	if err != nil || len(first.Runs) != api.ProductControlOwnerPageSize || first.NextCursor == "" {
		t.Fatalf("first runs page: %d %v", len(first.Runs), err)
	}
	second, err := s.ListProductOwnerRuns(ctx, scope, first.NextCursor)
	if err != nil || len(second.Runs) != 6 || second.NextCursor != "" {
		t.Fatalf("second runs page: %d %v", len(second.Runs), err)
	}
	seen := map[string]bool{}
	var previous *api.ProductReceiptEvidence
	for _, run := range append(first.Runs, second.Runs...) {
		e := run.Evidence
		if seen[e.OperationID] {
			t.Fatal("duplicate cursor row")
		}
		seen[e.OperationID] = true
		if previous != nil && (e.Receipt.AdmittedAt.After(previous.Receipt.AdmittedAt) || e.Receipt.AdmittedAt.Equal(previous.Receipt.AdmittedAt) && e.OperationID >= previous.OperationID) {
			t.Fatal("run ordering")
		}
		previous = &e
	}
	if _, err = s.ListProductOwnerRuns(ctx, f.other, first.NextCursor); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("cross-customer cursor: %v", err)
	}
	if _, err = s.ListProductOwnerRuns(ctx, scope, "invalid"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid cursor: %v", err)
	}
	// Independently retained receipts in many closed months exercise the
	// attention cursor separately from the recent-run cursor.
	for i := 0; i < api.ProductControlOwnerPageSize+1; i++ {
		op := f.ops["success"]
		op.ID = uuid.NewString()
		receipt := *op.ProductControl
		receipt.AdmittedAt = time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, i, 0)
		receipt.Period = receipt.AdmittedAt.Format("2006-01")
		op.ProductControl = &receipt
		insert(op)
	}
	a, err := s.ListProductOwnerAttention(ctx, scope.AccountID, scope.AppID, "")
	if err != nil || len(a.Items) != api.ProductControlOwnerPageSize || a.NextCursor == "" {
		t.Fatalf("attention first page: %d %v", len(a.Items), err)
	}
	b, err := s.ListProductOwnerAttention(ctx, scope.AccountID, scope.AppID, a.NextCursor)
	if err != nil || len(b.Items) != 3 || b.NextCursor != "" {
		t.Fatalf("attention next page: %d %v", len(b.Items), err)
	}
	seenPeriods := map[string]bool{}
	for _, item := range append(a.Items, b.Items...) {
		key := item.Period + "/" + item.TenantID
		if seenPeriods[key] {
			t.Fatal("duplicate attention row")
		}
		seenPeriods[key] = true
	}
	if _, err = s.ListProductOwnerAttention(ctx, scope.AccountID, scope.AppID, first.NextCursor); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("cross-query cursor: %v", err)
	}
}
