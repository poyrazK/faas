package operatorobs

import (
	"context"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/promql"
)

func runtimeBuilderMetricNames() []string {
	return []string{
		"builderd_build_worker_started_timestamp_seconds", "builderd_build_worker_last_attempt_timestamp_seconds", "builderd_build_worker_last_completed_timestamp_seconds",
		"builderd_build_worker_enabled", "builderd_build_worker_inflight", "builderd_build_worker_interval_seconds", "builderd_build_worker_busy_allowance_seconds",
		"builderd_build_slots_in_use", "builderd_build_slots_limit",
		"builderd_notification_queue_waiting", "builderd_notification_queue_active", "builderd_notification_queue_capacity", "builderd_notification_queue_workers", "builderd_notification_queue_oldest_enqueued_timestamp_seconds", "builderd_notification_queue_enabled",
	}
}

func runtimeBuilderQuery() string {
	// Retain the metric family in a reviewed label before timestamp drops __name__.
	return runtimeGaugeQuery("builderd", runtimeBuilderMetricNames())
}

func runtimeBuilderSignalKeys() map[string]bool {
	out := map[string]bool{}
	for _, name := range runtimeBuilderMetricNames() {
		if strings.HasPrefix(name, "builderd_notification_queue_") {
			for _, queue := range []string{"build", "cancel"} {
				out[name+"/"+queue] = true
			}
		} else {
			out[name+"/"] = true
		}
	}
	return out
}

func readRuntimeBuilderSamples(ctx context.Context, client *promql.Client, builds runtimeBuildSamples, now time.Time) runtimeGaugeSamples {
	return readRuntimeGaugeSamples(ctx, client, builds, "builderd", runtimeBuilderQuery(), runtimeBuilderSignalKeys(), now)
}
