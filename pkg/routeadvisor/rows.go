package routeadvisor

import (
	"strings"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RoutesFromStats maps the advice statistics query to RouteStats; the route
// label "METHOD /path" becomes the path. Shared by the advice endpoint and the
// route insights evaluator (ADR-1198).
func RoutesFromStats(rows []sqlc.RouteAdviceRouteStatsRow) []RouteStats {
	out := make([]RouteStats, 0, len(rows))
	for _, row := range rows {
		out = append(out, RouteStats{
			Method: row.Method, Path: strings.TrimPrefix(row.Route, row.Method+" "),
			Requests: row.Requests, AnonymousRequests: row.AnonymousRequests, AnonymousSuccess: row.AnonymousSuccess,
			ServerErrors: row.ServerErrors, Timeouts: row.Timeouts, ColdBoots: row.ColdBoots,
			P95LatencyMs: int(row.P95LatencyMs), CacheHits: row.CacheHits, WakesAvoided: row.WakesAvoided,
			Content: ContentStats{
				First: row.ContentFirst, Uncacheable: row.ContentUncacheable,
				Same:    []int64{row.ContentSame60, row.ContentSame300, row.ContentSame3600},
				Changed: []int64{row.ContentChanged60, row.ContentChanged300, row.ContentChanged3600},
			},
		})
	}
	return out
}
