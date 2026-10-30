// adr: 20261029151111 — an instance's measured CPU is the larger of its
// latest CPU and the p95 of its per-bucket peaks, once its history is long
// enough, and admission ignores a stale sample.
package instancestats

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestCPUHistoryChargeSample(t *testing.T) {
	h := newCPUHistory()
	start := time.Unix(1_800_000_000, 0)
	every := time.Duration(api.MeasuredChargeSampleSeconds) * time.Second
	minHistory := time.Duration(api.MeasuredCPUMinHistorySeconds) * time.Second
	if _, measured := h.chargeSample("a", 3, start); measured {
		t.Fatal("an instance with one sample was measured")
	}
	// A burst between two bucket starts still counts: a bucket keeps its peak.
	if _, measured := h.chargeSample("a", 80, start.Add(every+time.Second)); measured {
		t.Fatal("an instance was measured before its history spanned the minimum")
	}
	h.chargeSample("a", 2, start.Add(every+2*time.Second))
	got, measured := h.chargeSample("a", 2, start.Add(minHistory))
	if !measured || got != 80 {
		t.Fatalf("charge after a burst = %v, %v; want the bucket peak 80, measured", got, measured)
	}
	// The latest reading wins when it is above the p95.
	if got, _ := h.chargeSample("a", 150, start.Add(minHistory+time.Second)); got != 150 {
		t.Fatalf("charge during a spike = %v, want the latest 150", got)
	}
	// Ten minutes of quiet later, the burst has aged out.
	window := time.Duration(api.MeasuredChargeWindowSeconds) * time.Second
	last := start.Add(minHistory + time.Second)
	for at := last.Add(every); at.Sub(last) <= window+every; at = at.Add(every) {
		got, _ = h.chargeSample("a", 2, at)
	}
	if got != 2 {
		t.Fatalf("charge after the window passed = %v, want 2", got)
	}
	h.retain(map[string]struct{}{})
	if len(h.byInstance) != 0 {
		t.Fatal("retain kept a history no row listed")
	}
}

func TestMeasuredCPUSnapshot(t *testing.T) {
	r := NewReader()
	m := wire.NewOpsMetrics("schedd")
	p := &Poller{Reader: r, Metrics: m}
	start := time.Unix(1_800_000_000, 0)
	if _, _, ok := r.MeasuredCPU("n1", start); ok {
		t.Fatal("MeasuredCPU before any tick returned ok=true")
	}
	rows := func(at time.Time) []InstanceStat {
		return []InstanceStat{
			{InstanceID: "a", NodeID: "n1", CPU: Valid, CPUPct: 4.5, SampledAt: at},
			{InstanceID: "b", NodeID: "n1", CPU: Unknown, CPUPct: math.NaN(), SampledAt: at},
			{InstanceID: "c", NodeID: "n2", CPU: Valid, CPUPct: 50, SampledAt: at},
		}
	}
	p.publishMeasuredCPU(rows(start), start)
	if got, _, ok := r.MeasuredCPU("n1", start); !ok || len(got) != 0 {
		t.Fatalf("MeasuredCPU on the first tick = %v, %v; want nothing measured yet", got, ok)
	}
	tick := start.Add(time.Duration(api.MeasuredCPUMinHistorySeconds) * time.Second)
	p.publishMeasuredCPU(rows(tick), tick)
	got, asOf, ok := r.MeasuredCPU("n1", tick)
	if !ok || !asOf.Equal(tick) || len(got) != 1 || got["a"] != 45 {
		t.Fatalf("MeasuredCPU(n1) = %v, %v, %v; want {a: 45 millicores}", got, asOf, ok)
	}
	if got, _, ok := r.MeasuredCPU("n2", tick); !ok || got["c"] != 500 {
		t.Fatalf("MeasuredCPU(n2) = %v, %v; want {c: 500}", got, ok)
	}
	if got, _, ok := r.MeasuredCPU("n3", tick); !ok || len(got) != 0 {
		t.Fatalf("MeasuredCPU for an unreported node = %v, %v; want an empty fresh sample", got, ok)
	}
	// The node gauge sums a node's measured instances and the histogram
	// observes each one, for tuning the charge.
	body := scrapeMetrics(t, m)
	for _, want := range []string{
		`schedd_node_cpu_measured_millicores{node="n1"} 45`,
		`schedd_node_cpu_measured_millicores{node="n2"} 500`,
		`schedd_instance_cpu_measured_millicores_bucket{node="n1",le="50"} 1`,
		`schedd_instance_cpu_measured_millicores_bucket{node="n1",le="25"} 0`,
		`schedd_instance_cpu_measured_millicores_count{node="n2"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %s:\n%s", want, body)
		}
	}
	stale := tick.Add(time.Duration(api.MeasuredAdmissionSampleMaxAgeMillis)*time.Millisecond + time.Millisecond)
	if _, _, ok := r.MeasuredCPU("n1", stale); ok {
		t.Fatal("MeasuredCPU accepted a stale sample")
	}
	// A row without a CPU reading this tick keeps the instance's history.
	p.publishMeasuredCPU([]InstanceStat{{InstanceID: "a", NodeID: "n1", CPU: Unknown, SampledAt: tick}}, tick)
	if _, kept := p.cpuHistory.byInstance["a"]; !kept {
		t.Fatal("a tick without a CPU reading dropped the instance's history")
	}
}

// A fresh tick can carry a node's sample seconds older than itself; the
// measurement is dated and aged by vmmd's sample, as quiet pauses are.
func TestMeasuredCPUUsesNodeSampleTime(t *testing.T) {
	r := NewReader()
	p := &Poller{Reader: r}
	tick := time.Unix(1_800_000_000, 0)
	maxAge := time.Duration(api.MeasuredAdmissionSampleMaxAgeMillis) * time.Millisecond
	fresh, stale := tick.Add(-time.Second), tick.Add(-maxAge-time.Second)
	p.publishMeasuredCPU([]InstanceStat{
		{InstanceID: "a", NodeID: "n1", CPU: Valid, CPUPct: 1, SampledAt: fresh},
		{InstanceID: "b", NodeID: "n2", CPU: Valid, CPUPct: 1, SampledAt: stale},
	}, tick)
	if _, asOf, ok := r.MeasuredCPU("n1", tick); !ok || !asOf.Equal(fresh) {
		t.Fatalf("MeasuredCPU(n1) as of %v, %v; want vmmd's sample %v", asOf, ok, fresh)
	}
	if _, _, ok := r.MeasuredCPU("n2", tick); ok {
		t.Fatal("MeasuredCPU(n2) accepted a stale node sample")
	}
}

func TestMeasuredCPUAcross(t *testing.T) {
	local, fleet := NewReader(), NewReader()
	now := time.Unix(1_800_000_000, 0)
	local.cpu.Store(&measuredCPUSnapshot{asOf: now, byNode: map[string]measuredCPUNode{
		"own": {millicores: map[string]int{"l": 1}, sampledAt: now}}})
	fleet.cpu.Store(&measuredCPUSnapshot{asOf: now, byNode: map[string]measuredCPUNode{
		"own":  {millicores: map[string]int{"f": 1}, sampledAt: now},
		"peer": {millicores: map[string]int{"p": 1}, sampledAt: now}}})
	read := MeasuredCPUAcross(local, fleet, "own")
	if got, _, _ := read("own", now); got["l"] != 1 {
		t.Fatalf("own node read %v, want the local reader", got)
	}
	if got, _, _ := read("peer", now); got["p"] != 1 {
		t.Fatalf("peer node read %v, want the fleet reader", got)
	}
	if got, _, _ := MeasuredCPUAcross(local, fleet, "")("peer", now); got != nil {
		t.Fatalf("single box read %v from the fleet reader", got)
	}
}
