package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type periodUsageExporter struct {
	exporter objectstorage.UsageReportExporter
}

func (e periodUsageExporter) ExportUsageReports(ctx context.Context, req objectstorage.UsageReportExportRequest) ([]api.ObjectStorageUsageReport, error) {
	return e.exporter.ExportUsageReports(state.WithObjectUsagePeriod(ctx, req.PeriodStart), req)
}
