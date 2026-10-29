package wire_test

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

func TestOpsMetrics_CronDelivery(t *testing.T) {
	m := wire.NewOpsMetrics("schedd")
	m.ObserveCronDelivery(wire.CronDeliverySuccess)
	m.ObserveCronDelivery(wire.CronDeliveryFailed)
	m.ObserveCronDelivery(wire.CronDeliveryFailed)
	m.ObserveCronDelivery("unbounded-error-text")
	m.ObserveCronScheduleLag(3 * time.Second)
	m.ObserveCronScheduleLag(-time.Second)

	body := render(t, m)
	for _, want := range []string{
		`schedd_cron_delivery_total{outcome="success"} 1`,
		`schedd_cron_delivery_total{outcome="retry"} 0`,
		`schedd_cron_delivery_total{outcome="failed"} 2`,
		`schedd_cron_delivery_total{outcome="dead_letter"} 0`,
		`schedd_cron_schedule_lag_seconds_bucket{le="5"} 1`,
		`schedd_cron_schedule_lag_seconds_count 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing cron delivery metric %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, "unbounded-error-text") {
		t.Fatalf("unknown outcome escaped the closed label vocabulary:\n%s", body)
	}
}
