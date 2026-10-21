package product

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

// adr: 20261021154511 — a catalog with included starts publishes only while
// the release gate is on, refused with a stable code otherwise, and the owner
// console states the included starts before the price.
func TestProductPlanPricingGate(t *testing.T) {
	grant := api.ProductFeatureGrant{MonthlyStarts: 1000, UnitPriceMillicents: 20000}
	spec := func(grant api.ProductFeatureGrant) api.ProductCatalogSpec {
		return api.ProductCatalogSpec{Currency: "EUR", Plans: map[string]api.ProductPlan{"starter": {Features: map[string]api.ProductFeatureGrant{"export": grant}}}}
	}
	included := grant
	included.IncludedStarts = 100
	s := &Service{}
	if err := s.checkPlanPricing(spec(grant)); err != nil {
		t.Fatalf("catalog without included starts: %v", err)
	}
	err := s.checkPlanPricing(spec(included))
	if !errors.Is(err, errPlanPricingUnavailable) {
		t.Fatalf("included starts with the gate unset: %v", err)
	}
	w := httptest.NewRecorder()
	respond(w, nil, err)
	var problem api.Problem
	if json.Unmarshal(w.Body.Bytes(), &problem) != nil || w.Code != http.StatusConflict || problem.Code != api.CodeProductPlanPricingUnavailable {
		t.Fatalf("refusal = %d %s", w.Code, w.Body)
	}
	s.PlanPricingEnabled = func() bool { return false }
	if err := s.checkPlanPricing(spec(included)); !errors.Is(err, errPlanPricingUnavailable) {
		t.Fatalf("included starts with the gate off: %v", err)
	}
	s.PlanPricingEnabled = func() bool { return true }
	if err := s.checkPlanPricing(spec(included)); err != nil {
		t.Fatalf("included starts with the gate on: %v", err)
	}

	catalog := api.ProductCatalog{Spec: spec(included)}
	catalog.Spec.Currency = "EUR"
	if got, want := ownerGrantDescription(catalog, api.ProductCustomer{Plan: "starter"}, "export"), "100 included, then EUR 0.20000 per verified success; 1000 starts / UTC month"; got != want {
		t.Fatalf("owner grant = %q, want %q", got, want)
	}
	fee := spec(grant)
	fee.Plans["starter"] = api.ProductPlan{Features: fee.Plans["starter"].Features, MonthlyFeeMillicents: 2900000}
	s.PlanPricingEnabled = nil
	if err := s.checkPlanPricing(fee); !errors.Is(err, errPlanPricingUnavailable) {
		t.Fatalf("plan fee with the gate unset: %v", err)
	}
	if got := ownerPlanFee(api.ProductCatalog{Spec: fee}, "starter"); got != "EUR 29.00000 / UTC month" {
		t.Fatalf("owner plan fee = %q", got)
	}
	if got := ownerPlanFee(catalog, "starter"); got != "None" {
		t.Fatalf("owner plan without a fee = %q", got)
	}
}

// adr: 20261021154511 — the owner console's billing review names the month's
// plan fee and its proration.
func TestProductOwnerConsoleShowsPlanFee(t *testing.T) {
	joined := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	data := dashboard.ProductControlData{Main: true, AppSlug: "exports", BaseURL: "/dashboard/apps/exports/product-control", Period: "2026-10",
		Billing: &api.ProductBillingReview{Period: "2026-10", Currency: "EUR", AmountMillicents: 1029032,
			Fees: []api.ProductBillingFee{{Plan: "pro", Revision: strings.Repeat("a", 64), MonthlyFeeMillicents: 2900000, AmountMillicents: 1029032, ProratedFrom: &joined}}}}
	w := httptest.NewRecorder()
	if err := dashboard.Render(w, slog.New(slog.NewTextHandler(io.Discard, nil)), "nonce", dashboard.Page{Title: "Product Control", Body: "product_control", Data: data}); err != nil {
		t.Fatal(err)
	}
	if want := "Monthly fee · plan pro · <code>" + strings.Repeat("a", 64) + "</code> · 1029032 millicents EUR · prorated from 2026-10-21"; !strings.Contains(w.Body.String(), want) {
		t.Fatalf("console misses %q", want)
	}
}
