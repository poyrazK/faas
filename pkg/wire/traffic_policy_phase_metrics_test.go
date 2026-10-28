// adr: 20261027163626
package wire

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestTrafficPolicyPhaseHistogramIsApidOnly(t *testing.T) {
	apid := NewOpsMetrics("apid")
	apid.ObserveTrafficPolicyPhase("app_binding", "validate", 1500*time.Millisecond)
	if got := testutil.CollectAndCount(apid.Registry(), "traffic_policy_analysis_seconds"); got != 1 {
		t.Fatalf("apid series = %d, want 1", got)
	}
	other := NewOpsMetrics("schedd")
	other.ObserveTrafficPolicyPhase("app_binding", "validate", time.Second)
	if got := testutil.CollectAndCount(other.Registry(), "traffic_policy_analysis_seconds"); got != 0 {
		t.Fatalf("schedd series = %d, want 0", got)
	}
	var unwired *OpsMetrics
	unwired.ObserveTrafficPolicyPhase("app_binding", "validate", time.Second)
}

func TestTrafficBindingScopeCountersAreApidOnly(t *testing.T) {
	apid := NewOpsMetrics("apid")
	apid.ObserveTrafficBindingScope("app_binding", nil)
	apid.ObserveTrafficBindingScope("app_binding", []string{"alias", "alias"})
	if got := testutil.ToFloat64(apid.trafficBindingScopeAudits.WithLabelValues("app_binding", "covered")); got != 1 {
		t.Fatalf("covered audits = %v", got)
	}
	if got := testutil.ToFloat64(apid.trafficBindingScopeMisses.WithLabelValues("app_binding", "alias")); got != 2 {
		t.Fatalf("alias misses = %v", got)
	}
	NewOpsMetrics("schedd").ObserveTrafficBindingScope("app_binding", []string{"alias"})
	var unwired *OpsMetrics
	unwired.ObserveTrafficBindingScope("app_binding", []string{"alias"})
}
