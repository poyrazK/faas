package productcontrol

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestProductCatalogValidationAndRevision(t *testing.T) {
	base := func() api.ProductCatalogSpec {
		return api.ProductCatalogSpec{Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", DefinitionID: uuid.NewString()}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 100, UnitPriceMillicents: 25000}}}}}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*api.ProductCatalogSpec)
	}{
		{"currency", func(s *api.ProductCatalogSpec) { s.Currency = "eur" }},
		{"missing definition", func(s *api.ProductCatalogSpec) { s.Features["export"] = api.ProductFeature{Label: "Export"} }},
		{"foreign feature", func(s *api.ProductCatalogSpec) {
			s.Plans["starter"].Features["unknown"] = api.ProductFeatureGrant{MonthlyStarts: 1}
		}},
		{"negative price", func(s *api.ProductCatalogSpec) {
			s.Plans["starter"].Features["export"] = api.ProductFeatureGrant{MonthlyStarts: 1, UnitPriceMillicents: -1}
		}},
		{"unbounded quota", func(s *api.ProductCatalogSpec) {
			s.Plans["starter"].Features["export"] = api.ProductFeatureGrant{MonthlyStarts: api.ProductControlMaxMonthlyStarts + 1}
		}},
		{"included above the monthly limit", func(s *api.ProductCatalogSpec) {
			s.Plans["starter"].Features["export"] = api.ProductFeatureGrant{MonthlyStarts: 10, UnitPriceMillicents: 1, IncludedStarts: 11}
		}},
		{"negative included", func(s *api.ProductCatalogSpec) {
			s.Plans["starter"].Features["export"] = api.ProductFeatureGrant{MonthlyStarts: 10, IncludedStarts: -1}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := base()
			tc.mutate(&s)
			if _, err := Compile(s); err == nil {
				t.Fatal("accepted invalid catalog")
			}
		})
	}
	spec := base()
	a, err := Compile(spec)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Compile(spec)
	if err != nil || a.Revision != b.Revision {
		t.Fatal("unstable content revision")
	}
	spec.Features["export"] = api.ProductFeature{Label: "Changed", DefinitionID: spec.Features["export"].DefinitionID}
	if a.Spec.Features["export"].Label != "Export" {
		t.Fatal("caller mutated immutable catalog")
	}
	if Period(time.Date(2026, 11, 1, 0, 30, 0, 0, time.FixedZone("east", 3600))) != "2026-10" {
		t.Fatal("period is not UTC")
	}
	// adr: 20261021154511 — all starts included is valid, and only included
	// starts need the plan pricing release gate.
	if UsesPlanPricing(base()) {
		t.Fatal("a catalog without included starts needs the plan pricing gate")
	}
	included := base()
	included.Plans["starter"].Features["export"] = api.ProductFeatureGrant{MonthlyStarts: 100, UnitPriceMillicents: 25000, IncludedStarts: 100}
	if _, err := Compile(included); err != nil || !UsesPlanPricing(included) {
		t.Fatalf("included starts: %v, uses plan pricing %v", err, UsesPlanPricing(included))
	}
}

func TestProductNamedBindings(t *testing.T) {
	base := func() api.ProductCatalogSpec {
		return api.ProductCatalogSpec{Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: "production"}}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 100, UnitPriceMillicents: 25000}}}}}
	}
	spec := base()
	catalog, err := Compile(spec)
	if err != nil {
		t.Fatal(err)
	}
	spec.Features["export"].Operation.Scope = "staging"
	if catalog.Spec.Features["export"].Operation.Scope != "production" {
		t.Fatal("binding aliases caller memory")
	}
	for _, tc := range []struct {
		name   string
		change func(*api.ProductCatalogSpec)
	}{
		{"ambiguous", func(s *api.ProductCatalogSpec) {
			f := s.Features["export"]
			f.DefinitionID = uuid.NewString()
			s.Features["export"] = f
		}},
		{"implicit environment", func(s *api.ProductCatalogSpec) { s.Features["export"].Operation.Scope = "" }},
		{"all environments", func(s *api.ProductCatalogSpec) { s.Features["export"].Operation.Scope = "__all__" }},
		{"bad operation name", func(s *api.ProductCatalogSpec) { s.Features["export"].Operation.Name = "bad/name" }},
		{"duplicate binding", func(s *api.ProductCatalogSpec) { s.Features["other"] = s.Features["export"] }},
		{"zero definition", func(s *api.ProductCatalogSpec) {
			s.Features["export"] = api.ProductFeature{Label: "Export", DefinitionID: uuid.Nil.String()}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := base()
			tc.change(&spec)
			if _, err := Compile(spec); err == nil {
				t.Fatal("invalid binding accepted")
			}
		})
	}
}
