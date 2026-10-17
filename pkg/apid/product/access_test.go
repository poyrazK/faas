// ADR-1598: policy-only updates must keep the resulting catalog within bounds.
package product

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductAccessMergedCatalogBound(t *testing.T) {
	spec := api.ProductCatalogSpec{Currency: "EUR", Features: map[string]api.ProductFeature{}, Plans: map[string]api.ProductPlan{}}
	for i := range api.ProductControlMaxFeatures {
		name := fmt.Sprintf("feature-%02d-%s", i, strings.Repeat("a", 50))
		spec.Features[name] = api.ProductFeature{Label: "Export", Operation: &api.ProductOperationBinding{Name: fmt.Sprintf("export-%d", i), Scope: api.DefaultEnvScope}}
	}
	for i := range api.ProductControlMaxPlans {
		name := fmt.Sprintf("plan-%d", i)
		features := map[string]api.ProductFeatureGrant{}
		for feature := range spec.Features {
			features[feature] = api.ProductFeatureGrant{MonthlyStarts: 1000000, UnitPriceMillicents: 1000000000}
		}
		spec.Plans[name] = api.ProductPlan{Label: "Plan", Features: features}
		raw, err := json.Marshal(spec)
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) > api.ProductControlMaxCatalogBytes {
			delete(spec.Plans, name)
			break
		}
	}
	// Fill the remaining space using otherwise valid labels, leaving a small
	// margin. The stored base fits, but the policy-only mutation can overflow.
	for name, feature := range spec.Features {
		for len(feature.Label) < api.ProductControlMaxLabelBytes {
			feature.Label += "x"
			spec.Features[name] = feature
			raw, err := json.Marshal(spec)
			if err != nil {
				t.Fatal(err)
			}
			if len(raw) > api.ProductControlMaxCatalogBytes-100 {
				feature.Label = feature.Label[:len(feature.Label)-1]
				spec.Features[name] = feature
				break
			}
		}
	}
	base, err := productcontrol.Compile(spec)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(base.Spec)
	if err != nil || len(raw) > api.ProductControlMaxCatalogBytes {
		t.Fatal("invalid base catalog fixture", err)
	}
	policy := productcontrol.DefaultAccess()
	for i := range api.ProductControlAccessMaxRoles - 3 {
		policy.Roles[fmt.Sprintf("custom-%d", i)] = []api.ProductPermission{api.ProductPermissionRunWork, api.ProductPermissionViewBilling}
	}
	if _, err := previewAccess(base, policy); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("oversized merged policy catalog accepted: %v (base %d bytes)", err, len(raw))
	}
}
