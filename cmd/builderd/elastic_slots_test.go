package main

// adr: 20261118154632

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	builderdpkg "github.com/onebox-faas/faas/pkg/builderd"
	"github.com/onebox-faas/faas/pkg/builderslots"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
)

func TestApplyElasticSlots(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	elastic := builderdpkg.NewElasticSlots("gregale-compute-1", 32, func() (builderslots.Memory, error) { return builderslots.Memory{}, nil }, log)
	apply := func(key, value string) error {
		return applyElasticSlots(elastic, key, json.RawMessage(value), log)
	}
	if elastic.Enabled() {
		t.Fatal("elastic slots start on")
	}
	if err := apply(runtimeconfig.KeyBuilderElasticSlots, `"on"`); err != nil || !elastic.Enabled() {
		t.Fatalf("on: %v, enabled %t", err, elastic.Enabled())
	}
	if err := apply(runtimeconfig.KeyBuilderElasticSlots, `"shadow"`); err == nil || !elastic.Enabled() {
		t.Fatalf("an invalid mode must fail and keep the last value: %v, enabled %t", err, elastic.Enabled())
	}
	if err := apply(runtimeconfig.KeyBuilderSlots, `{"default":{"reserved":1,"max":2},"nodes":{"gregale-compute-1":{"reserved":1,"max":8}}}`); err != nil {
		t.Fatal(err)
	}
	if got := elastic.Applied(); got.Max != 8 || got.Reserved != 1 {
		t.Fatalf("applied %+v, want this node's 1/8", got)
	}
	if err := apply(runtimeconfig.KeyBuilderSlots, `{"default":{"reserved":1,"max":99}}`); err == nil {
		t.Fatal("an out-of-bounds builder_slots applied")
	}
	if got := elastic.Applied(); got.Max != 8 {
		t.Fatalf("a refused value changed the applied slots to %+v", got)
	}
	if err := apply(runtimeconfig.KeyBuilderElasticSlots, `"off"`); err != nil || elastic.Enabled() {
		t.Fatalf("off: %v, enabled %t", err, elastic.Enabled())
	}
}

func TestSlotCPUs(t *testing.T) {
	env := func(v string) func(string) string { return func(string) string { return v } }
	if got := slotCPUs(env("4")); got != 4 {
		t.Fatalf("override 4 = %d", got)
	}
	for _, v := range []string{"", "0", "-2", "many"} {
		if got := slotCPUs(env(v)); got < 1 {
			t.Fatalf("%q fell back to %d CPUs, want runtime.NumCPU()", v, got)
		}
	}
}
