// adr: 20261030100950 — the poller publishes the CPU pressure readings of
// the nodes whose readings this schedd receives, for the engine and the
// gauges.
package instancestats

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestPublishCPUPressure(t *testing.T) {
	r := NewReader()
	m := wire.NewOpsMetrics("schedd")
	p := &Poller{Reader: r, Metrics: m}
	at := time.Unix(1_800_000_000, 0)
	if got := r.NodeCPUPressure(at); got != nil {
		t.Fatalf("readings before any tick = %v", got)
	}

	byNode := map[string]sched.NodeCPUPressure{}
	snapshotCPUPressure(byNode, "n1", &sched.StatsSnapshot{CPUPressure: &sched.NodeCPUPressure{SomeAvg10: 31, BusyPercent: 96}})
	snapshotCPUPressure(byNode, "n2", &sched.StatsSnapshot{})
	snapshotCPUPressure(byNode, "n3", nil)
	p.publishCPUPressure(byNode, at)

	got := r.NodeCPUPressure(at.Add(sched.TelemetryFreshness))
	if len(got) != 1 || got["n1"] != (sched.NodeCPUPressure{SomeAvg10: 31, BusyPercent: 96}) {
		t.Fatalf("readings = %v, want n1 only", got)
	}
	if got := r.NodeCPUPressure(at.Add(sched.TelemetryFreshness + time.Second)); got != nil {
		t.Fatalf("stale readings = %v, want none", got)
	}
	body := scrapeMetrics(t, m)
	for _, want := range []string{
		`schedd_node_cpu_pressure_some_avg10{node="n1"} 31`,
		`schedd_node_cpu_busy_percent{node="n1"} 96`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %s", want)
		}
	}
	if strings.Contains(body, `schedd_node_cpu_busy_percent{node="n2"}`) {
		t.Error("a node without readings has a gauge")
	}
}
