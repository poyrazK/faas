// adr: 20261101215228
// adr: 20261109172421

package apidserver_test

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
)

// The CPU shape keys are retired (ADR-20261109172421): read-only, and a value
// already stored still validates, so apid's reconcile keeps applying it.
func TestRuntimeConfigCPUShapeKeysAreRetired(t *testing.T) {
	m := apidserver.NewRuntimeConfigManager(func(string) string { return "" })
	for _, key := range []string{runtimeconfig.KeyAppCPUBaselineMillicoresPerGB, runtimeconfig.KeyAppCPUCeilingFloorMillicores,
		runtimeconfig.KeyGuestVCPUMillicores, runtimeconfig.KeyGuestVCPUFromCPUApps, runtimeconfig.KeyCPUSizeApps} {
		def, ok := m.Definition(key)
		if !ok || def.Mutable || def.ControllerEnabled || def.Category != "Retired" {
			t.Errorf("%s: defined %v, mutable %v, controller %v, category %q; want a read-only retired entry", key, ok, def.Mutable, def.ControllerEnabled, def.Category)
		}
	}
	if err := m.Apply(runtimeconfig.KeyCPUSizeApps, json.RawMessage(`"*"`)); err != nil {
		t.Errorf("the stored cpu_size_apps value no longer validates: %v", err)
	}
}

func TestRuntimeConfigCPUShapeKeysCheckTheirBounds(t *testing.T) {
	m := apidserver.NewRuntimeConfigManager(func(string) string { return "" })
	cases := []struct {
		key, value string
		ok         bool
	}{
		{runtimeconfig.KeyAppCPUBaselineMillicoresPerGB, "500", true},
		{runtimeconfig.KeyAppCPUBaselineMillicoresPerGB, "124", false},
		{runtimeconfig.KeyAppCPUBaselineMillicoresPerGB, "2001", false},
		{runtimeconfig.KeyAppCPUCeilingFloorMillicores, "500", true},
		{runtimeconfig.KeyAppCPUCeilingFloorMillicores, "2000", true},
		{runtimeconfig.KeyAppCPUCeilingFloorMillicores, "750", false},
		{runtimeconfig.KeyAppCPUCeilingFloorMillicores, "3000", false},
		{runtimeconfig.KeyGuestVCPUMillicores, "500", true},
		{runtimeconfig.KeyGuestVCPUMillicores, "400", false},
		{runtimeconfig.KeyGuestVCPUFromCPUApps, `""`, true},
		{runtimeconfig.KeyGuestVCPUFromCPUApps, `"*"`, true},
		{runtimeconfig.KeyGuestVCPUFromCPUApps, `"0b6f8c1e-2f6a-4c55-9d38-3c1f0c5e7a11, 7e0d4a52-91b3-4f7e-8a3c-5d2e9b1f6c44"`, true},
		{runtimeconfig.KeyGuestVCPUFromCPUApps, `"my-app"`, false},
	}
	for _, c := range cases {
		err := m.Apply(c.key, json.RawMessage(c.value))
		if (err == nil) != c.ok {
			t.Errorf("Apply(%s, %s) = %v, want ok %v", c.key, c.value, err, c.ok)
		}
	}
	// Each default is the compiled behavior.
	for _, key := range []string{runtimeconfig.KeyAppCPUBaselineMillicoresPerGB, runtimeconfig.KeyAppCPUCeilingFloorMillicores, runtimeconfig.KeyGuestVCPUMillicores} {
		def, ok := m.Definition(key)
		if !ok || string(def.Default) != "1000" {
			t.Errorf("%s default = %s (defined %v), want 1000", key, def.Default, ok)
		}
	}
}

func TestRuntimeConfigRequestKeysCheckTheirBounds(t *testing.T) {
	t.Cleanup(func() { api.SetRequestConcurrency(api.RequestConcurrency{}) })
	m := apidserver.NewRuntimeConfigManager(func(string) string { return "" })
	for _, c := range []struct {
		key, value string
		ok         bool
	}{
		{runtimeconfig.KeyAppRequestConcurrencyDefault, "0", true},
		{runtimeconfig.KeyAppRequestConcurrencyDefault, "80", true},
		{runtimeconfig.KeyAppRequestConcurrencyMax, "1000", true},
		{runtimeconfig.KeyAppRequestConcurrencyMax, "10001", false},
		{runtimeconfig.KeyAppRequestConcurrencyMax, "-1", false},
	} {
		if err := m.Apply(c.key, json.RawMessage(c.value)); (err == nil) != c.ok {
			t.Errorf("Apply(%s, %s) = %v, want ok %v", c.key, c.value, err, c.ok)
		}
	}
	// apid resolves declarations against the applied maximum.
	if got := api.PlanFree.RequestConcurrencyBound(); got != 1000 {
		t.Errorf("Free bound after apply = %d, want 1000", got)
	}
}
