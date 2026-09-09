package wire_test

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

func TestOpsMetrics_APIHostingFunnelClosedAndPrivacySafe(t *testing.T) {
	m := wire.NewOpsMetrics("apid")
	m.ObserveAPIHostingPhase(wire.APIHostingFlowFirstDeploy, "source_detected", wire.APIHostingOutcomeComplete, 12*time.Millisecond)
	m.ObserveAPIHostingPhase(wire.APIHostingFlowFirstDeploy, "upload", wire.APIHostingOutcomeFailed, 250*time.Millisecond)
	m.ObserveAPIHostingPhase(wire.APIHostingFlowDev, "source_delta", wire.APIHostingOutcomeComplete, 3*time.Millisecond)
	m.ObserveAPIHostingPhase(wire.APIHostingFlowDev, "route_switch", wire.APIHostingOutcomeSkipped, 0)

	// These values intentionally look like the kinds of data that must never
	// become labels. The observer silently drops them at the closed-set
	// boundary rather than letting a caller create unbounded series.
	m.ObserveAPIHostingPhase("/workspace/customer-api", "/repo/main", "https://api.example.test", time.Second)
	m.ObserveAPIHostingPhase(wire.APIHostingFlowDev, "environment=production", wire.APIHostingOutcomeComplete, time.Second)
	m.ObserveAPIHostingPhase(wire.APIHostingFlowDev, "build", "failure_code_from_customer_input", time.Second)
	m.ObserveAPIHostingPhase(wire.APIHostingFlowDev, "build", wire.APIHostingOutcomeComplete, -time.Second)

	body := render(t, m)
	for _, want := range []string{
		`apid_api_hosting_phase_total{flow="first_deploy",outcome="completed",phase="source_detected"} 1`,
		`apid_api_hosting_phase_total{flow="first_deploy",outcome="failed",phase="upload"} 1`,
		`apid_api_hosting_phase_total{flow="dev",outcome="completed",phase="source_delta"} 1`,
		`apid_api_hosting_phase_total{flow="dev",outcome="skipped",phase="route_switch"} 1`,
		`apid_api_hosting_phase_duration_seconds_count{flow="first_deploy",outcome="completed",phase="source_detected"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing funnel sample %q in:\n%s", want, body)
		}
	}

	for _, forbidden := range []string{"workspace/customer-api", "/repo/main", "api.example.test", "environment=production", "failure_code_from_customer_input"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("privacy-sensitive value %q leaked into metrics:\n%s", forbidden, body)
		}
	}
}

func TestOpsMetrics_APIHostingFunnelPreinstantiatesBoundedSeries(t *testing.T) {
	m := wire.NewOpsMetrics("apid")
	body := render(t, m)

	// 2 flows × 9 phases × 3 outcomes is the complete counter surface. The
	// same tuples are pre-instantiated for the duration histogram, so idle
	// daemons still produce a stable zero-valued dashboard surface.
	const wantSeries = 2 * 9 * 3
	got := 0
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "apid_api_hosting_phase_total{") {
			got++
		}
	}
	if got != wantSeries {
		t.Fatalf("api-hosting counter cardinality = %d, want %d", got, wantSeries)
	}
}

func TestOpsMetrics_APIHostingFunnelNilSafe(t *testing.T) {
	var m *wire.OpsMetrics
	m.ObserveAPIHostingPhase(wire.APIHostingFlowFirstDeploy, "build", wire.APIHostingOutcomeComplete, time.Second)
}
