package main

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// requestLatencyMS renders a request-duration quantile. A value at the
// histogram's top finite bucket only says "at least that long"; printing
// it as 10000.0ms read as a measured latency (Hunt 12).
func requestLatencyMS(ms float64) string {
	if top := api.RequestDurationTopBucketSeconds * 1000; ms >= top {
		return fmt.Sprintf("≥%.0fms", top)
	}
	return fmt.Sprintf("%.1fms", ms)
}

// requestLatencyLine is "p50=… p95=… p99=…" with requestLatencyMS values.
func requestLatencyLine(p50, p95, p99 float64) string {
	return "p50=" + requestLatencyMS(p50) + " p95=" + requestLatencyMS(p95) + " p99=" + requestLatencyMS(p99)
}
