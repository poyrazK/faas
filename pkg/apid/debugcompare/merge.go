package debugcompare

import (
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

func merge(source, mirror map[string]Stats) []api.DebugCompareRouteStats {
	routes := make(map[string]api.DebugCompareRouteStats, len(source)+len(mirror))
	for route, s := range source {
		routes[route] = api.DebugCompareRouteStats{Route: route, SourceP50: s.P50, SourceP95: s.P95, SourceP99: s.P99, SourceN: s.N}
	}
	for route, m := range mirror {
		row := routes[route]
		row.Route, row.MirrorP50, row.MirrorP95, row.MirrorP99, row.MirrorN = route, m.P50, m.P95, m.P99, m.N
		routes[route] = row
	}
	out := make([]api.DebugCompareRouteStats, 0, len(routes))
	for _, row := range routes {
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Route < out[j].Route })
	return out
}
