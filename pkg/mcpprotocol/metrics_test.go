package mcpprotocol

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestHostIdentityBoundedLabelsAndHistograms(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := New(reg)
	hostApp := uuid.NewString()
	c.Observe(hostApp, `{"event":"mcp_protocol_request","app_id":"forged-app","method":"tools/call","tool":"secret-tool-name","outcome":"tool_error","execution_duration_ms":250,"stream_duration_ms":5000,"arguments":{"secret":"never-retained"}}`, time.Now())
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, family := range families {
		seen[family.GetName()] = true
		for _, m := range family.Metric {
			for _, l := range m.Label {
				if l.GetName() == "app_id" && l.GetValue() != hostApp {
					t.Fatal("tenant-supplied identity escaped")
				}
				if strings.Contains(l.GetValue(), "secret") || l.GetValue() == "forged-app" {
					t.Fatal("unbounded label escaped")
				}
			}
			if family.GetName() == "vmmd_mcp_protocol_execution_seconds" {
				if m.Histogram.GetSampleCount() != 1 || m.Histogram.GetSampleSum() != .25 {
					t.Fatal("wrong execution histogram")
				}
				if len(m.Histogram.Bucket) != len(buckets) {
					t.Fatal("missing histogram buckets")
				}
			}
			if family.GetName() == "vmmd_mcp_protocol_stream_seconds" && m.Histogram.GetSampleSum() != 5 {
				t.Fatal("stream conflated with execution")
			}
		}
	}
	if len(seen) != 6 {
		t.Fatalf("families=%v", seen)
	}
}
func TestRejectMalformedEventsAndExpireCapacity(t *testing.T) {
	c := New(nil)
	id := uuid.NewString()
	now := time.Now()
	for _, line := range []string{`{"event":"mcp_tool_call","method":"tools/call","outcome":"success"}`, `{"event":"mcp_protocol_request","method":"tools/call","outcome":"invented"}`, `{"event":"mcp_protocol_request","method":"tools/call","outcome":"success","execution_duration_ms":-1}`, `{"event":"mcp_protocol_request","method":"tools/call","outcome":"success","stream_duration_ms":86400001}`, strings.Repeat("x", 4097)} {
		c.Observe(id, line, now)
	}
	event := `{"event":"mcp_protocol_request","method":"ping","tool":"greet","outcome":"success"}`
	c.Observe("not-a-host-uuid", event, now)
	if len(c.apps) != 0 {
		t.Fatal("invalid event accepted")
	}
	for range MaxApps {
		c.Observe(uuid.NewString(), event, now)
	}
	c.Observe(id, event, now)
	if len(c.apps) != MaxApps || c.drops != 1 {
		t.Fatal("capacity not bounded")
	}
	c.Observe(id, event, now.Add(Retention+time.Second))
	if len(c.apps) != 1 {
		t.Fatal("expired apps not reclaimed")
	}
	if c.apps[id].tools["none"].counts[0] != 1 {
		t.Fatal("non-tool RPC mislabeled")
	}
}
func TestCollectorConcurrentIntakeAndScrape(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := New(reg)
	id := uuid.NewString()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 100 {
			c.Observe(id, `{"event":"mcp_protocol_request","method":"tools/call","tool":"greet","outcome":"cancelled"}`, time.Now())
		}
	}()
	for range 10 {
		if _, err := reg.Gather(); err != nil {
			t.Fatal(err)
		}
	}
	<-done
	if count := testutil.CollectAndCount(c, "vmmd_mcp_protocol_requests_total"); count != 9 {
		t.Fatalf("outcome series=%d", count)
	}
}

func TestDeliveryCancellationPreservesRPCOutcome(t *testing.T) {
	c := New(nil)
	id := uuid.NewString()
	c.Observe(id, `{"event":"mcp_protocol_request","method":"tools/call","tool":"greet","outcome":"success","stream_outcome":"cancelled","execution_duration_ms":10}`, time.Now())
	c.Observe(id, `{"event":"mcp_protocol_request","method":"tools/call","tool":"greet","outcome":"cancelled","stream_outcome":"cancelled"}`, time.Now())
	s := c.apps[id].tools["greet"]
	if s.counts[0] != 1 || s.counts[4] != 1 || s.cancellations != 2 {
		t.Fatal("RPC outcome overwritten or cancellation counted twice")
	}
}
