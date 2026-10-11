package environmentsync

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCompileQueueRetentionAndPinnedRecovery(t *testing.T) {
	d := definition()
	w := d.Workloads["api"]
	w.QueueBindings = map[string]api.EnvironmentQueueBinding{"orders": {QueueName: "orders", WorkloadClass: "worker"}}
	w.QueueSmoke = map[string]api.EnvironmentQueueSmoke{"orders": {Payload: []byte(`{"id":"qualification"}`)}}
	d.Workloads["api"] = w
	baseline, err := Compile(d)
	if err != nil {
		t.Fatal(err)
	}
	d.QueuePruningPolicy = "retain"
	w.QueueRecoveries = map[string]string{"orders": "11111111-2222-4333-8444-555555555555"}
	d.Workloads["api"] = w
	desired, err := Compile(d)
	if err != nil || desired.Digest == baseline.Digest || len(desired.Fields) != len(baseline.Fields) {
		t.Fatalf("review metadata lost its identity or invented ownership: %+v %v", desired, err)
	}
	for i, field := range desired.Fields {
		if field.Key() != baseline.Fields[i].Key() || string(field.Value) != string(baseline.Fields[i].Value) {
			t.Fatalf("recovery metadata changed managed queue values: %+v", field)
		}
	}
	w.QueueRecoveries["orders"] = "11111111222243338444555555555555"
	d.Workloads["api"] = w
	compact, err := Compile(d)
	if err != nil || compact.Digest != desired.Digest || compact.Definition.Workloads["api"].QueueRecoveries["orders"] != "11111111-2222-4333-8444-555555555555" {
		t.Fatalf("copied compact UUID did not normalize to reviewed identity: %+v %v", compact, err)
	}
	for _, mutate := range []func(*api.EnvironmentDefinition){
		func(d *api.EnvironmentDefinition) { d.QueuePruningPolicy = "delete" },
		func(d *api.EnvironmentDefinition) { d.Workloads["api"].QueueRecoveries["orders"] = "not-a-uuid" },
		func(d *api.EnvironmentDefinition) {
			d.Workloads["api"].QueueRecoveries["orders"] = "00000000-0000-0000-0000-000000000000"
		},
		func(d *api.EnvironmentDefinition) {
			d.Workloads["api"].QueueRecoveries["missing"] = "11111111-2222-4333-8444-555555555555"
		},
	} {
		copy, err := Compile(desired.Definition)
		if err != nil {
			t.Fatal(err)
		}
		mutate(&copy.Definition)
		if _, err := Compile(copy.Definition); err == nil {
			t.Fatal("invalid retention/recovery was accepted")
		}
	}
}
