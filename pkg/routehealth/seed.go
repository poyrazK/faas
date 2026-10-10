package routehealth

import (
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// RankedRoute is one observed route label with the selector it maps to.
type RankedRoute struct {
	Usage         api.RouteCustomerUsage
	Selector      api.RouteHealthRoute
	CustomerCount int64
}

// RankRouteUsage orders observed routes by distinct customer groups (tenant,
// or consumer when groupBy is "consumer"), then observed requests, then
// method/path. Rows without traffic, or whose label is not a valid exact
// selector, are skipped; duplicate labels keep their first row.
func RankRouteUsage(rows []api.RouteCustomerUsage, groupBy string) []RankedRoute {
	zero := int64(0)
	seen := map[[2]string]bool{}
	ranked := make([]RankedRoute, 0, len(rows))
	for _, row := range rows {
		path, ok := strings.CutPrefix(row.Route, row.Method+" ")
		if !ok || row.Requests <= 0 {
			continue
		}
		selector := api.RouteHealthRoute{Method: row.Method, Path: path}
		key := [2]string{selector.Method, selector.Path}
		if seen[key] || Validate(api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{selector}}) != nil {
			continue
		}
		seen[key] = true
		count := row.PlatformTenantCount
		if groupBy == "consumer" {
			count = row.ConsumerCount
		}
		ranked = append(ranked, RankedRoute{Usage: row, Selector: selector, CustomerCount: count})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if a.CustomerCount != b.CustomerCount {
			return a.CustomerCount > b.CustomerCount
		}
		if a.Usage.Requests != b.Usage.Requests {
			return a.Usage.Requests > b.Usage.Requests
		}
		if a.Selector.Method != b.Selector.Method {
			return a.Selector.Method < b.Selector.Method
		}
		return a.Selector.Path < b.Selector.Path
	})
	return ranked
}

// SeedSelectors returns at most limit report-mode selectors ranked by tenant
// reach. The result is never nil so it can be saved directly.
func SeedSelectors(rows []api.RouteCustomerUsage, limit int) []api.RouteHealthRoute {
	ranked := RankRouteUsage(rows, "tenant")
	if limit > api.RouteHealthMaxRoutes {
		limit = api.RouteHealthMaxRoutes
	}
	if len(ranked) > limit {
		ranked = ranked[:max(limit, 0)]
	}
	out := make([]api.RouteHealthRoute, 0, len(ranked))
	for _, item := range ranked {
		out = append(out, item.Selector)
	}
	return out
}
