package objectstorage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const DefaultUsageExportInterval = 5 * time.Minute

// UsageExportJob binds one immutable backend placement to its operator-owned
// report file. Jobs are intentionally provider-neutral; the exporter carries
// the provider-specific API and attribution rules.
type UsageExportJob struct {
	BackendID          string
	BackendFingerprint string
	ReportPath         string
	Exporter           UsageReportExporter
	// CoverageLag accounts for provider sources that arrive asynchronously,
	// such as OVH server access logs. The exported observation is moved back
	// by this duration, and the report belongs to the month that observation
	// falls in.
	CoverageLag time.Duration
}

// RunUsageExportsOnce refreshes every configured backend report for the
// current UTC month. A failed job never replaces its previous report, so apid
// continues to see the last known cumulative observation and eventually
// fails closed when it becomes stale.
func RunUsageExportsOnce(ctx context.Context, jobs []UsageExportJob, now time.Time) error {
	if now.IsZero() {
		return ErrInvalid
	}
	return runUsageExports(ctx, jobs, now, nil)
}

func runUsageExports(ctx context.Context, jobs []UsageExportJob, now time.Time, observe func(string, string, error)) error {
	now = now.UTC()
	var failures []error
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if job.CoverageLag < 0 {
			err := ErrInvalid
			if observe != nil {
				observe(job.BackendID, "failed", err)
			}
			failures = append(failures, fmt.Errorf("backend %s: %w", job.BackendID, err))
			continue
		}
		err := exportUsageJob(ctx, job, now)
		if err != nil {
			if observe != nil {
				observe(job.BackendID, "failed", err)
			}
			failures = append(failures, fmt.Errorf("backend %s: %w", job.BackendID, err))
			continue
		}
		if observe != nil {
			observe(job.BackendID, "success", nil)
		}
	}
	return errors.Join(failures...)
}

// exportUsageJob writes one backend's reports for the month its lagged
// observation falls in. A report counts only toward the month it is observed
// in, and the runner used to report only the clock's month, clamping the
// observation to its first instant. The previous month's last CoverageLag of
// usage was therefore never reported, and month close billed a month short by
// that much (Hunt 19 H19-2). For ObjectStorageReportCloseWindow into a month,
// the file also carries the previous month's closing report, observed at its
// last microsecond: the timestamp column keeps microseconds, so a later
// instant would round into the next month.
func exportUsageJob(ctx context.Context, job UsageExportJob, now time.Time) error {
	observedAt := now.Add(-job.CoverageLag)
	period := utcMonth(observedAt)
	requests := []UsageReportExportRequest{{
		BackendID: job.BackendID, BackendFingerprint: job.BackendFingerprint,
		PeriodStart: period, ObservedAt: observedAt,
	}}
	if observedAt.Sub(period) < api.ObjectStorageReportCloseWindow {
		requests = append(requests, UsageReportExportRequest{
			BackendID: job.BackendID, BackendFingerprint: job.BackendFingerprint,
			PeriodStart: period.AddDate(0, -1, 0), ObservedAt: period.Add(-time.Microsecond),
		})
	}
	return ExportUsageReportSet(ctx, job.ReportPath, job.Exporter, requests...)
}

// RunUsageExports keeps provider reports fresh until ctx is cancelled. The
// first run is immediate so enabling a backend cannot wait for the first
// interval tick before accounting becomes available.
func RunUsageExports(ctx context.Context, jobs func() []UsageExportJob, interval time.Duration, now func() time.Time, observe func(string, string, error)) {
	if interval <= 0 {
		interval = DefaultUsageExportInterval
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	run := func() {
		_ = runUsageExports(ctx, jobs(), now(), observe)
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
