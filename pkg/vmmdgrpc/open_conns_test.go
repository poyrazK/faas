package vmmdgrpc

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/sched/flowcount"
	"github.com/onebox-faas/faas/pkg/state"
)

type hostIPVMM struct {
	vmmStubBase
	hosts map[string]string
}

func (v hostIPVMM) SnapshotLiveHostIPs() map[string]string { return v.hosts }

type openConnsCounter struct {
	warmed int
	counts map[string]int64
}

type flowTelemetryCounter struct {
	openConnsCounter
	summaries map[string][]flowcount.FlowSummary
}

func (c *flowTelemetryCounter) Snapshot(_ context.Context, instanceID string) ([]flowcount.FlowSummary, error) {
	return append([]flowcount.FlowSummary(nil), c.summaries[instanceID]...), nil
}

func (c *openConnsCounter) Warm(_ context.Context, instances []state.Instance) error {
	c.warmed = len(instances)
	return nil
}

func (c *openConnsCounter) Open(_ context.Context, instanceID string) (int64, error) {
	return c.counts[instanceID], nil
}

func TestServerOpenConnsUsesComputeHostSnapshot(t *testing.T) {
	counter := &openConnsCounter{counts: map[string]int64{"vm-1": 3, "vm-2": 1}}
	server := &Server{
		vmm:         hostIPVMM{hosts: map[string]string{"vm-1": "10.100.0.2", "vm-2": "10.100.0.3"}},
		flowCounter: counter,
	}

	got := server.openConns(context.Background())
	if counter.warmed != 2 {
		t.Fatalf("Warm received %d instances, want 2", counter.warmed)
	}
	if got["vm-1"] != 3 || got["vm-2"] != 1 {
		t.Fatalf("open-conns = %#v, want vm-1=3 vm-2=1", got)
	}
}

// adr: 127 — the vmmd Stats seam preserves OpenConns while exposing the
// optional bounded endpoint snapshot from the same conntrack warm.
func TestServerFlowTelemetryIncludesBoundedSummaries(t *testing.T) {
	counter := &flowTelemetryCounter{
		openConnsCounter: openConnsCounter{counts: map[string]int64{"vm-1": 3}},
		summaries: map[string][]flowcount.FlowSummary{
			"vm-1": {{InstanceID: "vm-1", Protocol: "tcp", RemoteIP: "203.0.113.10", RemotePort: 443, State: "ESTABLISHED", Direction: "outbound", Count: 2}},
		},
	}
	server := &Server{
		vmm:         hostIPVMM{hosts: map[string]string{"vm-1": "10.100.0.2"}},
		flowCounter: counter,
	}

	got := server.flowTelemetry(context.Background())
	if got.openConns["vm-1"] != 3 {
		t.Fatalf("open-conns = %#v, want vm-1=3", got.openConns)
	}
	rows := got.summaries["vm-1"]
	if len(rows) != 1 || rows[0].RemoteIP != "203.0.113.10" || rows[0].Count != 2 {
		t.Fatalf("flow summaries = %#v, want one outbound summary", rows)
	}
}
