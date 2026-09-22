// adr: 201
package circuit_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/circuit"
)

// A slot can be reused less than Window after its previous last sample:
// late in one bucket, then early in the same bucket on the next revolution.
// That must not carry historical successes forward and hide a new outage.
func TestWindowReusedBucketDropsHistoricalSuccesses(t *testing.T) {
	clk := newClock()
	g := circuit.NewGroup(circuit.DefaultConfig(), clk.now)
	clk.add(900 * time.Millisecond)
	for range 100 {
		g.Success(key)
	}
	clk.add(9200 * time.Millisecond)
	g.Success(key) // early in the next revolution of the same ring slot
	clk.add(850 * time.Millisecond)
	for range 5 {
		g.Failure(key)
	}
	if got := g.State(key); got != circuit.StateOpen {
		t.Fatalf("state = %q, want open: the 100 historical successes are older than Window", got)
	}
}

// The inverse regression trips a healthy target: four old failures can be
// refreshed indefinitely by successful samples in their reused ring slot.
func TestWindowReusedBucketDropsHistoricalFailures(t *testing.T) {
	clk := newClock()
	cfg := circuit.DefaultConfig()
	cfg.MinRequests = 6
	g := circuit.NewGroup(cfg, clk.now)
	clk.add(900 * time.Millisecond)
	for range 4 {
		g.Failure(key)
	}
	clk.add(9200 * time.Millisecond)
	g.Success(key)
	clk.add(850 * time.Millisecond)
	g.Success(key)
	if got := g.State(key); got != circuit.StateClosed {
		t.Fatalf("state = %q, want closed: only two recent successes remain in the window", got)
	}
}
