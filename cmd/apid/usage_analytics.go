package main

import (
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// usageDailyPoints folds per-app daily rollup rows into the account-level
// series exposed by GET /v1/usage/summary. The sort is deliberately done here
// as well as in the store so alternate Store implementations cannot change
// the wire ordering. Ties use app ID as a stable fallback.
func usageDailyPoints(rows []state.DailyUsage, apps []state.App) []api.DailyUsagePoint {
	slugByApp := make(map[string]string, len(apps))
	for _, app := range apps {
		slugByApp[app.ID] = app.Slug
	}

	type aggregate struct {
		point  api.DailyUsagePoint
		topApp string
	}
	byDate := make(map[string]*aggregate)
	for _, row := range rows {
		date := row.Day.UTC().Format("2006-01-02")
		gbHours := float64(row.MBSeconds) / 3_600_000.0
		item := byDate[date]
		if item == nil {
			item = &aggregate{point: api.DailyUsagePoint{Date: date}}
			byDate[date] = item
		}
		item.point.GBHours += gbHours
		if gbHours > item.point.TopAppGBHours ||
			(gbHours == item.point.TopAppGBHours && row.AppID < item.topApp) {
			item.point.TopAppGBHours = gbHours
			item.topApp = row.AppID
			item.point.TopAppSlug = slugByApp[row.AppID]
		}
	}

	out := make([]api.DailyUsagePoint, 0, len(byDate))
	for _, item := range byDate {
		out = append(out, item.point)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}
