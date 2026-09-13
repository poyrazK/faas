package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	egresspb "github.com/onebox-faas/faas/api/proto/onebox/faas/egress/v1"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	fleetEgressPort              = "9092"
	fleetEgressReconcileInterval = 10 * time.Second
	fleetEgressRetiredTTL        = 2 * time.Hour
)

type activeComputeNodeSource interface {
	ActiveComputeNodes(context.Context) ([]state.ComputeNode, error)
}

type fleetEgressMetrics struct {
	registry  *prometheus.Registry
	expected  prometheus.Gauge
	connected prometheus.Gauge
	failures  *prometheus.CounterVec
}

func newFleetEgressMetrics() *fleetEgressMetrics {
	m := &fleetEgressMetrics{
		registry: prometheus.NewRegistry(),
		expected: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "meterd_fleet_egress_expected_streams",
			Help: "Active compute nodes expected to expose a gateway usage stream.",
		}),
		connected: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "meterd_fleet_egress_connected_streams",
			Help: "Active compute gateway usage streams currently connected to meterd.",
		}),
		failures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "meterd_fleet_egress_failures_total",
			Help: "Fleet gateway usage stream failures by stage.",
		}, []string{"stage"}),
	}
	m.registry.MustRegister(m.expected, m.connected, m.failures)
	return m
}

type fleetEgressEntry struct {
	nodeID    string
	target    string
	adapter   *gatewayEgressAdapter
	cancel    context.CancelFunc
	retiredAt time.Time
}

type closableGatewayEgressClient struct {
	egresspb.EgressTxServiceClient
	close func() error
}

func (c *closableGatewayEgressClient) Close() error { return c.close() }

// fleetGatewayEgressAdapter maintains one destructive usage stream per active
// compute gateway. Retired and retargeted adapters remain readable until their
// already-received buckets drain, preventing a topology change from discarding
// the tail of the previous stream.
type fleetGatewayEgressAdapter struct {
	nodes   activeComputeNodeSource
	tlsCfg  *tls.Config
	dialFn  func(context.Context, string, *tls.Config) (egresspb.EgressTxServiceClient, error)
	now     func() time.Time
	log     *slog.Logger
	metrics *fleetEgressMetrics

	mu      sync.Mutex
	active  map[string]*fleetEgressEntry
	retired []*fleetEgressEntry
}

func fleetEgressTarget(node state.ComputeNode) (string, error) {
	if node.GatewayTargetURL == nil {
		return "", fmt.Errorf("gateway target is missing")
	}
	raw := strings.TrimSpace(*node.GatewayTargetURL)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "tcp" || u.Hostname() == "" {
		return "", fmt.Errorf("invalid gateway target %q", raw)
	}
	return "tcp://" + net.JoinHostPort(u.Hostname(), fleetEgressPort), nil
}

func (a *fleetGatewayEgressAdapter) Run(ctx context.Context) {
	a.reconcile(ctx)
	ticker := time.NewTicker(fleetEgressReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			a.stop()
			return
		case <-ticker.C:
			a.reconcile(ctx)
		}
	}
}

func (a *fleetGatewayEgressAdapter) reconcile(ctx context.Context) {
	nodes, err := a.nodes.ActiveComputeNodes(ctx)
	if err != nil {
		a.metrics.failures.WithLabelValues("discover").Inc()
		if a.log != nil {
			a.log.Warn("meterd: fleet egress discovery failed", "err", err)
		}
		return
	}
	targets := make(map[string]string, len(nodes))
	for _, node := range nodes {
		target, targetErr := fleetEgressTarget(node)
		if targetErr != nil {
			a.metrics.failures.WithLabelValues("target").Inc()
			continue
		}
		targets[node.ID] = target
	}
	a.metrics.expected.Set(float64(len(targets)))

	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	for nodeID, entry := range a.active {
		target, present := targets[nodeID]
		if present && target == entry.target {
			delete(targets, nodeID)
			continue
		}
		entry.cancel()
		entry.retiredAt = now
		a.retired = append(a.retired, entry)
		delete(a.active, nodeID)
	}
	for nodeID, target := range targets {
		streamCtx, cancel := context.WithCancel(ctx)
		adapter := &gatewayEgressAdapter{
			now:    a.now,
			tlsCfg: tlsForService(a.tlsCfg, "egress.faas"),
			data:   make(map[string]map[int64]gatewayUsageBucket),
			dialFn: a.dialFn,
		}
		entry := &fleetEgressEntry{nodeID: nodeID, target: target, adapter: adapter, cancel: cancel}
		a.active[nodeID] = entry
		adapter.startStream(streamCtx, target, a.log)
	}
	kept := a.retired[:0]
	for _, entry := range a.retired {
		if entry.adapter.Tracked() > 0 && now.Sub(entry.retiredAt) < fleetEgressRetiredTTL {
			kept = append(kept, entry)
		}
	}
	a.retired = kept
	a.updateConnectedLocked()
}

func (a *fleetGatewayEgressAdapter) updateConnectedLocked() {
	connected := 0
	for _, entry := range a.active {
		if entry.adapter.connected.Load() {
			connected++
		}
	}
	a.metrics.connected.Set(float64(connected))
}

func (a *fleetGatewayEgressAdapter) ReadUsageDeltas(instanceID string) (meter.UsageDeltas, bool) {
	if a == nil || instanceID == "" {
		return meter.UsageDeltas{}, false
	}
	a.mu.Lock()
	adapters := make([]*gatewayEgressAdapter, 0, len(a.active)+len(a.retired))
	for _, entry := range a.active {
		adapters = append(adapters, entry.adapter)
	}
	for _, entry := range a.retired {
		adapters = append(adapters, entry.adapter)
	}
	a.mu.Unlock()
	var out meter.UsageDeltas
	found := false
	for _, adapter := range adapters {
		delta, ok := adapter.ReadUsageDeltas(instanceID)
		if !ok {
			continue
		}
		found = true
		out.TXBytes += delta.TXBytes
		out.Requests += delta.Requests
		out.ColdBootCount += delta.ColdBootCount
	}
	return out, found
}

func (a *fleetGatewayEgressAdapter) stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, entry := range a.active {
		entry.cancel()
	}
	for _, entry := range a.retired {
		entry.cancel()
	}
	clear(a.active)
	a.retired = nil
	a.metrics.connected.Set(0)
}
