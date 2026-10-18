package mcpprotocol

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestCoverageLifecycleIdentityPauseAndReplacement(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := NewCoverage(reg)
	appID, depID := uuid.NewString(), uuid.NewString()
	observe, stop := c.Start(appID, depID, "host-instance")
	now := time.Now()
	observe(logbuf.Line{Line: `{"event":"mcp_telemetry_heartbeat","version":1,"loss_alert":true,"app_id":"forged","instance_id":"forged"}`, WrittenAt: now})
	if c.instances[appID+"/host-instance"].heartbeat != now {
		t.Fatal("heartbeat missing")
	}
	if count := testutil.CollectAndCount(c, "vmmd_mcp_telemetry_heartbeat_timestamp_seconds"); count != 1 {
		t.Fatal(count)
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		for _, m := range f.Metric {
			for _, l := range m.Label {
				if strings.Contains(l.GetValue(), "forged") {
					t.Fatal("guest identity escaped")
				}
			}
		}
	}
	c.Pause("host-instance", true)
	if count := testutil.CollectAndCount(c, "vmmd_mcp_telemetry_heartbeat_timestamp_seconds"); count != 0 {
		t.Fatal("paused instance still reporting")
	}
	c.Pause("host-instance", false)
	if !c.instances[appID+"/host-instance"].heartbeat.IsZero() {
		t.Fatal("resume reused old heartbeat")
	}
	replacement, replacementStop := c.Start(appID, depID, "host-instance")
	stop()
	observe(logbuf.Line{Line: `{"event":"mcp_telemetry_heartbeat","version":1}`, WrittenAt: now})
	if len(c.instances) != 1 || !c.instances[appID+"/host-instance"].heartbeat.IsZero() {
		t.Fatal("old lifecycle mutated replacement")
	}
	replacement(logbuf.Line{Line: `{"event":"mcp_telemetry_heartbeat","version":2}`, WrittenAt: now})
	if !c.instances[appID+"/host-instance"].heartbeat.IsZero() {
		t.Fatal("unsupported version accepted")
	}
	replacementStop()
	replacementStop()
	if len(c.instances) != 0 {
		t.Fatal("retired lifecycle retained")
	}
}
func TestCoverageCapacity(t *testing.T) {
	c := NewCoverage(nil)
	app := uuid.NewString()
	for range MaxCoverageInstances {
		c.Start(app, "", uuid.NewString())
	}
	c.Start(app, "", "overflow")
	if len(c.instances) != MaxCoverageInstances || c.drops != 1 {
		t.Fatal("capacity not bounded")
	}
}

type coverageReader struct {
	performanceReader
	rows []promql.VectorSample
}

func (r coverageReader) QueryVector(_ context.Context, q string) ([]promql.VectorSample, error) {
	if !strings.Contains(q, `app_id="`+r.app+`"`) {
		return nil, context.Canceled
	}
	return r.rows, nil
}
func TestCoverageStatesAndInventory(t *testing.T) {
	id := uuid.NewString()
	now := time.Now()
	r := coverageReader{performanceReader: performanceReader{app: id}}
	for _, tc := range []struct {
		name   string
		values []float64
		state  string
	}{{"reporting", []float64{1, 3}, "Reporting"}, {"partial", []float64{1, 0}, "Partially reporting"}, {"starting", []float64{2, 2}, "Starting"}, {"stale", []float64{0, 0}, "Stale"}, {"missing", nil, "Unavailable"}} {
		t.Run(tc.name, func(t *testing.T) {
			r.rows = nil
			for _, v := range tc.values {
				r.rows = append(r.rows, promql.VectorSample{Value: v})
			}
			got, err := FetchCoverage(t.Context(), r, id, now)
			if err != nil || got.State != tc.state {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
	r.rows = []promql.VectorSample{{Labels: map[string]string{"instance_id": "one"}, Value: 1}}
	got, err := FetchCoverage(t.Context(), r, id, now, []string{"one", "missing"})
	if err != nil || got.State != "Unavailable (instance coverage incomplete)" || got.Expected != 2 {
		t.Fatalf("missing replica: %+v %v", got, err)
	}
	got, err = FetchCoverage(t.Context(), r, id, now, []string{})
	if err != nil || got.State != "Idle (no executing instances)" || got.Expected != 0 {
		t.Fatalf("scale zero: %+v %v", got, err)
	}
}

func TestCoverageConcurrentScrapePauseAndIntake(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := NewCoverage(reg)
	observe, stop := c.Start(uuid.NewString(), uuid.NewString(), "replica")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 100 {
			observe(logbuf.Line{Line: `{"event":"mcp_telemetry_heartbeat","version":1}`, WrittenAt: time.Now()})
			c.Pause("replica", true)
			c.Pause("replica", false)
		}
	}()
	for range 20 {
		if _, err := reg.Gather(); err != nil {
			t.Fatal(err)
		}
	}
	<-done
	stop()
}
