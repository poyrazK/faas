// adr: 201
package gateway

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func gatherNamed(t *testing.T, reg *prometheus.Registry, name string) []*dto.Metric {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, f := range families {
		if f.GetName() == name {
			return f.GetMetric()
		}
	}
	return nil
}

// The metrics must be on the daemon's OWN registry. A collector that is
// constructed but never registered is invisible to /metrics — the exact
// failure mode recorded for promauto-in-pkg elsewhere in this repo.
func TestTrafficResilienceMetricsAreRegistered(t *testing.T) {
	m := NewMetrics()
	m.PreInstantiateTrafficResilience()
	for _, name := range []string{
		"gateway_retry_attempts_total",
		"gateway_retry_exhausted_total",
		"gateway_circuit_transitions_total",
	} {
		if got := gatherNamed(t, m.Registry(), name); len(got) == 0 {
			t.Fatalf("%s has no series after pre-instantiation; it is either unregistered or not pre-instantiated", name)
		}
	}
}

// Pre-instantiation exists so an operator alerting on `rate == 0` is not
// reading a cold-start absence as a healthy zero.
func TestTrafficResilienceSeriesExistBeforeAnyTraffic(t *testing.T) {
	m := NewMetrics()
	m.PreInstantiateTrafficResilience()

	attempts := gatherNamed(t, m.Registry(), "gateway_retry_attempts_total")
	if len(attempts) != 4 {
		t.Fatalf("retry attempt series = %d, want all 4 outcomes present at boot", len(attempts))
	}
	for _, s := range attempts {
		if s.GetCounter().GetValue() != 0 {
			t.Fatalf("pre-instantiated series started non-zero: %v", s)
		}
	}
	exhausted := gatherNamed(t, m.Registry(), "gateway_retry_exhausted_total")
	if len(exhausted) != 8 {
		t.Fatalf("retry exhausted series = %d, want all 8 reasons", len(exhausted))
	}
}

// Every exhaustion reason the retry loop can emit must have a pre-instantiated
// series, or a real rejection surfaces a label nobody has a panel for.
func TestEveryRetryReasonIsPreInstantiated(t *testing.T) {
	m := NewMetrics()
	m.PreInstantiateTrafficResilience()
	got := map[string]bool{}
	for _, s := range gatherNamed(t, m.Registry(), "gateway_retry_exhausted_total") {
		for _, l := range s.GetLabel() {
			if l.GetName() == "reason" {
				got[l.GetValue()] = true
			}
		}
	}
	for _, reason := range []string{
		RetrySkipCommitted, RetrySkipNonIdempotent, RetrySkipNoTarget,
		RetrySkipBudget, RetrySkipAttempts, RetrySkipBodyNotReplay,
		RetrySkipIdempotency, RetrySkipAggregate,
	} {
		if !got[reason] {
			t.Fatalf("reason %q has no pre-instantiated series; the loop can emit it", reason)
		}
	}
}

func TestTrafficResilienceCountersIncrement(t *testing.T) {
	m := NewMetrics()
	m.IncRetryAttempt("replay_ok")
	m.IncRetryExhausted(RetrySkipNoTarget)
	m.IncCircuitTransition("closed", "open")
	m.SetCircuitOpenTargets("app-1", 3)

	if v := labelledCounterValue(t, m.Registry(), "gateway_retry_attempts_total", "outcome", "replay_ok"); v != 1 {
		t.Fatalf("replay_ok = %v, want 1", v)
	}
	if v := labelledCounterValue(t, m.Registry(), "gateway_retry_exhausted_total", "reason", RetrySkipNoTarget); v != 1 {
		t.Fatalf("no_healthy_sibling = %v, want 1", v)
	}
	gauges := gatherNamed(t, m.Registry(), "gateway_circuit_open_targets")
	if len(gauges) != 1 || gauges[0].GetGauge().GetValue() != 3 {
		t.Fatalf("open targets = %v, want a single app-1 series at 3", gauges)
	}
}

func labelledCounterValue(t *testing.T, reg *prometheus.Registry, name, label, value string) float64 {
	t.Helper()
	for _, s := range gatherNamed(t, reg, name) {
		for _, l := range s.GetLabel() {
			if l.GetName() == label && l.GetValue() == value {
				return s.GetCounter().GetValue()
			}
		}
	}
	return -1
}

// §11: no ADR-201 metric may carry a plaintext hostname. The gateway metrics
// are keyed by app/instance identifiers only; the egress gauge (on the shared
// OpsMetrics registry) takes the redacted hash. This pins the gateway half.
func TestTrafficResilienceMetricsCarryNoHostLabels(t *testing.T) {
	m := NewMetrics()
	m.PreInstantiateTrafficResilience()
	m.SetCircuitOpenTargets("app-1", 1)
	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, f := range families {
		if !strings.HasPrefix(f.GetName(), "gateway_retry_") &&
			!strings.HasPrefix(f.GetName(), "gateway_circuit_") {
			continue
		}
		for _, s := range f.GetMetric() {
			for _, l := range s.GetLabel() {
				if l.GetName() == "host" || l.GetName() == "upstream" || l.GetName() == "hostname" {
					t.Fatalf("%s carries a %q label; §11 forbids plaintext hosts in labels", f.GetName(), l.GetName())
				}
			}
		}
	}
}
