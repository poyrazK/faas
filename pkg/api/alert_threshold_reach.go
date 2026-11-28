package api

import "fmt"

// alertPercentMetrics are ratios of a whole, bounded to 0..100. Metrics
// that can leave that range (SLO budget remaining, signal gaps, storage
// utilisation over quota) are deliberately absent.
var alertPercentMetrics = map[string]bool{
	"error_rate_pct": true, "cold_start_pct": true, "cold_wake_rate_pct": true,
	"event_handler_failure_pct": true, "event_terminal_failure_pct": true,
}

// alertNonNegativeMetrics are counts, durations and rates that are never
// below zero.
var alertNonNegativeMetrics = map[string]bool{
	"latency_p50_ms": true, "latency_p95_ms": true, "latency_p99_ms": true,
	"request_count": true, "failed_invocations": true, "oom_kills": true,
	"queue_depth": true, "queue_backlog": true, "queue_oldest_age_seconds": true,
	"log_error_lines": true, "log_warn_lines": true,
}

// AlertThresholdUnreachable explains a plain-comparison rule that can never
// fire, or returns "". `error_rate_pct gt 500` and `latency_p95_ms lt -1`
// were saved and silently never alerted (prod hunt r63). Baseline
// comparisons are validated separately.
func AlertThresholdUnreachable(metric, comparison string, threshold float64) string {
	pct := alertPercentMetrics[metric]
	if !pct && !alertNonNegativeMetrics[metric] {
		return ""
	}
	switch comparison {
	case "gt":
		if pct && threshold >= 100 {
			return fmt.Sprintf("%s is a percentage (0 to 100), so gt %g never fires; use a threshold below 100", metric, threshold)
		}
	case "gte":
		if pct && threshold > 100 {
			return fmt.Sprintf("%s is a percentage (0 to 100), so gte %g never fires; use a threshold of 100 or less", metric, threshold)
		}
	case "lt":
		if threshold <= 0 {
			return fmt.Sprintf("%s is never below 0, so lt %g never fires; use a threshold above 0", metric, threshold)
		}
	case "lte":
		if threshold < 0 {
			return fmt.Sprintf("%s is never below 0, so lte %g never fires; use a threshold of 0 or more", metric, threshold)
		}
	}
	return ""
}
