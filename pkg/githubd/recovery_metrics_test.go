package githubd

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecoveryCollectorNilPoolReportsFailureAndZeroQueues(t *testing.T) {
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(NewRecoveryCollector(nil))
	want := `
# HELP githubd_recovery_collect_error 1 when the last GitHub recovery queue metric collection failed.
# TYPE githubd_recovery_collect_error gauge
githubd_recovery_collect_error 1
# HELP githubd_recovery_queue_items Actionable durable GitHub recovery work by queue and state.
# TYPE githubd_recovery_queue_items gauge
githubd_recovery_queue_items{queue="check_update",status="dead"} 0
githubd_recovery_queue_items{queue="check_update",status="pending"} 0
githubd_recovery_queue_items{queue="check_update",status="processing"} 0
githubd_recovery_queue_items{queue="webhook_delivery",status="dead"} 0
githubd_recovery_queue_items{queue="webhook_delivery",status="pending"} 0
githubd_recovery_queue_items{queue="webhook_delivery",status="processing"} 0
`
	if err := testutil.GatherAndCompare(registry, strings.NewReader(want),
		"githubd_recovery_collect_error", "githubd_recovery_queue_items"); err != nil {
		t.Fatal(err)
	}
}
