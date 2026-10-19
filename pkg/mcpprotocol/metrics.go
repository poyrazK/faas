// Package mcpprotocol aggregates bounded, self-reported starter observations.
// App identity is supplied by the host, never decoded from tenant log fields.
package mcpprotocol

import (
	"encoding/json"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
)

const MaxApps = 1024
const Retention = time.Hour

var buckets = []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 300}
var outcomes = []string{"success", "rpc_error", "tool_error", "task_created", "cancelled", "timeout", "unobserved", "authorization_error", "internal_error"}
var tools = []string{"none", "greet", "add", "stream_demo", "build_report", "other"}
var methods = strings.Fields("initialize server/discover ping tools/list tools/call resources/list resources/templates/list resources/read prompts/list prompts/get completion/complete tasks/get tasks/cancel tasks/update subscriptions/listen other")

type histogram struct {
	count   uint64
	sum     float64
	buckets map[float64]uint64
}

func (h *histogram) observe(ms *float64) {
	if ms == nil {
		return
	}
	seconds := *ms / 1000
	h.count++
	h.sum += seconds
	if h.buckets == nil {
		h.buckets = make(map[float64]uint64, len(buckets))
		for _, b := range buckets {
			h.buckets[b] = 0
		}
	}
	for _, b := range buckets {
		if seconds <= b {
			h.buckets[b]++
		}
	}
}

type series struct {
	counts            [9]uint64
	cancellations     uint64
	execution, stream histogram
}
type app struct {
	last  time.Time
	tools map[string]*series
}

type Collector struct {
	mu                                                        sync.Mutex
	apps                                                      map[string]*app
	requests, execution, stream, last, dropped, cancellations *prometheus.Desc
	drops                                                     uint64
}

func New(reg prometheus.Registerer) *Collector {
	c := &Collector{apps: map[string]*app{},
		cancellations: prometheus.NewDesc("vmmd_mcp_protocol_cancellations_total", "Observed RPC or delivery cancellations, counted once per event.", []string{"app_id", "tool"}, nil),
		requests:      prometheus.NewDesc("vmmd_mcp_protocol_requests_total", "Self-reported parsed RPC outcomes; excludes notifications and pre-dispatch rejects.", []string{"app_id", "tool", "outcome"}, nil),
		execution:     prometheus.NewDesc("vmmd_mcp_protocol_execution_seconds", "Dispatch to response construction, independent of stream lifetime.", []string{"app_id", "tool"}, nil),
		stream:        prometheus.NewDesc("vmmd_mcp_protocol_stream_seconds", "SSE headers to delivery completion; may overlap execution.", []string{"app_id", "tool"}, nil),
		last:          prometheus.NewDesc("vmmd_mcp_protocol_last_event_timestamp_seconds", "Host intake time of last accepted event; silence may mean idle traffic.", []string{"app_id"}, nil),
		dropped:       prometheus.NewDesc("vmmd_mcp_protocol_capacity_dropped_total", "Events dropped because the bounded app aggregation table is full.", nil, nil)}
	if reg != nil {
		reg.MustRegister(c)
	}
	return c
}
func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func validDuration(v *float64) bool {
	return v == nil || (!math.IsNaN(*v) && !math.IsInf(*v, 0) && *v >= 0 && *v <= 86400000)
}

// Observe consumes each newly committed log line once. It performs no I/O,
// retains no payload, and bounds parsing to 4 KiB. Replays must not call it.
func (c *Collector) Observe(appID, line string, at time.Time) {
	if c == nil || len(line) > 4096 || !strings.Contains(line, "mcp_protocol_request") {
		return
	}
	if _, err := uuid.Parse(appID); err != nil {
		return
	}
	var event struct {
		Event         string   `json:"event"`
		Method        string   `json:"method"`
		Tool          string   `json:"tool"`
		Outcome       string   `json:"outcome"`
		StreamOutcome string   `json:"stream_outcome"`
		Execution     *float64 `json:"execution_duration_ms"`
		Stream        *float64 `json:"stream_duration_ms"`
	}
	if json.Unmarshal([]byte(line), &event) != nil || event.Event != "mcp_protocol_request" || !contains(methods, event.Method) || !contains(outcomes, event.Outcome) || !validDuration(event.Execution) || !validDuration(event.Stream) {
		return
	}
	tool := "none"
	if event.Method == "tools/call" {
		tool = event.Tool
		if !contains(tools, tool) || tool == "none" {
			tool = "other"
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	a := c.apps[appID]
	if a == nil {
		c.expire(at)
		if len(c.apps) >= MaxApps {
			c.drops++
			return
		}
		a = &app{tools: map[string]*series{}}
		c.apps[appID] = a
	}
	if at.After(a.last) {
		a.last = at
	}
	s := a.tools[tool]
	if s == nil {
		s = &series{}
		a.tools[tool] = s
	}
	for i, outcome := range outcomes {
		if event.Outcome == outcome {
			s.counts[i]++
			break
		}
	}
	if event.Outcome == "cancelled" || event.StreamOutcome == "cancelled" {
		s.cancellations++
	}
	s.execution.observe(event.Execution)
	s.stream.observe(event.Stream)
}
func (c *Collector) expire(at time.Time) {
	for id, a := range c.apps {
		if at.Sub(a.last) > Retention {
			delete(c.apps, id)
		}
	}
}
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{c.requests, c.execution, c.stream, c.last, c.dropped, c.cancellations} {
		ch <- d
	}
}
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	// Snapshot under the lock, then release before sending to the registry.
	// Scrape backpressure must never hold a guest's log intake lock.
	type sample struct {
		id, tool string
		value    series
	}
	c.mu.Lock()
	c.expire(time.Now())
	drops := c.drops
	last := make(map[string]time.Time, len(c.apps))
	var samples []sample
	for id, a := range c.apps {
		last[id] = a.last
		for tool, s := range a.tools {
			value := *s
			value.execution.buckets = copyBuckets(s.execution)
			value.stream.buckets = copyBuckets(s.stream)
			samples = append(samples, sample{id: id, tool: tool, value: value})
		}
	}
	c.mu.Unlock()
	ch <- prometheus.MustNewConstMetric(c.dropped, prometheus.CounterValue, float64(drops))
	for id, at := range last {
		ch <- prometheus.MustNewConstMetric(c.last, prometheus.GaugeValue, float64(at.Unix()), id)
	}
	for _, s := range samples {
		ch <- prometheus.MustNewConstMetric(c.cancellations, prometheus.CounterValue, float64(s.value.cancellations), s.id, s.tool)
		for i, outcome := range outcomes {
			ch <- prometheus.MustNewConstMetric(c.requests, prometheus.CounterValue, float64(s.value.counts[i]), s.id, s.tool, outcome)
		}
		ch <- prometheus.MustNewConstHistogram(c.execution, s.value.execution.count, s.value.execution.sum, s.value.execution.buckets, s.id, s.tool)
		ch <- prometheus.MustNewConstHistogram(c.stream, s.value.stream.count, s.value.stream.sum, s.value.stream.buckets, s.id, s.tool)
	}
}
func copyBuckets(h histogram) map[float64]uint64 {
	values := make(map[float64]uint64, len(buckets))
	for _, b := range buckets {
		values[b] = h.buckets[b]
	}
	return values
}
