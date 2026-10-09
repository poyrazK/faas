package main

import "github.com/onebox-faas/faas/pkg/api"

// Links are derived at read time; fixed windows remain frozen in the parent.
func linkedProfileRouteChecks(slug string, a, b api.ProfileQuery, checks []api.ProfileRouteRegression) []api.ProfileRouteRegression {
	out := append([]api.ProfileRouteRegression(nil), checks...)
	for i := range out {
		out[i].ComparisonURL = api.ProfileRouteComparisonURL(slug, a, b, out[i])
	}
	return out
}
