package state

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// productHistoryBackdate moves every plan history entry of a customer to at.
type productHistoryBackdate func(app, tenant string, at time.Time)

// productLastPlan reads a customer's latest plan history entry.
type productLastPlan func(app, tenant string) (string, time.Time)

func TestProductTrialConversionMemory(t *testing.T) {
	s := NewMemStore()
	productTrialConversionContract(t, s, s.SeedProductTrialForTest, func(app, tenant string, at time.Time) {
		s.mu.Lock()
		defer s.mu.Unlock()
		for i := range s.productLocked().planHistory[app+"/"+tenant] {
			s.productLocked().planHistory[app+"/"+tenant][i].at = at.UTC()
		}
	}, func(app, tenant string) (string, time.Time) {
		s.mu.Lock()
		defer s.mu.Unlock()
		history := s.productLocked().planHistory[app+"/"+tenant]
		return history[len(history)-1].plan, history[len(history)-1].at
	})
}

// adr: 20261022180921 — a plan requiring a payment method keeps a customer
// after their trial only with one on file. Until the trial is resolved, a
// customer without one is refused new starts; resolution moves them to the
// fallback plan effective at the trial's end, once, unless their plan changed
// after it. A self-service change to such a plan needs a payment method
// unless the customer would be in a trial.
func productTrialConversionContract(t *testing.T, s productPlanTestStore, seedTrial productTrialSeed, backdate productHistoryBackdate, lastPlan productLastPlan) {
	t.Helper()
	ctx := t.Context()
	f := newProductPlanFixture(ctx, t, s)
	account, app := f.scope.AccountID, f.scope.AppID
	grant := map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 10000}}
	spec := f.spec
	spec.Plans = map[string]api.ProductPlan{"free": {Label: "Free", Features: grant},
		"pro": {Label: "Pro", MonthlyFeeMillicents: 2900000, TrialDays: 14, RequiresPaymentMethod: true, FallbackPlan: "free", Features: grant}}
	spec.SelfService = &api.ProductPlanPolicy{Transitions: map[string][]string{"free": {"pro"}, "pro": {"free"}}}
	if _, err := s.PutProductCatalog(ctx, account, app, spec); err != nil {
		t.Fatal(err)
	}
	conversion := s.(ProductTrialConversionStore)
	orgs := map[string]string{}
	assign := func(tenant, plan string) {
		t.Helper()
		if _, err := s.SetProductCustomer(ctx, account, app, api.ProductCustomer{TenantID: tenant, Plan: plan, AuthOrganizationID: orgs[tenant]}); err != nil {
			t.Fatal(err)
		}
	}
	customer := func(name, plan string) string {
		t.Helper()
		tenant, _, err := s.CreatePlatformTenant(ctx, account, name, name, 100)
		if err != nil {
			t.Fatal(err)
		}
		orgs[tenant.ID] = uuid.NewString()
		assign(tenant.ID, plan)
		return tenant.ID
	}
	get := func(tenant string) api.ProductCustomer {
		t.Helper()
		c, err := s.GetProductCustomer(ctx, account, app, tenant)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	admit := func(tenant, key string) error {
		a := f.admission
		a.PlatformTenantID, a.IdempotencyKey = tenant, key
		_, _, err := s.AdmitOperation(ctx, a)
		return err
	}
	setCard := func(tenant string, onFile bool) api.ProductCustomer {
		t.Helper()
		c, err := conversion.SetProductCustomerPaymentMethod(ctx, account, app, tenant, onFile)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	// end ends a customer's trial a minute ago, after their assignments.
	// Postgres keeps microseconds.
	now := time.Now().UTC().Truncate(time.Microsecond)
	end := func(tenant string) time.Time {
		start, ended := now.AddDate(0, 0, -14), now.Add(-time.Minute)
		backdate(app, tenant, start)
		seedTrial(app, tenant, start, ended)
		return ended
	}
	resolve := func() map[string]ProductTrialResolution {
		t.Helper()
		out := map[string]ProductTrialResolution{}
		for {
			batch, err := conversion.ResolveProductTrials(ctx, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range batch {
				if r.AppID == app {
					out[r.TenantID] = r
				}
			}
			if len(batch) < api.ProductControlTrialResolutionBatch {
				return out
			}
		}
	}

	// During the trial a customer without a payment method starts work.
	held := customer("held", "pro")
	if c := get(held); c.TrialEndsAt == nil || c.PaymentMethodAt != nil || c.TrialResolvedAt != nil {
		t.Fatalf("pro assignment = %+v", c)
	}
	if err := admit(held, "in-trial"); err != nil {
		t.Fatal(err)
	}
	// After it, new starts wait for a payment method or the resolution.
	heldEnd := end(held)
	if err := admit(held, "awaiting"); !errors.Is(err, ErrProductPaymentMethodRequired) {
		t.Fatalf("start after the trial without a payment method: %v", err)
	}
	// Saving one lifts the refusal at once and keeps when it was first seen.
	carded := customer("carded", "pro")
	end(carded)
	first := setCard(carded, true)
	if first.PaymentMethodAt == nil {
		t.Fatalf("recorded payment method = %+v", first)
	}
	if again := setCard(carded, true); again.PaymentMethodAt == nil || !again.PaymentMethodAt.Equal(*first.PaymentMethodAt) {
		t.Fatalf("payment method seen again = %+v, first %s", again, first.PaymentMethodAt)
	}
	if c := get(carded); c.PaymentMethodAt == nil || !c.PaymentMethodAt.Equal(*first.PaymentMethodAt) {
		t.Fatalf("stored payment method = %+v", c)
	}
	if err := admit(carded, "carded"); err != nil {
		t.Fatal(err)
	}
	// A customer assigned another plan after the trial ended keeps it.
	chose := customer("chose", "pro")
	end(chose)
	assign(chose, "free")
	assign(chose, "pro")
	running := customer("running", "pro")
	never := customer("never", "free")

	resolved := resolve()
	if len(resolved) != 3 || resolved[held].FallbackPlan != "free" || !resolved[held].RequiresPaymentMethod || !resolved[held].TrialEndsAt.Equal(heldEnd) ||
		resolved[held].Plan != "pro" || resolved[carded].FallbackPlan != "" || resolved[chose].FallbackPlan != "" {
		t.Fatalf("resolved = %+v", resolved)
	}
	if c := get(held); c.Plan != "free" || c.TrialResolvedAt == nil {
		t.Fatalf("fallen back customer = %+v", c)
	}
	// The move takes effect at the trial's end.
	if plan, at := lastPlan(app, held); plan != "free" || !at.Equal(heldEnd) {
		t.Fatalf("fallback history = %s at %s, want free at %s", plan, at, heldEnd)
	}
	for _, tenant := range []string{carded, chose} {
		if c := get(tenant); c.Plan != "pro" || c.TrialResolvedAt == nil {
			t.Fatalf("kept customer = %+v", c)
		}
	}
	for _, tenant := range []string{running, never} {
		if c := get(tenant); c.TrialResolvedAt != nil {
			t.Fatalf("customer without an ended trial was resolved: %+v", c)
		}
	}
	// A trial is resolved once.
	if again := resolve(); len(again) != 0 {
		t.Fatalf("resolved again = %+v", again)
	}
	// Resolved customers start work on their plan.
	for _, tenant := range []string{held, chose} {
		if err := admit(tenant, "resolved-"+tenant); err != nil {
			t.Fatalf("start after resolution: %v", err)
		}
	}

	// Self-service to pro needs a payment method unless the customer would
	// be in a trial: one the change starts or one running.
	scope := func(tenant string) ProductPlanScope {
		out := f.scope
		out.TenantID, out.OrganizationID = tenant, orgs[tenant]
		return out
	}
	if _, err := s.PreviewProductPlanChange(ctx, scope(held), api.ProductPlanSelection{ID: uuid.NewString(), Plan: "pro"}); !errors.Is(err, ErrProductPaymentMethodRequired) {
		t.Fatalf("self-service to pro after the trial without a payment method: %v", err)
	}
	setCard(held, true)
	if receipt, err := s.ApplyProductPlanChange(ctx, scope(held), productPlanReview(ctx, t, s, scope(held), "pro")); err != nil || receipt.Preview.After.Plan != "pro" ||
		!receipt.Preview.After.RequiresPaymentMethod || receipt.Preview.After.FallbackPlan != "free" {
		t.Fatalf("self-service to pro with a payment method = %+v %v", receipt.Preview.After, err)
	}
	productPlanReview(ctx, t, s, scope(never), "pro")
	assign(running, "free")
	productPlanReview(ctx, t, s, scope(running), "pro")

	// Clearing the record keeps the resolution.
	if cleared := setCard(held, false); cleared.PaymentMethodAt != nil || cleared.TrialResolvedAt == nil {
		t.Fatalf("cleared payment method = %+v", cleared)
	}
	if _, err := conversion.SetProductCustomerPaymentMethod(ctx, account, app, uuid.NewString(), true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("payment method of an unassigned tenant: %v", err)
	}
}
