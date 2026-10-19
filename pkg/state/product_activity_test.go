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
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

type productActivityTestStore interface {
	productBillingTestStore
	ProductOwnerStore
	ProductOnboardingStore
	ProductSelfServiceStore
	ProductCustomerActivityStore
}

func TestProductControlActivityMemory(t *testing.T) {
	s := NewMemStore()
	insert := func(op Operation) { s.mu.Lock(); defer s.mu.Unlock(); s.insertProductOperationLocked(op) }
	settle := func(op Operation) { s.mu.Lock(); defer s.mu.Unlock(); s.settleProductOperationLocked(op) }
	productActivityContract(t, s, seedProductBilling(t, s, insert, settle), insert)
}

func activityTestRead(t *testing.T, s ProductCustomerActivityStore, scope ProductBillingScope, cursor string) api.ProductCustomerActivityPage {
	t.Helper()
	p, err := s.ListProductCustomerActivity(t.Context(), scope.AccountID, scope.AppID, scope.TenantID, cursor)
	if err != nil {
		t.Fatal(err)
	}
	if err = productcontrol.ValidateActivityPage(p, cursor); err != nil {
		t.Fatalf("activity contract: %+v %v", p, err)
	}
	return p
}

func productActivityContract(t *testing.T, s productActivityTestStore, f productBillingFixture, insert func(Operation)) {
	t.Helper()
	ctx, scope := t.Context(), f.scope
	initial := activityTestRead(t, s, scope, "")
	if len(initial.Events) != 10 || initial.NextCursor != "" {
		t.Fatalf("retained admissions/outcomes missing: %+v", initial)
	}
	for _, e := range initial.Events {
		if e.SourceID == f.ops["expired"].ID && e.Kind != "work_admitted" {
			t.Fatal("result expiry was inferred as an outcome")
		}
		if e.Terms.Admission != nil {
			t.Fatal("missing legacy admission evidence was invented")
		}
	}

	// Record setup and plan changes through their normal write contracts.
	auth, err := s.CreateApp(ctx, App{AccountID: scope.AccountID, Slug: "activity-auth-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := s.GetProductCatalog(ctx, scope.AccountID, scope.AppID, "")
	if err != nil {
		t.Fatal(err)
	}
	spec := catalog.Spec
	spec.AuthApp = auth.Slug
	spec.Access = &api.ProductAccessPolicy{Roles: map[string][]api.ProductPermission{"owner": {api.ProductPermissionManagePlan}}}
	spec.SelfService = &api.ProductPlanPolicy{Transitions: map[string][]string{"pro": {"starter"}}}
	head, err := s.PutProductCatalog(ctx, scope.AccountID, scope.AppID, spec)
	if err != nil {
		t.Fatal(err)
	}
	org := uuid.NewString()
	p, err := s.PreviewProductOnboarding(ctx, scope.AccountID, scope.AppID, api.ProductOnboardingRequest{ID: uuid.NewString(), TenantID: scope.TenantID, Plan: "starter", AuthApp: auth.Slug, AuthAppID: auth.ID, OrganizationID: org, OrganizationName: "Timeline workspace"})
	if err != nil {
		t.Fatal(err)
	}
	onboarding, err := s.ApplyProductOnboarding(ctx, scope.AccountID, scope.AppID, api.ProductOnboardingConfirmation{Request: p.Request, ExpectedHash: p.Hash})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ObserveProductCustomerAccess(ctx, scope.AccountID, scope.AppID, scope.TenantID, auth.Slug, org); err != nil {
		t.Fatal(err)
	}
	newOp := f.ops["success"]
	newOp.ID = uuid.NewString()
	terms := *newOp.ProductControl
	terms.AdmittedAt = onboarding.CreatedAt.Add(time.Microsecond)
	terms.Period = productcontrol.Period(terms.AdmittedAt)
	newOp.ProductControl, newOp.UpdatedAt = &terms, terms.AdmittedAt.Add(time.Microsecond)
	insert(newOp)
	f.settle(newOp)
	change, err := s.PreviewProductCustomerChange(ctx, scope.AccountID, scope.AppID, api.ProductCustomer{TenantID: scope.TenantID, Plan: "pro", Revision: head.Revision})
	if err != nil {
		t.Fatal(err)
	}
	ownerChange, err := s.ApplyProductCustomerChange(ctx, scope.AccountID, scope.AppID, api.ProductCustomerChangeRequest{ID: uuid.NewString(), ExpectedHash: change.Hash, Customer: change.After})
	if err != nil {
		t.Fatal(err)
	}
	planScope := ProductPlanScope{AccountID: scope.AccountID, AppID: scope.AppID, TenantID: scope.TenantID, AuthApp: auth.Slug, AuthAppID: auth.ID, OrganizationID: org, Role: "owner"}
	plan, err := s.PreviewProductPlanChange(ctx, planScope, api.ProductPlanSelection{ID: uuid.NewString(), Plan: "starter"})
	if err != nil {
		t.Fatal(err)
	}
	planChange, err := s.ApplyProductPlanChange(ctx, planScope, api.ProductPlanChangeConfirmation{Request: plan.Request, ExpectedHash: plan.Hash})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"unknown", "expired"} {
		if _, err = s.WaiveProductReceipt(ctx, scope, f.ops[name].ID, "Pilot <review> write-off"); err != nil {
			t.Fatal(err)
		}
	}
	review, err := s.ReviewProductBilling(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	statement, err := s.FinalizeProductBilling(ctx, scope, review.ReviewHash)
	if err != nil {
		t.Fatal(err)
	}
	credit, err := s.CreditProductReceipt(ctx, scope, api.ProductCreditRequest{ID: uuid.NewString(), TenantID: scope.TenantID, OperationID: f.ops["success"].ID, AmountMillicents: 1000, Reason: "Pilot correction"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.HandoffProductBilling(ctx, scope, statement.ID, "pilot-invoice"); err != nil {
		t.Fatal(err)
	}
	full := activityTestRead(t, s, scope, "")
	if len(full.Events) != 22 {
		t.Fatalf("event count: %d, want 22", len(full.Events))
	}
	kinds := map[string]int{}
	for _, e := range full.Events {
		kinds[e.Kind]++
		switch e.Kind {
		case "customer_assignment_changed":
			if e.SourceID != ownerChange.ID || e.Assignment.Before.Plan != "starter" || e.Assignment.After.Plan != "pro" || e.Assignment.ActorRole != "" {
				t.Fatalf("owner evidence: %+v", e)
			}
		case "customer_plan_changed":
			if e.SourceID != planChange.ID || e.Assignment.ActorRole != "owner" || e.Assignment.Before.Plan != "pro" || e.Assignment.After.Plan != "starter" {
				t.Fatalf("customer plan evidence: %+v", e)
			}
		case "onboarding_first_success":
			if e.SourceID != onboarding.ID || e.Milestone.OperationID != newOp.ID {
				t.Fatalf("first success: %+v", e)
			}
		case "billing_document_created":
			if e.Document.Snapshot != nil || (e.SourceID != statement.ID && e.SourceID != credit.ID) {
				t.Fatalf("document evidence: %+v", e)
			}
		}
		if e.SourceID == f.ops["success"].ID && e.Terms != nil && (e.Terms.Revision != catalog.Revision || e.Terms.UnitPriceMillicents != 25000) {
			t.Fatal("historical terms changed with the plan")
		}
	}
	if len(kinds) != 10 {
		t.Fatalf("source kinds missing: %+v", kinds)
	}
	retained := activityTestRead(t, s, scope, "")
	raw, _ := json.Marshal(full)
	for _, forbidden := range []string{"preview_hash", "expected_hash", "fingerprint", "snapshot", "auth_access_token", "input_schema"} {
		if strings.Contains(string(raw), `"`+forbidden+`"`) {
			t.Fatalf("unsafe projection: %s", forbidden)
		}
	}
	// Returned pointers must not be able to rewrite retained source records.
	for i := range full.Events {
		if full.Events[i].Assignment != nil {
			full.Events[i].Assignment.After.Plan = "tampered"
		}
	}
	if again := activityTestRead(t, s, scope, ""); !reflect.DeepEqual(again.Events, retained.Events) {
		t.Fatal("returned evidence aliases the store")
	}
	otherApp, err := s.CreateApp(ctx, App{AccountID: scope.AccountID, Slug: "activity-other-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PutProductCatalog(ctx, scope.AccountID, otherApp.ID, spec); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetProductCustomer(ctx, scope.AccountID, otherApp.ID, api.ProductCustomer{TenantID: scope.TenantID, Plan: "starter"}); err != nil {
		t.Fatal(err)
	}
	otherScope := scope
	otherScope.AppID = otherApp.ID
	if other := activityTestRead(t, s, otherScope, ""); len(other.Events) != 0 {
		t.Fatal("same tenant leaked history across products")
	}

	// More than a page of simultaneous events exercises ID and kind tie breakers.
	want := len(full.Events)
	for range api.ProductControlActivityPageSize + 3 {
		op := f.ops["success"]
		op.ID = uuid.NewString()
		insert(op)
		want++
	}
	first := activityTestRead(t, s, scope, "")
	if first.NextCursor == "" || len(first.Events) != api.ProductControlActivityPageSize {
		t.Fatal("missing bounded continuation")
	}
	seen, cursor := map[string]bool{}, ""
	for pages := 0; ; pages++ {
		if pages > 3 {
			t.Fatal("pagination did not terminate")
		}
		page := activityTestRead(t, s, scope, cursor)
		for _, e := range page.Events {
			if seen[e.ID] {
				t.Fatal("duplicate event across pages")
			}
			seen[e.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != want {
		t.Fatalf("pagination lost events: %d want %d", len(seen), want)
	}
	second := activityTestRead(t, s, scope, first.NextCursor)
	fresh := f.ops["success"]
	fresh.ID = uuid.NewString()
	freshTerms := *fresh.ProductControl
	freshTerms.AdmittedAt, freshTerms.Period = time.Now().UTC().Add(time.Second), productcontrol.Period(time.Now())
	fresh.ProductControl = &freshTerms
	insert(fresh)
	if again := activityTestRead(t, s, scope, first.NextCursor); !reflect.DeepEqual(again.Events, second.Events) {
		t.Fatal("newer insertion shifted the older-page boundary")
	}
	if newest := activityTestRead(t, s, scope, ""); newest.Events[0].SourceID != fresh.ID {
		t.Fatal("refresh did not discover later records")
	}
	for _, bad := range []struct {
		account, app, tenant, cursor string
		want                         error
	}{
		{uuid.NewString(), scope.AppID, scope.TenantID, "", ErrNotFound},
		{scope.AccountID, uuid.NewString(), scope.TenantID, "", ErrNotFound},
		{scope.AccountID, scope.AppID, uuid.NewString(), "", ErrNotFound},
		{scope.AccountID, scope.AppID, f.other.TenantID, first.NextCursor, ErrInvalidArgument},
		{scope.AccountID, scope.AppID, scope.TenantID, "bad", ErrInvalidArgument},
		{scope.AccountID, scope.AppID, "bad", "", ErrInvalidArgument},
	} {
		if _, err := s.ListProductCustomerActivity(ctx, bad.account, bad.app, bad.tenant, bad.cursor); !errors.Is(err, bad.want) {
			t.Fatalf("scope/cursor denial: %v want %v", err, bad.want)
		}
	}
	if other := activityTestRead(t, s, f.other, ""); len(other.Events) != 2 {
		t.Fatalf("other customer leaked records: %+v", other)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.ListProductCustomerActivity(canceled, scope.AccountID, scope.AppID, scope.TenantID, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled activity succeeded: %v", err)
	}
}
