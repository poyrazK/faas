// adr: 20261110151628

package apidserver_test

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
)

// TestRuntimeConfigObjectInventoryQuiet: apid applies off, shadow or on and
// refuses anything else, so Postgres never reads an unapplied value as the
// mode (ADR-20261110151628 §2).
func TestRuntimeConfigObjectInventoryQuiet(t *testing.T) {
	m := apidserver.NewRuntimeConfigManager(func(string) string { return "" })
	for _, c := range []struct {
		value string
		ok    bool
	}{
		{`"off"`, true}, {`"shadow"`, true}, {`"on"`, true}, {`"yes"`, false}, {`true`, false}, {`""`, false},
	} {
		if err := m.Apply(runtimeconfig.KeyObjectInventoryQuiet, json.RawMessage(c.value)); (err == nil) != c.ok {
			t.Errorf("Apply(%s) = %v, want ok %v", c.value, err, c.ok)
		}
	}
	def, ok := m.Definition(runtimeconfig.KeyObjectInventoryQuiet)
	if want := strconv.Quote(api.DefaultObjectInventoryQuiet); !ok || string(def.Default) != want || !def.Mutable || !def.ControllerEnabled {
		t.Errorf("definition %+v (defined %v), want default %s, mutable, applied by apid", def, ok, want)
	}
}
