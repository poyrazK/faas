package main

import (
	"fmt"
	"io"
	"strings"
	"time"
)

func sameObsDeploymentSourceStatus(a, b *int) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func obsDeploymentSourceStatus(status *int) string {
	if status == nil {
		return "unknown"
	}
	return fmt.Sprintf("%d", *status)
}

func writeObsDeploymentSources(w io.Writer, sources *obsDeploymentSources) {
	if sources == nil {
		return
	}
	writeObsDeploymentSource(w, "serving_health", sources.ServingHealth)
	writeObsDeploymentSource(w, "notification_health", sources.NotificationHealth)
}

func writeObsDeploymentSource(w io.Writer, name string, source obsDeploymentSourceSummary) {
	_, _ = fmt.Fprintf(w, "source_read source=%s state=%s reason=%s http_status=%s reads=%d failed_reads=%d cancelled_reads=%d consecutive_failures=%d last_read_at=%s last_success_at=%s first_failure_at=%s last_recovery_at=%s\n", name, source.State, source.Reason, obsDeploymentSourceStatus(source.HTTPStatus), source.Reads, source.FailedReads, source.CancelledReads, source.ConsecutiveFailures, servingTime(source.LastReadAt), servingTime(source.LastSuccessAt), servingTime(source.FirstFailureAt), servingTime(source.LastRecoveryAt))
	if source.LastReadSeconds != nil {
		_, _ = fmt.Fprintf(w, "  source_read_timing source=%s started_at=%s completed_at=%s duration_seconds=%g\n", name, servingTime(source.LastReadStartedAt), servingTime(source.LastReadCompletedAt), *source.LastReadSeconds)
	}
	if failure := source.LastFailure; failure != nil {
		_, _ = fmt.Fprintf(w, "  source_read_failure source=%s at=%s reason=%s http_status=%s\n", name, failure.At.Format(time.RFC3339Nano), failure.Reason, obsDeploymentSourceStatus(failure.HTTPStatus))
	}
}

func writeObsDeploymentSourceTransitions(w io.Writer, transitions []obsDeploymentTransition) {
	for _, transition := range transitions {
		if transition.State == "unknown" && strings.HasPrefix(transition.Reason, "source_") {
			_, _ = fmt.Fprintf(w, "  source_read_transition at=%s attempt=%d check=%s reason=%s http_status=%s\n", transition.At.Format(time.RFC3339Nano), transition.Attempt, transition.Check, transition.Reason, obsDeploymentSourceStatus(transition.SourceHTTPStatus))
		}
	}
}
