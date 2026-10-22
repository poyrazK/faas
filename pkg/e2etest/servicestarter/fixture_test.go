package servicestarter

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

// Exercise production definition validation for every revision the native
// journey submits, including its empty retained retirement graph.
func TestReviewedServiceFixtureRevisionsCompile(t *testing.T) {
	d := Definition("binding-native-service", "binding-native-caller", "binding-native-billing", "registry.invalid/caller@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "registry.invalid/billing@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	d = WithWorker(d, "binding-native-worker", "registry.invalid/worker@sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd")
	initial, err := environmentsync.Compile(d)
	if err != nil {
		t.Fatal(err)
	}
	caller := initial.Definition.Workloads["caller"]
	var runtime api.AppManifest
	if json.Unmarshal(caller.Runtime, &runtime) != nil || runtime.ServiceBindingTransport != api.ServiceBindingTransportHTTPS || caller.ServiceBindings["billing"].EnvKey != "BILLING_ENDPOINT" || caller.ServiceBindings["billing"].Workload != "billing" {
		t.Fatal("fixture lost custom managed HTTPS routing")
	}
	worker := initial.Definition.Workloads["worker"]
	if json.Unmarshal(worker.Runtime, &runtime) != nil || runtime.ExecutionMode != api.ExecutionModeWorker || runtime.ServiceBindingTransport != api.ServiceBindingTransportHTTPS || worker.ServiceBindings["billing"].EnvKey != "WORKER_BILLING_ENDPOINT" || worker.QueueBindings["orders"].Enabled == nil || !*worker.QueueBindings["orders"].Enabled || worker.QueueBindings["orders"].Mode != "push" || len(worker.QueueSmoke["orders"].Payload) == 0 || worker.Variables[api.QueueCanaryCapabilityEnv] != `["orders"]` {
		t.Fatal("reviewed worker queue or HTTPS dependency missing")
	}
	target := d.Workloads["billing"]
	target.Source.Image = "registry.invalid/billing@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	d.Workloads["billing"] = target
	next, err := environmentsync.Compile(d)
	if err != nil || next.Digest == initial.Digest {
		t.Fatal("target revision did not create a new reviewed definition")
	}
	d = WithSourceWorker(d)
	source, err := environmentsync.Compile(d)
	if err != nil || source.Digest == next.Digest || source.Definition.Workloads["worker"].Source.Kind != "dockerfile" {
		t.Fatal("source worker revision did not compile as a distinct reviewed build")
	}
	d.Workloads = map[string]api.EnvironmentWorkload{}
	d.WorkloadPruningPolicy = "retain"
	if _, err := environmentsync.Compile(d); err != nil {
		t.Fatal(err)
	}
}
