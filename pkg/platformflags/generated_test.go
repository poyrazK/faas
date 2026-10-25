package platformflags

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/productcap"
)

func TestEveryCapabilityHasExactlyOneFlag(t *testing.T) {
	products, err := productcap.Load()
	if err != nil {
		t.Fatal(err)
	}
	owners := map[string][]string{}
	for _, spec := range Catalog() {
		if spec.Capability != "" {
			owners[spec.Capability] = append(owners[spec.Capability], spec.Key)
		}
	}
	for _, capability := range products.Capabilities {
		if capability.AlwaysOn {
			if keys := owners[capability.ID]; len(keys) != 0 {
				t.Errorf("always-on capability %q has flags %v, want none", capability.ID, keys)
			}
			continue
		}
		if keys := owners[capability.ID]; len(keys) != 1 {
			t.Errorf("capability %q is released by %d flags %v, want exactly one", capability.ID, len(keys), keys)
		}
		key, ok := CapabilityFlag(capability.ID)
		if !ok {
			t.Errorf("capability %q has no flag", capability.ID)
			continue
		}
		spec, _ := Lookup(key)
		if spec.Generated && spec.Core != capability.Core {
			t.Errorf("flag %q core = %v, catalog says %v", key, spec.Core, capability.Core)
		}
		if spec.Parent != capability.Parent {
			t.Errorf("flag %q parent = %q, catalog says %q", key, spec.Parent, capability.Parent)
		}
	}
}

func TestGeneratedFlagDefaultsFollowTier(t *testing.T) {
	tests := []struct {
		maturity productcap.Maturity
		want     Definition
	}{
		{productcap.MaturityGA, Definition{Enabled: true, Default: true}},
		{productcap.MaturityBeta, Definition{Enabled: true, Default: true}},
		{productcap.MaturityPreview, Definition{Enabled: true, Default: false}},
		{productcap.MaturityInternal, Definition{Enabled: true, Default: false}},
	}
	for _, tc := range tests {
		got := TierDefault(tc.maturity)
		if got.Enabled != tc.want.Enabled || got.Default != tc.want.Default {
			t.Errorf("TierDefault(%s) = %+v, want %+v", tc.maturity, got, tc.want)
		}
	}
	products, err := productcap.Load()
	if err != nil {
		t.Fatal(err)
	}
	set := NewSet(nil)
	subject := Subject{AccountID: "11111111-1111-4111-8111-111111111111", Plan: "pro"}
	for _, capability := range products.Capabilities {
		key, _ := CapabilityFlag(capability.ID)
		if spec, _ := Lookup(key); !spec.Generated {
			continue
		}
		want := capability.Maturity == productcap.MaturityGA || capability.Maturity == productcap.MaturityBeta
		if got := set.Enabled(key, subject); got != want {
			t.Errorf("%s (%s) enabled by default = %v, want %v", capability.ID, capability.Maturity, got, want)
		}
	}
}
