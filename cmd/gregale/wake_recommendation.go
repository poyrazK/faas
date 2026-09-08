package main

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const wakeRecommendationWindow = 7 * 24 * time.Hour

type wakeRecommendationStats struct {
	ready []int
}

func renderWakeRecommendation(ctx context.Context, client *api.Client, slug string, app api.AppResponse) {
	now := time.Now().UTC()
	timeline, err := client.GetAppWakeTimeline(ctx, slug, api.AppWakeTimelineOptions{
		Since: now.Add(-wakeRecommendationWindow).Format(time.RFC3339Nano),
		Until: now.Format(time.RFC3339Nano),
	})
	if err != nil || len(timeline.Rows) < 10 {
		if err == nil && len(timeline.Rows) > 0 {
			renderTriggerClassSummary(timeline, app)
		}
		return
	}
	renderTriggerClassSummary(timeline, app)

	stats := make(map[string]*wakeRecommendationStats)
	for _, row := range timeline.Rows {
		if row.ReadyInMS <= 0 {
			continue
		}
		tier := wakeRecommendationTier(row)
		if tier == "" {
			continue
		}
		if stats[tier] == nil {
			stats[tier] = &wakeRecommendationStats{}
		}
		stats[tier].ready = append(stats[tier].ready, int(row.ReadyInMS))
	}

	_, _ = fmt.Fprintf(osStdout, "wake recommendation (last 7d, %d wakes):\n", len(timeline.Rows))
	for _, tier := range []string{"warm", "init", "cold_boot_fallback"} {
		stat := stats[tier]
		if stat == nil || len(stat.ready) == 0 {
			continue
		}
		_, _ = fmt.Fprintf(osStdout, "  %s: p50=%dms p95=%dms (n=%d)\n",
			tier, percentileMS(stat.ready, 0.50), percentileMS(stat.ready, 0.95), len(stat.ready))
	}

	projectedWarm := 0.0
	if stat := stats["warm"]; stat != nil && len(stat.ready) > 0 {
		projectedWarm = float64(percentileMS(stat.ready, 0.50))
	} else if stat := stats["init"]; stat != nil && len(stat.ready) > 0 {
		// Warm snapshots are designed to resume in at most half the
		// init-snapshot p50 (pkg/api/limits.go / ADR-074).
		projectedWarm = float64(percentileMS(stat.ready, 0.50)) * 0.5
	}
	if projectedWarm > 0 {
		_, _ = fmt.Fprintf(osStdout, "  projected warm p50: %.0fms\n", projectedWarm)
	} else {
		_, _ = fmt.Fprintln(osStdout, "  projected warm p50: n/a")
	}

	if acct, err := client.Whoami(ctx); err == nil {
		plan := api.Plan(acct.Plan)
		if cost := ResidentGBHoursPerMonth(plan, app.RAMMB, 1); cost > 0 {
			_, _ = fmt.Fprintf(osStdout, "  min 1 monthly resident: %s\n", formatGBHours(cost))
		} else {
			_, _ = fmt.Fprintf(osStdout, "  min 1 monthly resident: unavailable on %s plan\n", acct.Plan)
		}
	}
}

// renderTriggerClassSummary makes known non-user wake cost visible beside the
// existing latency recommendation. The estimate assumes one idle timeout per
// suppressed wake and uses the platform's published overage rate; it is a
// planning number, not an invoice calculation.
func renderTriggerClassSummary(timeline api.AppWakeTimelineResponse, app api.AppResponse) {
	wakes := len(timeline.Rows)
	if wakes == 0 {
		return
	}
	known := timeline.TriggerClassHistogram["monitor"] + timeline.TriggerClassHistogram["crawler"]
	if known == 0 {
		for _, row := range timeline.Rows {
			if row.TriggerClass == "monitor" || row.TriggerClass == "crawler" {
				known++
			}
		}
	}
	_, _ = fmt.Fprintf(osStdout,
		"  %d of your last %d wakes were monitors/crawlers (≈ €%.2f estimated resident cost)\n",
		known, wakes, estimateCrawlerWakeCostEUR(known, app))
}

func estimateCrawlerWakeCostEUR(wakes int, app api.AppResponse) float64 {
	if wakes <= 0 || app.RAMMB <= 0 || app.IdleTimeoutS <= 0 {
		return 0
	}
	gbHours := float64(app.RAMMB+api.PerVMOverheadMB) * float64(wakes) * float64(app.IdleTimeoutS) / (1024 * 3600)
	return gbHours * float64(api.OverageMillicentsPerGBHour) / 100000
}

func wakeRecommendationTier(row api.WakeTimelineJSONRow) string {
	switch row.Tier {
	case "warm", "init", "cold_boot_fallback":
		return row.Tier
	}
	// Older telemetry has method but no tier. Keep those rows useful
	// during the additive rollout by mapping restore/cold_boot to the
	// corresponding non-warm tier.
	switch row.Method {
	case "restore":
		return "init"
	case "cold_boot":
		return "cold_boot_fallback"
	default:
		return ""
	}
}

func percentileMS(values []int, quantile float64) int {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]int(nil), values...)
	sort.Ints(ordered)
	idx := int(math.Ceil(float64(len(ordered)) * quantile))
	if idx < 1 {
		idx = 1
	}
	if idx > len(ordered) {
		idx = len(ordered)
	}
	return ordered[idx-1]
}
