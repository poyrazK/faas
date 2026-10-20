package liveserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type closingConn struct{ closed chan struct{} }

func (c *closingConn) Send([]byte) error { return nil }
func (c *closingConn) Close() error {
	close(c.closed)
	return nil
}

// adr: 20261016151947 — a process that restarts one database's hub must not
// keep connections that reached the stopped hub: nothing would ever answer
// them, and realtimed would wait on them instead of reconnecting.
func TestSessionOfAStoppedHubCloses(t *testing.T) {
	hub := New(Config{Database: "db_1"})
	close(hub.done) // the hub's loop has returned
	conn := &closingConn{closed: make(chan struct{})}
	sess := hub.attach(context.Background(), conn, false)
	sess.Receive(context.Background(), []byte(`{"type":"subscribe"}`))
	select {
	case <-conn.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("a connection attached to a stopped hub stayed open")
	}
	<-sess.Done()
}

// adr: 20261016151947 — one process serving several databases labels each
// hub's metrics with its database and shares the process-wide memory gauge.
func TestMetricsSetLabelsEachDatabase(t *testing.T) {
	reg := prometheus.NewRegistry()
	set := NewMetricsSet(reg)
	a, b := set.Database("db_a"), set.Database("db_b")
	a.SetUp(true)
	b.SetUp(false)
	b.Restarted()
	a.SetSlotLag(42)
	want := `
# HELP livequery_hub_up 1 while the database's hub runs; 0 while it starts or restarts after an error.
# TYPE livequery_hub_up gauge
livequery_hub_up{database="db_a"} 1
livequery_hub_up{database="db_b"} 0
# HELP livequery_hub_restarts_total Times the database's hub stopped on an error and was started again.
# TYPE livequery_hub_restarts_total counter
livequery_hub_restarts_total{database="db_a"} 0
livequery_hub_restarts_total{database="db_b"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "livequery_hub_up", "livequery_hub_restarts_total"); err != nil {
		t.Fatal(err)
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() != "livequery_engine_memory_bytes" {
			continue
		}
		if n := len(f.GetMetric()); n != 1 || len(f.GetMetric()[0].GetLabel()) != 0 {
			t.Fatalf("engine memory has %d series, labels %v; want one unlabeled process gauge", n, f.GetMetric()[0].GetLabel())
		}
	}
	if got := testutil.ToFloat64(a.slotLag); got != 42 {
		t.Fatalf("db_a slot lag = %v", got)
	}
}
