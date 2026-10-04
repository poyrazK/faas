package main

import (
	"context"
	"crypto/tls"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
	dto "github.com/prometheus/client_model/go"
)

type fleetTestNodes struct {
	mu        sync.Mutex
	nodes     []state.ComputeNode
	instances map[string][]state.Instance
}

func (s *fleetTestNodes) ActiveComputeNodes(context.Context) ([]state.ComputeNode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]state.ComputeNode(nil), s.nodes...), nil
}

func (s *fleetTestNodes) ListInstancesOnNodeID(_ context.Context, nodeID string) ([]state.Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]state.Instance(nil), s.instances[nodeID]...), nil
}

type fleetTestParker struct {
	rows []scheddgrpc.InstanceStatsRow
	err  error
}

func (p *fleetTestParker) ParkInstance(context.Context, string, string, string) error { return nil }
func (p *fleetTestParker) ListInstanceStats(context.Context) ([]scheddgrpc.InstanceStatsRow, error) {
	return append([]scheddgrpc.InstanceStatsRow(nil), p.rows...), p.err
}

func TestFleetStatsParkerFansInAllActiveComputeNodes(t *testing.T) {
	t.Parallel()
	nodeA, nodeB := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	instA, instB := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	targetA, targetB := "tcp://compute-a:9091", "tcp://compute-b:9091"
	nodes := &fleetTestNodes{
		nodes: []state.ComputeNode{
			{ID: nodeA, Active: true, ScheddTargetURL: &targetA},
			{ID: nodeB, Active: true, ScheddTargetURL: &targetB},
		},
		instances: map[string][]state.Instance{
			nodeA: {{ID: instA, NodeID: nodeA, State: "running"}},
			nodeB: {{ID: instB, NodeID: nodeB, State: "running"}},
		},
	}
	clients := map[string]*fleetTestParker{
		targetA: {rows: []scheddgrpc.InstanceStatsRow{{InstanceID: instA, NodeID: nodeA, CPUUsageUsec: 1200, NetTxBytes: 40}}},
		targetB: {rows: []scheddgrpc.InstanceStatsRow{{InstanceID: instB, NodeID: nodeB, CPUUsageUsec: 3400, NetRxBytes: 50}}},
	}
	m := newFleetStatsMetrics()
	p := &fleetStatsParker{
		nodes: nodes, fallback: &fleetTestParker{}, metrics: m,
		snapshots: make(map[string][]scheddgrpc.InstanceStatsRow),
		dial: func(_ context.Context, target string, _ *tls.Config) (parkInstanceParker, error) {
			return clients[target], nil
		},
	}

	rows, err := p.ListInstanceStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %#v, want telemetry from both compute nodes", rows)
	}
	assertGaugeValue(t, m.registry, "meterd_fleet_stats_expected_nodes", 2)
	assertGaugeValue(t, m.registry, "meterd_fleet_stats_connected_nodes", 2)
	assertGaugeValue(t, m.registry, "meterd_fleet_stats_missing_instances", 0)

	// A transient disconnect retains B's cumulative snapshot. Replacing it
	// with an empty slice would silently reset the durable metering baseline.
	clients[targetB].err = errors.New("compute-b unavailable")
	rows, err = p.ListInstanceStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows after transient disconnect = %#v, want prior B snapshot retained", rows)
	}
	assertGaugeValue(t, m.registry, "meterd_fleet_stats_connected_nodes", 1)

	// Once B leaves the active fleet its retained snapshot must disappear.
	nodes.mu.Lock()
	nodes.nodes = nodes.nodes[:1]
	nodes.mu.Unlock()
	rows, err = p.ListInstanceStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].InstanceID != instA {
		t.Fatalf("rows after retirement = %#v, want only A", rows)
	}
}

func TestFleetStatsParkerRejectsWrongNodeAndReportsMissingLiveInstance(t *testing.T) {
	t.Parallel()
	nodeID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	instID := "11111111-1111-4111-8111-111111111111"
	target := "tcp://compute-a:9091"
	nodes := &fleetTestNodes{
		nodes: []state.ComputeNode{{ID: nodeID, Active: true, ScheddTargetURL: &target}},
		instances: map[string][]state.Instance{
			nodeID: {{ID: instID, NodeID: nodeID, State: "running"}},
		},
	}
	m := newFleetStatsMetrics()
	p := &fleetStatsParker{
		nodes: nodes, fallback: &fleetTestParker{}, metrics: m,
		snapshots: make(map[string][]scheddgrpc.InstanceStatsRow),
		dial: func(context.Context, string, *tls.Config) (parkInstanceParker, error) {
			return &fleetTestParker{rows: []scheddgrpc.InstanceStatsRow{{InstanceID: instID, NodeID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc"}}}, nil
		},
	}
	rows, err := p.ListInstanceStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("accepted cross-node row: %#v", rows)
	}
	assertGaugeValue(t, m.registry, "meterd_fleet_stats_missing_instances", 1)
}

func assertGaugeValue(t *testing.T, registry interface {
	Gather() ([]*dto.MetricFamily, error)
}, name string, want float64) {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == name {
			if got := family.GetMetric()[0].GetGauge().GetValue(); got != want {
				t.Fatalf("%s = %v, want %v", name, got, want)
			}
			return
		}
	}
	t.Fatalf("metric %s not found", name)
}

// A new fleet runs no instances, so the CPU and egress samplers never ask
// for fleet stats and meterd reported zero expected nodes. The rollout's
// metering-convergence gate (expected > 0 and connected == expected) could
// therefore never pass before the first customer wake. The periodic refresh
// keeps the gauges current with no instances at all.
func TestFleetStatsGaugesRefreshOnAnIdleFleet(t *testing.T) {
	t.Parallel()
	targetA, targetB := "tcp://compute-a:9091", "tcp://compute-b:9091"
	nodes := &fleetTestNodes{nodes: []state.ComputeNode{
		{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Active: true, ScheddTargetURL: &targetA},
		{ID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", Active: true, ScheddTargetURL: &targetB},
	}}
	m := newFleetStatsMetrics()
	p := &fleetStatsParker{
		nodes: nodes, fallback: &fleetTestParker{}, metrics: m,
		snapshots: make(map[string][]scheddgrpc.InstanceStatsRow),
		dial: func(context.Context, string, *tls.Config) (parkInstanceParker, error) {
			return &fleetTestParker{}, nil
		},
	}
	cpu := &scheddCPUAdapter{parker: p, now: time.Now}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		refreshFleetStatsPeriodically(ctx, cpu, 10*time.Millisecond)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for gaugeValue(t, m.registry, "meterd_fleet_stats_connected_nodes") != 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	assertGaugeValue(t, m.registry, "meterd_fleet_stats_expected_nodes", 2)
	assertGaugeValue(t, m.registry, "meterd_fleet_stats_connected_nodes", 2)
}

// gaugeValue returns the first sample of a gauge, or -1 while it is absent.
func gaugeValue(t *testing.T, registry interface {
	Gather() ([]*dto.MetricFamily, error)
}, name string) float64 {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == name && len(family.GetMetric()) > 0 {
			return family.GetMetric()[0].GetGauge().GetValue()
		}
	}
	return -1
}
