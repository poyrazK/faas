// adr: 20261118154632

package apidserver_test

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
)

func TestRuntimeConfigBuilderSlotKeys(t *testing.T) {
	m := apidserver.NewRuntimeConfigManager(func(string) string { return "" })
	for _, key := range []string{runtimeconfig.KeyBuilderElasticSlots, runtimeconfig.KeyBuilderSlots} {
		def, ok := m.Definition(key)
		if !ok || !def.Mutable || def.Category != "Builds" {
			t.Fatalf("%s: defined %v, mutable %v, category %q; want a mutable Builds entry", key, ok, def.Mutable, def.Category)
		}
		if err := m.Apply(key, def.Default); err != nil {
			t.Fatalf("%s: the default %s does not validate: %v", key, def.Default, err)
		}
	}
	if def, _ := m.Definition(runtimeconfig.KeyBuilderElasticSlots); string(def.Default) != `"off"` {
		t.Fatalf("builder_elastic_slots default = %s, want \"off\"", def.Default)
	}
	for _, c := range []struct {
		key, value string
		ok         bool
	}{
		{runtimeconfig.KeyBuilderElasticSlots, `"on"`, true},
		{runtimeconfig.KeyBuilderElasticSlots, `"off"`, true},
		{runtimeconfig.KeyBuilderElasticSlots, `"shadow"`, false},
		{runtimeconfig.KeyBuilderElasticSlots, `true`, false},
		{runtimeconfig.KeyBuilderSlots, `{"default":{"reserved":1,"max":2},"nodes":{"gregale-compute-1":{"reserved":1,"max":8}}}`, true},
		{runtimeconfig.KeyBuilderSlots, `{"nodes":{"gregale-compute-2":{"reserved":1,"max":2}}}`, true},
		{runtimeconfig.KeyBuilderSlots, `{"default":{"reserved":1,"max":17}}`, false},
		{runtimeconfig.KeyBuilderSlots, `{"default":{"reserved":0,"max":2}}`, false},
		{runtimeconfig.KeyBuilderSlots, `{"default":{"reserved":1,"maximum":2}}`, false},
		{runtimeconfig.KeyBuilderSlots, `"1/8"`, false},
	} {
		if err := m.Apply(c.key, json.RawMessage(c.value)); (err == nil) != c.ok {
			t.Errorf("Apply(%s, %s) = %v, want ok %v", c.key, c.value, err, c.ok)
		}
	}
}
