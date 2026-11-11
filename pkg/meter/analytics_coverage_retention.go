package meter

import (
	"context"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
)

// Coverage records have no useful evidence outside the longest supported
// request-telemetry window. Pending rows left by crashed writers expire here
// too; until then they continue to disclose missing counts.
var analyticsCoverageRetentionSQL = [...]string{
	`DELETE FROM public.request_analytics_coverage_watermarks WHERE ctid IN (SELECT ctid FROM public.request_analytics_coverage_watermarks WHERE source_at < now()-$2::integer*interval '1 day' ORDER BY source_at LIMIT $1)`,
	`DELETE FROM public.request_analytics_rollup_windows WHERE ctid IN (SELECT ctid FROM public.request_analytics_rollup_windows WHERE bucket_start < now()-$2::integer*interval '1 day' ORDER BY bucket_start LIMIT $1)`,
	`DELETE FROM public.request_analytics_coverage_intervals WHERE ctid IN (SELECT ctid FROM public.request_analytics_coverage_intervals WHERE covered_until < now()-$2::integer*interval '1 day' ORDER BY covered_until LIMIT $1)`,
	`DELETE FROM public.request_analytics_coverage_gaps WHERE ctid IN (SELECT ctid FROM public.request_analytics_coverage_gaps WHERE gap_until < now()-$2::integer*interval '1 day' ORDER BY gap_until LIMIT $1)`,
}

func RetentionOnceAnalyticsCoverage(ctx context.Context, db retentionExecer) (int64, error) {
	days := 0
	for _, plan := range []api.Plan{api.PlanFree, api.PlanHobby, api.PlanPro, api.PlanScale} {
		days = max(days, api.MustLimitsFor(plan).DebugTelemetryRetentionDays)
	}
	var total int64
	for _, query := range analyticsCoverageRetentionSQL {
		for batch := 0; batch < MaxRetentionBatches; batch++ {
			n, err := db.Exec(ctx, query, RetentionBatchSize, days)
			if err != nil {
				return total, fmt.Errorf("analytics coverage retention: %w", err)
			}
			total += n
			if n < RetentionBatchSize {
				break
			}
			if batch == MaxRetentionBatches-1 {
				return total, ErrRetentionBatchCap
			}
		}
	}
	return total, nil
}
