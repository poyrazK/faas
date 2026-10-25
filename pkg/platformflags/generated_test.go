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

// ungatedGeneratedFlags are capabilities whose generated flag gates nothing
// by design, with the reason (ADR-20261023115741).
var ungatedGeneratedFlags = map[string]string{
	"framework-profiles": "detection runs inside the CLI's deploy and init flows; there is no request or daemon decision to gate",
	"frozen-tier":        "operator performance switch (FAAS_FROZEN_TIER) with no customer-visible behaviour to release",
}

// TestEveryGeneratedFlagGatesSomething fails when a capability's generated
// flag decides nothing: no route, setting, daemon or CLI command consults it,
// so turning it off would hide nothing. Give the capability an owner in
// pkg/productcap/catalog.json, a hand-written flag its handlers check, or an
// entry above with the reason.
func TestEveryGeneratedFlagGatesSomething(t *testing.T) {
	products, err := productcap.Load()
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, capability := range products.Capabilities {
		ids[capability.ID] = true
		if capability.AlwaysOn {
			continue
		}
		key, ok := CapabilityFlag(capability.ID)
		if !ok {
			continue // TestEveryCapabilityHasExactlyOneFlag reports it
		}
		if spec, _ := Lookup(key); !spec.Generated {
			continue // a hand-written flag is checked by its own handlers
		}
		gates := len(capability.Routes) > 0 || len(capability.Settings) > 0 || len(capability.Consumers) > 0 || len(capability.CLI) > 0
		_, exempt := ungatedGeneratedFlags[capability.ID]
		switch {
		case !gates && !exempt:
			t.Errorf("capability %q: its generated flag %q gates no route, setting, daemon or CLI command", capability.ID, key)
		case gates && exempt:
			t.Errorf("capability %q gates something now; drop it from ungatedGeneratedFlags", capability.ID)
		}
	}
	for id := range ungatedGeneratedFlags {
		if !ids[id] {
			t.Errorf("ungatedGeneratedFlags names %q, which the catalog does not have", id)
		}
	}
}
