// adr: 570
package circuit

import (
	"testing"
	"time"
)

func TestReplayRetainsProbeTimesAndRecovery(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	g := NewGroup(EgressConfig(), func() time.Time { return now })
	for _, age := range []time.Duration{90, 60, 30} {
		g.ObserveAt("upstream", false, now.Add(-age*time.Second))
	}
	if got := g.State("upstream"); got != StateHalfOpen {
		t.Fatalf("replayed open interval was refreshed at startup: %s", got)
	}
	g.ObserveAt("upstream", true, now)
	if got := g.State("upstream"); got != StateClosed {
		t.Fatalf("fresh successful probe did not close replayed circuit: %s", got)
	}
}

func TestReplayUsesPerKeyConfigurationWithoutRefreshingHistoricalSamples(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	g := NewGroup(DefaultConfig(), func() time.Time { return now }).WithConfigFor(func(key string) (Config, bool) {
		return EgressConfig(), key == "upstream"
	})
	for _, age := range []time.Duration{90, 60, 30} {
		g.ObserveAt("upstream", false, now.Add(-age*time.Second))
	}
	if got := g.State("upstream"); got != StateHalfOpen {
		t.Fatalf("per-key replay refreshed historical samples or ignored egress configuration: %s", got)
	}
	g.ObserveAt("upstream", true, now)
	if got := g.State("upstream"); got != StateClosed {
		t.Fatalf("per-key replay did not recover on the actual successful probe: %s", got)
	}
	if got := g.State("other"); got != StateClosed {
		t.Fatalf("upstream replay changed another key: %s", got)
	}
}
