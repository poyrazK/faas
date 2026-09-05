package main

import (
	"html/template"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/dashboard/views"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

func usageAppData(rows []state.Usage, apps []state.App, totalGBHours float64) []dashboard.UsageAppData {
	slugByApp := make(map[string]string, len(apps))
	for _, app := range apps {
		slugByApp[app.ID] = app.Slug
	}

	out := make([]dashboard.UsageAppData, 0, len(rows))
	for _, row := range rows {
		slug := slugByApp[row.AppID]
		linkable := slug != ""
		if slug == "" {
			// A just-deleted app can still have current-month usage. Keep
			// the row visible without exposing an internal UUID as a label.
			slug = "deleted app"
		}
		used := float64(row.MBSeconds) / 3_600_000.0
		share := 0.0
		if totalGBHours > 0 {
			share = used / totalGBHours * 100
		}
		out = append(out, dashboard.UsageAppData{
			Slug:        slug,
			Linkable:    linkable,
			UsedGBHours: used,
			SharePct:    share,
			Requests:    row.Requests,
			CPUHours:    meter.CPUHours(row.CPUUsec),
			EgressGB:    float64(row.TXBytes+row.NetTxBytes) / (1024 * 1024 * 1024),
			IngressGB:   float64(row.NetRxBytes) / (1024 * 1024 * 1024),
			ColdBoots:   row.ColdBootCount,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UsedGBHours != out[j].UsedGBHours {
			return out[i].UsedGBHours > out[j].UsedGBHours
		}
		return out[i].Slug < out[j].Slug
	})
	return out
}

func usageDailyView(rows []state.DailyUsage, apps []state.App) ([]dashboard.UsageDailyPoint, template.HTML) {
	points := usageDailyPoints(rows, apps)
	out := make([]dashboard.UsageDailyPoint, 0, len(points))
	sparkline := make([]appmetrics.SparklinePoint, 0, len(points))
	for _, point := range points {
		out = append(out, dashboard.UsageDailyPoint{
			Date:          point.Date,
			GBHours:       point.GBHours,
			TopAppSlug:    point.TopAppSlug,
			TopAppGBHours: point.TopAppGBHours,
		})
		at, err := time.Parse("2006-01-02", point.Date)
		if err == nil {
			sparkline = append(sparkline, appmetrics.SparklinePoint{Time: at.UTC(), Value: point.GBHours})
		}
	}
	return out, views.RenderUsageSparkline(sparkline, 480, 100)
}
