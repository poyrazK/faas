package main

import (
	"context"
	"crypto/tls"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

const fleetStatsRPCTimeout = 5 * time.Second

func tlsForService(cfg *tls.Config, serverName string) *tls.Config {
	if cfg == nil {
		return nil
	}
	clone := cfg.Clone()
	clone.ServerName = serverName
	return clone
}

// fleetNodeSource is deliberately narrower than state.Store. Meterd only
// needs active-node discovery and the physical instance view to fan telemetry
// in and expose gaps; tests can therefore model a split fleet without a DB.
type fleetNodeSource interface {
	ActiveComputeNodes(context.Context) ([]state.ComputeNode, error)
	ListInstancesOnNodeID(context.Context, string) ([]state.Instance, error)
}

type fleetStatsMetrics struct {
	registry  *prometheus.Registry
	expected  prometheus.Gauge
	connected prometheus.Gauge
	missing   prometheus.Gauge
	failures  *prometheus.CounterVec
}

func newFleetStatsMetrics() *fleetStatsMetrics {
	m := &fleetStatsMetrics{
		registry: prometheus.NewRegistry(),
		expected: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "meterd_fleet_stats_expected_nodes",
			Help: "Active compute nodes with a configured schedd telemetry target.",
		}),
		connected: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "meterd_fleet_stats_connected_nodes",
			Help: "Compute schedd targets that returned an instance telemetry snapshot on the last refresh.",
		}),
		missing: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "meterd_fleet_stats_missing_instances",
			Help: "Live physical instances absent from their compute schedd telemetry snapshot.",
		}),
		failures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "meterd_fleet_stats_failures_total",
			Help: "Fleet schedd telemetry failures by stage.",
		}, []string{"stage"}),
	}
	m.registry.MustRegister(m.expected, m.connected, m.missing, m.failures)
	return m
}

type fleetStatsResult struct {
	nodeID string
	rows   []scheddgrpc.InstanceStatsRow
	err    error
}

// fleetStatsParker preserves the control-plane schedd as the destination for
// automated ParkInstance calls while fanning ListInstanceStats out to every
// active compute schedd. Per-node snapshots survive transient reconnects so
// the cumulative CPU/network counters keep their durable DB baseline.
type fleetStatsParker struct {
	nodes    fleetNodeSource
	fallback parkInstanceParker
	dial     func(context.Context, string, *tls.Config) (parkInstanceParker, error)
	tlsCfg   *tls.Config
	now      func() time.Time
	log      *slog.Logger
	metrics  *fleetStatsMetrics

	mu        sync.Mutex
	snapshots map[string][]scheddgrpc.InstanceStatsRow
}

func (p *fleetStatsParker) ParkInstance(ctx context.Context, instanceID, reason, traceID string) error {
	return p.fallback.ParkInstance(ctx, instanceID, reason, traceID)
}

func (p *fleetStatsParker) ListInstanceStats(ctx context.Context) ([]scheddgrpc.InstanceStatsRow, error) {
	nodes, err := p.nodes.ActiveComputeNodes(ctx)
	if err != nil {
		p.metrics.failures.WithLabelValues("discover").Inc()
		// A DB discovery outage must not turn a previously working single-box
		// or control-plane feed into zeros.
		return p.fallback.ListInstanceStats(ctx)
	}

	active := make(map[string]state.ComputeNode, len(nodes))
	for _, node := range nodes {
		if node.ScheddTargetURL == nil || strings.TrimSpace(*node.ScheddTargetURL) == "" {
			continue
		}
		active[node.ID] = node
	}
	p.metrics.expected.Set(float64(len(active)))
	if len(active) == 0 {
		return p.fallback.ListInstanceStats(ctx)
	}

	results := make(chan fleetStatsResult, len(active))
	var wg sync.WaitGroup
	for _, node := range active {
		node := node
		wg.Add(1)
		go func() {
			defer wg.Done()
			callCtx, cancel := context.WithTimeout(ctx, fleetStatsRPCTimeout)
			defer cancel()
			client, err := p.dial(callCtx, strings.TrimSpace(*node.ScheddTargetURL), tlsForService(p.tlsCfg, "schedd.faas"))
			if err != nil {
				results <- fleetStatsResult{nodeID: node.ID, err: err}
				return
			}
			if closer, ok := client.(interface{ Close() error }); ok {
				defer closer.Close()
			}
			rows, err := client.ListInstanceStats(callCtx)
			results <- fleetStatsResult{nodeID: node.ID, rows: rows, err: err}
		}()
	}
	wg.Wait()
	close(results)

	connected := 0
	validByNode := make(map[string]map[string]struct{}, len(active))
	p.mu.Lock()
	for nodeID := range p.snapshots {
		if _, ok := active[nodeID]; !ok {
			delete(p.snapshots, nodeID)
		}
	}
	for result := range results {
		if result.err != nil {
			p.metrics.failures.WithLabelValues("rpc").Inc()
			if p.log != nil {
				p.log.Warn("meterd: compute schedd stats refresh failed", "node_id", result.nodeID, "err", result.err)
			}
			continue
		}
		connected++
		valid := make([]scheddgrpc.InstanceStatsRow, 0, len(result.rows))
		ids := make(map[string]struct{}, len(result.rows))
		for _, row := range result.rows {
			if uuid.Validate(row.InstanceID) != nil || (row.NodeID != "" && row.NodeID != result.nodeID) {
				p.metrics.failures.WithLabelValues("invalid_row").Inc()
				continue
			}
			valid = append(valid, row)
			ids[row.InstanceID] = struct{}{}
		}
		p.snapshots[result.nodeID] = valid
		validByNode[result.nodeID] = ids
	}
	combined := make([]scheddgrpc.InstanceStatsRow, 0)
	for nodeID, rows := range p.snapshots {
		combined = append(combined, rows...)
		if _, ok := validByNode[nodeID]; !ok {
			ids := make(map[string]struct{}, len(rows))
			for _, row := range rows {
				ids[row.InstanceID] = struct{}{}
			}
			validByNode[nodeID] = ids
		}
	}
	p.mu.Unlock()
	p.metrics.connected.Set(float64(connected))

	missing := 0
	for nodeID := range active {
		instances, listErr := p.nodes.ListInstancesOnNodeID(ctx, nodeID)
		if listErr != nil {
			p.metrics.failures.WithLabelValues("instances").Inc()
			continue
		}
		ids := validByNode[nodeID]
		for _, instance := range instances {
			switch instance.State {
			case "running", "waking", "cold_booting":
				if _, ok := ids[instance.ID]; !ok {
					missing++
				}
			}
		}
	}
	p.metrics.missing.Set(float64(missing))
	return combined, nil
}
