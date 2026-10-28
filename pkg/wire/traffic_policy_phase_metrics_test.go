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
