// adr: 20261101215228

package runtimeconfig

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRequestConcurrencyApply(t *testing.T) {
	t.Cleanup(func() { api.SetRequestConcurrency(api.RequestConcurrency{}) })
	var r RequestConcurrency
	for _, step := range []struct {
		key, value       string
		handled, wantErr bool
	}{
		{KeyAppRequestConcurrencyDefault, "80", true, false},
		{KeyAppRequestConcurrencyMax, "1000", true, false},
		{KeyAppRequestConcurrencyMax, "10001", true, true},
		{KeyAppRequestConcurrencyDefault, "-1", true, true},
		{KeyAppRequestConcurrencyDefault, `"80"`, true, true},
		{KeyHSTS, "true", false, false},
	} {
		handled, err := r.Apply(step.key, json.RawMessage(step.value))
		if handled != step.handled || (err != nil) != step.wantErr {
			t.Errorf("Apply(%s, %s) = %v, %v; want handled %v, error %v", step.key, step.value, handled, err, step.handled, step.wantErr)
		}
	}
	if got := api.CurrentRequestConcurrency(); got != (api.RequestConcurrency{Default: 80, Max: 1000}) {
		t.Fatalf("applied = %+v, want default 80, max 1000", got)
	}
	if _, err := r.Apply(KeyAppRequestConcurrencyMax, json.RawMessage("0")); err != nil {
		t.Fatal(err)
	}
	if got := api.CurrentRequestConcurrency(); got != (api.RequestConcurrency{Default: 80}) {
		t.Errorf("max unset again = %+v, want default 80 only", got)
	}
}
