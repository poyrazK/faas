// adr: 459 — environment intent and runtime ownership contracts.
package targets

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestQueueDemandAggregatesSameNameAcrossEnvironmentBindings(t *testing.T) {
	signal := queueDepthSignal{bindings: []queueBindingDepth{
		{binding: state.QueueBinding{QueueName: "orders", DeploymentScope: "production", Enabled: true, MaxConcurrency: 2}, queue: state.QueueStats{Depth: 20}},
		{binding: state.QueueBinding{QueueName: "orders", DeploymentScope: "staging", Enabled: true, MaxConcurrency: 1}, queue: state.QueueStats{Depth: 5}},
		{binding: state.QueueBinding{QueueName: "orders", DeploymentScope: "preview", Enabled: false, MaxConcurrency: 1}, queue: state.QueueStats{Depth: 50}},
	}}
	if demand := signal.bindingWorkerDemand(5); demand["orders"] != 3 {
		t.Fatalf("aggregate lost same-name scoped binding demand: %+v", demand)
	}
}
