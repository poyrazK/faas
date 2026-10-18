package mcpprotocol

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
	"github.com/prometheus/client_golang/prometheus"
)

const MaxCoverageInstances = 4096
const HeartbeatStaleAfter = 2 * time.Minute
const HeartbeatStartupGrace = 3 * time.Minute

type instanceCoverage struct {
	paused                        bool
	appID, deploymentID, instance string
	started, heartbeat            time.Time
	alert                         bool
}

// CoverageCollector tracks only host-created log lifecycles. Guest fields never
// select identities. Heartbeats are self-reported, not proof of MCP readiness.
type CoverageCollector struct {
	mu                                 sync.Mutex
	instances                          map[string]*instanceCoverage
	started, heartbeat, alert, dropped *prometheus.Desc
	drops                              uint64
}

func NewCoverage(reg prometheus.Registerer) *CoverageCollector {
	labels := []string{"app_id", "deployment_id", "instance_id"}
	c := &CoverageCollector{instances: map[string]*instanceCoverage{},
		started:   prometheus.NewDesc("vmmd_mcp_telemetry_instance_started_timestamp_seconds", "Host log lifecycle start; removed when the instance log lifecycle ends.", labels, nil),
		heartbeat: prometheus.NewDesc("vmmd_mcp_telemetry_heartbeat_timestamp_seconds", "Host intake time of the last version-1 starter heartbeat, or zero before first observation.", labels, nil),
		alert:     prometheus.NewDesc("vmmd_mcp_telemetry_loss_alert_enabled", "Starter explicitly opted into operator telemetry-loss alerts.", labels, nil),
		dropped:   prometheus.NewDesc("vmmd_mcp_telemetry_capacity_dropped_total", "Instance lifecycles omitted from bounded coverage tracking.", nil, nil)}
	if reg != nil {
		reg.MustRegister(c)
	}
	return c
}

// Start returns callbacks tied to this exact lifecycle, so late writes and old
// stop callbacks cannot revive or remove a replacement with the same identity.
func (c *CoverageCollector) Start(appID, deploymentID, instance string) (func(logbuf.Line), func()) {
	noop := func(logbuf.Line) {}
	if _, err := uuid.Parse(appID); err != nil || len(instance) == 0 || len(instance) > 256 {
		return noop, func() {}
	}
	if deploymentID != "" {
		if _, err := uuid.Parse(deploymentID); err != nil {
			deploymentID = ""
		}
	}
	key := appID + "/" + instance
	row := &instanceCoverage{appID: appID, deploymentID: deploymentID, instance: instance, started: time.Now()}
	c.mu.Lock()
	if _, exists := c.instances[key]; !exists && len(c.instances) >= MaxCoverageInstances {
		c.drops++
		c.mu.Unlock()
		return noop, func() {}
	}
	c.instances[key] = row
	c.mu.Unlock()
	observe := func(line logbuf.Line) {
		if len(line.Line) > 4096 || !strings.Contains(line.Line, "mcp_telemetry_heartbeat") {
			return
		}
		var event struct {
			Event   string `json:"event"`
			Version int    `json:"version"`
			Alert   bool   `json:"loss_alert"`
		}
		if json.Unmarshal([]byte(line.Line), &event) != nil || event.Event != "mcp_telemetry_heartbeat" || event.Version != 1 {
			return
		}
		// WrittenAt is assigned by the host log ring; guest timestamps are ignored.
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.instances[key] != row {
			return
		}
		if line.WrittenAt.After(row.heartbeat) {
			row.heartbeat = line.WrittenAt
			row.alert = event.Alert
		}
	}
	stop := func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.instances[key] == row {
			delete(c.instances, key)
		}
	}
	return observe, stop
}
func (c *CoverageCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{c.started, c.heartbeat, c.alert, c.dropped} {
		ch <- d
	}
}
func (c *CoverageCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	rows := make([]instanceCoverage, 0, len(c.instances))
	for _, r := range c.instances {
		if !r.paused {
			rows = append(rows, *r)
		}
	}
	drops := c.drops
	c.mu.Unlock()
	ch <- prometheus.MustNewConstMetric(c.dropped, prometheus.CounterValue, float64(drops))
	for _, r := range rows {
		labels := []string{r.appID, r.deploymentID, r.instance}
		last := 0.0
		if !r.heartbeat.IsZero() {
			last = float64(r.heartbeat.Unix())
		}
		enabled := 0.0
		if r.alert {
			enabled = 1
		}
		ch <- prometheus.MustNewConstMetric(c.started, prometheus.GaugeValue, float64(r.started.Unix()), labels...)
		ch <- prometheus.MustNewConstMetric(c.heartbeat, prometheus.GaugeValue, last, labels...)
		ch <- prometheus.MustNewConstMetric(c.alert, prometheus.GaugeValue, enabled, labels...)
	}
}

// Pause suppresses coverage and alerts while a guest cannot execute. Resuming
// starts a new grace period without trusting the guest's wall clock.
func (c *CoverageCollector) Pause(instance string, paused bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.instances {
		if r.instance == instance && r.paused != paused {
			r.paused = paused
			if !paused {
				r.started = time.Now()
				r.heartbeat = time.Time{}
			}
		}
	}
}
