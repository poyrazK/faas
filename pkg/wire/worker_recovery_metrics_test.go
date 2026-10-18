// adr: 20261017223000
package wire_test

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

func TestWorkerRecoveryMetricsReplaceFleetSnapshot(t *testing.T) {
	m := wire.NewOpsMetrics("schedd")
	now := time.Unix(1000, 0)
	m.SetWorkerRecoverySummary(3, 1, 1, 4, 2, 1, 1, 2, now)
	for _, want := range []string{
		`schedd_worker_recovery_apps{condition="eligible"} 3`,
		`schedd_worker_recovery_apps{condition="unknown"} 1`,
		`schedd_worker_recovery_apps{condition="overdue"} 1`,
		`schedd_worker_recovery_replicas{state="desired"} 4`,
		`schedd_worker_recovery_replicas{state="missing"} 2`,
		`schedd_worker_recovery_observed_timestamp_seconds 1000`,
	} {
		if !strings.Contains(render(t, m), want) {
			t.Fatalf("missing fleet evidence: %s", want)
		}
	}
	m.SetWorkerRecoverySummary(0, 0, 0, 0, 0, 0, 0, 0, now.Add(time.Minute))
	for _, family := range []string{
		`schedd_worker_recovery_apps{condition="overdue"} 0`,
		`schedd_worker_recovery_replicas{state="missing"} 0`,
		`schedd_worker_recovery_replicas{state="desired"} 0`,
		`schedd_worker_recovery_observed_timestamp_seconds 1060`,
	} {
		if !strings.Contains(render(t, m), family) {
			t.Fatalf("old alert input survived snapshot replacement: %s", family)
		}
	}
	var absent *wire.OpsMetrics
	absent.SetWorkerRecoverySummary(0, 0, 0, 0, 0, 0, 0, 0, now)
}
