// adr: 1430

package object

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

// apid bills only the published object-storage rate card.
func TestObjectStoragePricingMustBeThePublishedRateCard(t *testing.T) {
	if err := ValidateObjectStoragePricing(nil); err != nil {
		t.Fatal("no registry rejected", err)
	}
	registry := &objectstorage.Registry{}
	if err := ValidateObjectStoragePricing(registry); err != nil {
		t.Fatal("unpriced registry rejected", err)
	}
	published := api.PublishedObjectStoragePricing()
	registry.Pricing = &published
	if err := ValidateObjectStoragePricing(registry); err != nil {
		t.Fatal("published rate card rejected", err)
	}
	for _, tt := range []struct {
		name   string
		mutate func(*api.ObjectStoragePricing)
	}{
		{"currency", func(p *api.ObjectStoragePricing) { p.Currency = "EUR" }},
		{"storage", func(p *api.ObjectStoragePricing) { p.StorageMillicentsPerGiBMonth = 30_000 }},
		{"requests", func(p *api.ObjectStoragePricing) { p.RequestsMillicentsPerMillion = 400 }},
		{"egress", func(p *api.ObjectStoragePricing) { p.EgressMillicentsPerGiB = 9_000 }},
	} {
		pricing := api.PublishedObjectStoragePricing()
		tt.mutate(&pricing)
		registry.Pricing = &pricing
		if err := ValidateObjectStoragePricing(registry); err == nil || !strings.Contains(err.Error(), "published rate card") {
			t.Fatalf("%s: off-card pricing error = %v", tt.name, err)
		}
	}
}
