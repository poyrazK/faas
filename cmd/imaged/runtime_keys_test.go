// adr: 20261101215228
package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/capflags"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
)

// imaged applies the request-concurrency keys next to the platform flags, so
// the Postgres pool size it stamps follows the per-instance limit the
// gateways and schedd enforce.
func TestRuntimeKeysApplyRequestConcurrency(t *testing.T) {
	t.Cleanup(func() { api.SetRequestConcurrency(api.RequestConcurrency{}) })
	flags := platformflags.NewSet(capflags.Bootstrap(func(string) string { return "" }))
	keys, apply := runtimeKeys(flags, &runtimeconfig.RequestConcurrency{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, key := range runtimeconfig.RequestConcurrencyKeys {
		if !slices.Contains(keys, key) {
			t.Fatalf("keys %v miss %s", keys, key)
		}
	}
	for key, value := range map[string]string{
		runtimeconfig.KeyAppRequestConcurrencyDefault: "80",
		runtimeconfig.KeyAppRequestConcurrencyMax:     "1000",
	} {
		if err := apply(context.Background(), key, json.RawMessage(value), 1); err != nil {
			t.Fatalf("apply %s: %v", key, err)
		}
	}
	if got := api.CurrentRequestConcurrency(); got != (api.RequestConcurrency{Default: 80, Max: 1000}) {
		t.Fatalf("applied %+v, want default 80 and max 1000", got)
	}
	if got := api.MustLimitsFor(api.PlanFree).PostgresPoolMax(0); got != api.PostgresPoolMaxPerInstance {
		t.Fatalf("Free pool = %d, want %d", got, api.PostgresPoolMaxPerInstance)
	}
	if err := apply(context.Background(), runtimeconfig.KeyAppRequestConcurrencyMax, json.RawMessage(`"x"`), 2); err == nil {
		t.Fatal("a non-integer maximum applied")
	}
}
