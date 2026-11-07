// adr: 20261106214916

package apidserver_test

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
)

func TestRuntimeConfigSnapshotRefreshKeysCheckTheirValues(t *testing.T) {
	m := apidserver.NewRuntimeConfigManager(func(string) string { return "" })
	for _, c := range []struct {
		key, value string
		ok         bool
	}{
		{runtimeconfig.KeySnapshotRefresh, `"off"`, true},
		{runtimeconfig.KeySnapshotRefresh, `"shadow"`, true},
		// on is accepted once schedd can perform a refresh.
		{runtimeconfig.KeySnapshotRefresh, `"on"`, false},
		{runtimeconfig.KeySnapshotRefresh, `"yes"`, false},
		{runtimeconfig.KeySnapshotRefresh, `true`, false},
		{runtimeconfig.KeySnapshotRefreshWindowHours, "1", true},
		{runtimeconfig.KeySnapshotRefreshWindowHours, "720", true},
		{runtimeconfig.KeySnapshotRefreshWindowHours, "721", false},
		{runtimeconfig.KeySnapshotRefreshWindowHours, "0", false},
	} {
		err := m.Apply(c.key, json.RawMessage(c.value))
		if (err == nil) != c.ok {
			t.Errorf("Apply(%s, %s) = %v, want ok %v", c.key, c.value, err, c.ok)
		}
	}
	// Each default is the compiled value.
	for key, want := range map[string]string{
		runtimeconfig.KeySnapshotRefresh:            strconv.Quote(api.DefaultSnapshotRefresh),
		runtimeconfig.KeySnapshotRefreshWindowHours: strconv.Itoa(api.DefaultSnapshotRefreshWindowHours),
	} {
		def, ok := m.Definition(key)
		if !ok || string(def.Default) != want {
			t.Errorf("%s default = %s (defined %v), want %s", key, def.Default, ok, want)
		}
	}
}
