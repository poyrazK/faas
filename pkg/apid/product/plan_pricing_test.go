package product

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
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
}
