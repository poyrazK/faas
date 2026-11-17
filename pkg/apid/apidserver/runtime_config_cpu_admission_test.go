// adr: 20261109172421

package apidserver_test

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
)

// cpu_admission is retired: read-only, and a stored value still validates
// so apid's reconcile keeps applying it.
func TestRuntimeConfigCPUAdmissionIsRetired(t *testing.T) {
	m := apidserver.NewRuntimeConfigManager(func(string) string { return "" })
	for _, c := range []struct {
		value string
		ok    bool
	}{
		{`"enforce"`, true},
		{`"observe"`, true},
		{`"off"`, false},
		{`true`, false},
	} {
		if err := m.Apply(runtimeconfig.KeyCPUAdmission, json.RawMessage(c.value)); (err == nil) != c.ok {
			t.Errorf("Apply(cpu_admission, %s) = %v, want ok %v", c.value, err, c.ok)
		}
	}
	def, ok := m.Definition(runtimeconfig.KeyCPUAdmission)
	if !ok || string(def.Default) != strconv.Quote(api.DefaultCPUAdmission) {
		t.Errorf("cpu_admission default = %s (defined %v), want %q", def.Default, ok, api.DefaultCPUAdmission)
	}
	if def.Mutable || def.ControllerEnabled || def.Category != "Retired" {
		t.Errorf("cpu_admission: mutable %v, controller %v, category %q; want a read-only retired entry", def.Mutable, def.ControllerEnabled, def.Category)
	}
}
