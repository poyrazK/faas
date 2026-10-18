package main

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/productcap"
)

func TestRenderOmitsInternalCapabilities(t *testing.T) {
	catalog := productcap.Catalog{Capabilities: []productcap.Capability{{
		ID: "private-feature", Name: "Private feature", Category: "private-category",
		Description: "Private description", Maturity: productcap.MaturityInternal,
		Plans: []string{"private-plan"}, DocsURL: "/docs/ops/private-feature",
		OperatorFlag: "FAAS_PRIVATE_FEATURE", AcceptanceTest: "TestPrivateFeature",
	}}}
	for _, maturity := range []productcap.Maturity{productcap.MaturityPreview, productcap.MaturityBeta, productcap.MaturityGA} {
		catalog.Capabilities = append(catalog.Capabilities, productcap.Capability{
			Name: string(maturity), Maturity: maturity, Plans: []string{"hobby", "pro"},
			DocsURL: "/docs/" + string(maturity),
		})
	}
	out := render(catalog)
	for _, private := range []string{"private-feature", "Private feature", "private-category", "Private description", "private-plan", "FAAS_PRIVATE_FEATURE", "TestPrivateFeature"} {
		if strings.Contains(out, private) {
			t.Errorf("customer matrix exposes internal field %q", private)
		}
	}
	for _, maturity := range []string{"preview", "beta", "ga"} {
		if !strings.Contains(out, "["+maturity+"](../docs/"+maturity+")") {
			t.Errorf("customer matrix omits %s capability", maturity)
		}
	}
	if !strings.Contains(out, "`hobby`, `pro`") {
		t.Error("customer plan entitlements are missing")
	}
}

func TestRenderCatalogKeepsProductControlPrivate(t *testing.T) {
	catalog, err := productcap.Load()
	if err != nil {
		t.Fatal(err)
	}
	out := render(catalog)
	for _, private := range []string{"Gregale Product Control", "product-control", "FAAS_PRODUCT_CONTROL_ENABLED"} {
		if strings.Contains(out, private) {
			t.Errorf("customer matrix exposes %q", private)
		}
	}
	for _, capability := range catalog.Capabilities {
		listed := strings.Contains(out, "["+capability.Name+"](")
		if listed != (capability.Maturity != productcap.MaturityInternal) {
			t.Errorf("%s visibility does not match its maturity %s", capability.ID, capability.Maturity)
		}
	}
}
