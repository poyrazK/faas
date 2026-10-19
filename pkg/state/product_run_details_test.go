package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type productRunDetailsTestStore interface {
	productWorkspaceActivityTestStore
	ProductRunDetailsStore
}

func readRunDetails(t *testing.T, s ProductRunDetailsStore, scope ProductRunDetailsScope, id string) api.ProductRunDetails {
	t.Helper()
	d, err := s.GetProductRunDetails(t.Context(), scope, id)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestProductControlRunDetailsMemory(t *testing.T) {
	s := NewMemStore()
	insert := func(op Operation) { s.mu.Lock(); defer s.mu.Unlock(); s.insertProductOperationLocked(op) }
	settle := func(op Operation) { s.mu.Lock(); defer s.mu.Unlock(); s.settleProductOperationLocked(op) }
	productRunDetailsContract(t, s, seedProductBilling(t, s, insert, settle))
}

func productRunDetailsContract(t *testing.T, s productRunDetailsTestStore, f productBillingFixture) {
	t.Helper()
	ctx := t.Context()
	auth, err := s.CreateApp(ctx, App{AccountID: f.scope.AccountID, Slug: "run-auth-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := s.GetProductCatalog(ctx, f.scope.AccountID, f.scope.AppID, "")
	if err != nil {
		t.Fatal(err)
	}
	spec := catalog.Spec
	spec.AuthApp = auth.Slug
	spec.Access = &api.ProductAccessPolicy{Roles: map[string][]api.ProductPermission{"all": {api.ProductPermissionViewWork, api.ProductPermissionViewBilling}, "viewer": {api.ProductPermissionViewWork}, "billing": {api.ProductPermissionViewBilling}, "manager": {api.ProductPermissionManagePlan}}}
	// Current terms deliberately differ from the admitted receipt.
	spec.Plans["starter"].Features["export"] = api.ProductFeatureGrant{MonthlyStarts: 99, UnitPriceMillicents: 90000}
	if _, err = s.PutProductCatalog(ctx, f.scope.AccountID, f.scope.AppID, spec); err != nil {
		t.Fatal(err)
	}
	org := uuid.NewString()
	customer := api.ProductCustomer{TenantID: f.scope.TenantID, Plan: "pro", AuthOrganizationID: org}
	if _, err = s.SetProductCustomer(ctx, f.scope.AccountID, f.scope.AppID, customer); err != nil {
		t.Fatal(err)
	}
	scope := ProductRunDetailsScope{ProductPlanScope{AccountID: f.scope.AccountID, AppID: f.scope.AppID, TenantID: f.scope.TenantID, AuthApp: auth.Slug, AuthAppID: auth.ID, OrganizationID: org, Role: "all"}}
	id := f.ops["success"].ID
	before, err := s.ReviewProductBilling(ctx, f.scope)
	if err != nil {
		t.Fatal(err)
	}
	d := readRunDetails(t, s, scope, id)
	if d.Plan != "starter" || d.Allowance != nil || d.StartsConsumed != 1 || d.Outcome.State != "succeeded" || d.Outcome.DetailsAvailable || d.Charge.State != "provisional" || *d.Charge.AmountMillicents != 25000 {
		t.Fatalf("historical terms/outcome: %+v", d)
	}
	viewer := scope
	viewer.Role = "viewer"
	private := readRunDetails(t, s, viewer, id)
	raw, err := json.Marshal(private)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"charge", "currency", "price", "millicents", "waiver", "invoice", "definition", "deployment", "execution", "input", "result\"", "review_hash", "admission_evidence"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("viewer leaked %s: %s", forbidden, raw)
		}
	}
	for _, role := range []string{"billing", "manager", "unknown"} {
		t.Run(role+" cannot read runs", func(t *testing.T) {
			sc := scope
			sc.Role = role
			if _, err := s.GetProductRunDetails(t.Context(), sc, id); !errors.Is(err, ErrProductAccess) {
				t.Fatal(err)
			}
		})
	}
	for _, name := range []string{"failed", "cancelled", "expired", "unknown"} {
		x := readRunDetails(t, s, scope, f.ops[name].ID)
		if x.StartsConsumed != 1 {
			t.Fatal("start was returned")
		}
		if name == "failed" || name == "cancelled" {
			if x.Outcome.State != name || x.Charge.State != "not_billable" || *x.Charge.AmountMillicents != 0 {
				t.Fatal(x)
			}
		} else if x.Outcome.State != "unavailable" || x.Charge.State != "unresolved" || x.Charge.AmountMillicents != nil || x.Charge.NetAmountMillicents != nil {
			t.Fatal("missing work inferred an outcome/zero charge", x)
		}
	}
	after, err := s.ReviewProductBilling(ctx, f.scope)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("read changed billing", err)
	}
	for _, name := range []string{"expired", "unknown"} {
		if _, err = s.WaiveProductReceipt(ctx, f.scope, f.ops[name].ID, "PRIVATE_WAIVER_REASON"); err != nil {
			t.Fatal(err)
		}
		x := readRunDetails(t, s, scope, f.ops[name].ID)
		if x.Charge.State != "waived" || *x.Charge.AmountMillicents != 0 || x.Outcome.State != "unavailable" {
			t.Fatal("waiver invented an outcome", x)
		}
	}
	review, err := s.ReviewProductBilling(ctx, f.scope)
	if err != nil {
		t.Fatal(err)
	}
	statement, err := s.FinalizeProductBilling(ctx, f.scope, review.ReviewHash)
	if err != nil {
		t.Fatal(err)
	}
	d = readRunDetails(t, s, scope, id)
	if d.Charge.State != "finalized" || d.Charge.Statement.ID != statement.ID || *d.Charge.NetAmountMillicents != 25000 {
		t.Fatal(d)
	}
	for i, amount := range []int64{1000, 2000, 22000} {
		_, err = s.CreditProductReceipt(ctx, f.scope, api.ProductCreditRequest{ID: uuid.NewString(), TenantID: scope.TenantID, OperationID: id, AmountMillicents: amount, Reason: "PRIVATE_CREDIT_REASON"})
		if err != nil {
			t.Fatal(err)
		}
		d = readRunDetails(t, s, scope, id)
		if d.Charge.CreditCount != int64(i+1) || d.Charge.CreditMillicents >= 0 || *d.Charge.NetAmountMillicents != 25000+d.Charge.CreditMillicents {
			t.Fatal(d)
		}
		if i < 2 && d.Charge.State != "partially_credited" || i == 2 && d.Charge.State != "credited" {
			t.Fatal(d)
		}
	}
	d.Charge.Statement.ID = uuid.NewString()
	*d.Charge.AmountMillicents = 999
	detached := readRunDetails(t, s, scope, id)
	if detached.Charge.Statement.ID != statement.ID || *detached.Charge.AmountMillicents != 25000 {
		t.Fatal("aliased source evidence")
	}
	raw, err = json.Marshal(detached)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"PRIVATE_", "reason", "snapshot", "handoff", "actor", "definition", "deployment", "executions"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("private evidence leaked", forbidden)
		}
	}
	if x := readRunDetails(t, s, scope, f.ops["discounted"].ID); x.Charge.CreditCount != 0 || *x.Charge.NetAmountMillicents != 10000 {
		t.Fatal("other receipt credits leaked", x)
	}
	for _, tc := range []struct {
		name  string
		alter func(*ProductRunDetailsScope)
		id    string
	}{
		{"other tenant", func(s *ProductRunDetailsScope) { s.TenantID = f.other.TenantID }, id},
		{"other app", func(s *ProductRunDetailsScope) { s.AppID = auth.ID }, id},
		{"other account", func(s *ProductRunDetailsScope) { s.AccountID = uuid.NewString() }, id},
		{"other org", func(s *ProductRunDetailsScope) { s.OrganizationID = uuid.NewString() }, id},
		{"other realm", func(s *ProductRunDetailsScope) { s.AuthAppID = uuid.NewString() }, id},
		{"other receipt", func(*ProductRunDetailsScope) {}, f.ops["bob"].ID},
		{"malformed receipt", func(*ProductRunDetailsScope) {}, "bad"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := scope
			tc.alter(&sc)
			if _, err := s.GetProductRunDetails(t.Context(), sc, tc.id); err == nil {
				t.Fatal("foreign/invalid scope accepted")
			}
		})
	}
	// Pausing new work does not revoke the existing read permission.
	customer.Suspended = true
	if _, err = s.SetProductCustomer(ctx, scope.AccountID, scope.AppID, customer); err != nil {
		t.Fatal(err)
	}
	_ = readRunDetails(t, s, scope, id)
	customer.AuthOrganizationID = uuid.NewString()
	if _, err = s.SetProductCustomer(ctx, scope.AccountID, scope.AppID, customer); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetProductRunDetails(ctx, scope, id); !errors.Is(err, ErrProductAccess) {
		t.Fatal("old mapping still authorized", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = s.GetProductRunDetails(cancelled, scope, id); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", err)
	}
}

func TestProductControlRunDetailsLiveMemory(t *testing.T) {
	s := NewMemStore()
	persist := func(op Operation, settled bool) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.operationMemoryLocked().operations[op.ID] = cloneOperation(op)
		if settled {
			s.settleProductOperationLocked(op)
		}
	}
	productRunDetailsLiveContract(t, s, persist)
}

func productRunDetailsLiveContract(t *testing.T, s productRunDetailsTestStore, persist func(Operation, bool)) {
	t.Helper()
	f := helpFixture(t.Context(), t, s)
	f.admission.IdempotencyKey = uuid.NewString()
	op, _, err := s.AdmitOperation(t.Context(), f.admission)
	if err != nil {
		t.Fatal(err)
	}
	scope := ProductRunDetailsScope{f.scope}
	scope.Role = "viewer"
	op.ExpiresAt = time.Now().Add(-time.Hour)
	persist(op, false)
	d := readRunDetails(t, s, scope, op.ID)
	if d.Outcome.State != "accepted" || !d.Outcome.DetailsAvailable || d.Charge != nil || d.Allowance == nil || d.Allowance.StartsAfter != d.Allowance.StartsBefore+1 {
		t.Fatal("active work/allowance missing", d)
	}
	d.Allowance.StartsBefore = 999
	if d = readRunDetails(t, s, scope, op.ID); d.Allowance.StartsBefore == 999 {
		t.Fatal("aliased admission evidence")
	}
	op.State, op.ExpiresAt = api.OperationRequiresReconciliation, time.Now().Add(time.Hour)
	persist(op, false)
	if d = readRunDetails(t, s, scope, op.ID); d.Outcome.State != "requires_reconciliation" || !d.Outcome.DetailsAvailable {
		t.Fatal(d)
	}
	op.ExpiresAt = time.Now().Add(-time.Second)
	persist(op, false)
	if d = readRunDetails(t, s, scope, op.ID); d.Outcome.State != "unavailable" || d.Outcome.DetailsAvailable || d.Outcome.ResolvedAt != nil {
		t.Fatal("expired attention invented outcome", d)
	}
	op.State, op.UpdatedAt, op.ExpiresAt = api.OperationSucceeded, time.Now().UTC(), time.Now().Add(time.Hour)
	op.Result = json.RawMessage(`{"PRIVATE_RESULT":true}`)
	persist(op, true)
	if d = readRunDetails(t, s, scope, op.ID); d.Outcome.State != "succeeded" || !d.Outcome.ResultAvailable {
		t.Fatal(d)
	}
	op.ExpiresAt = time.Now().Add(-time.Second)
	persist(op, false)
	if d = readRunDetails(t, s, scope, op.ID); d.Outcome.State != "succeeded" || d.Outcome.DetailsAvailable || d.Outcome.ResultAvailable {
		t.Fatal("outcome lost on detail expiry", d)
	}
	// Permission changes for the same role take effect on every read.
	f.spec.Access.Roles["viewer"] = []api.ProductPermission{api.ProductPermissionViewBilling}
	if _, err = s.PutProductCatalog(t.Context(), scope.AccountID, scope.AppID, f.spec); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetProductRunDetails(t.Context(), scope, op.ID); !errors.Is(err, ErrProductAccess) {
		t.Fatal("stale role permissions accepted", err)
	}
}
