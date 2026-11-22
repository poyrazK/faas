// Package routepeaks serves GET /v1/apps/{slug}/analytics/route-resources
// (ADR-20261121170000): it reads an app's instance-minutes and route minutes,
// joins them with pkg/routeresources and sizes a split suggestion.
package routepeaks

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/routeresources"
	"github.com/onebox-faas/faas/pkg/state"
)

// ReadTimeout bounds the two reads one request makes.
const ReadTimeout = 5 * time.Second

// routeRowsPerResourceRow bounds the route rows read per instance-minute:
// the gateway keeps at most RouteResourceRoutesPerInstanceMinuteMax.
const routeRowsPerResourceRow = 4

// FlagSubject is the subject the route_resource_peaks flag is decided for:
// account and plan only, as the gateway decides it for the route half.
func FlagSubject(accountID, plan string) platformflags.Subject {
	return platformflags.Subject{AccountID: accountID, Plan: plan}
}

// Window parses the since parameter.
func Window(since string) (time.Duration, error) {
	if since == "" {
		return api.RouteResourceWindowDefault, nil
	}
	d, err := time.ParseDuration(since)
	if err != nil || d < time.Minute {
		return 0, fmt.Errorf("since must be a duration of at least 1m, such as 24h")
	}
	if d > api.RouteResourceWindowMax {
		return 0, fmt.Errorf("since must be at most %s", api.RouteResourceWindowMax)
	}
	return d, nil
}

// Read loads and joins the app's minutes in [from, until).
//
// Both reads return newest rows first and may stop at their row bound. The
// analysed window then starts after the oldest minute either read reached,
// and both sides drop everything before it, so a minute never counts as
// unsampled or idle only because the other read stopped earlier; the
// boundary minute itself may be partly read and is dropped too.
func Read(ctx context.Context, store state.RouteResourceStore, appID uuid.UUID, from, until time.Time) (api.RouteResourcesResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, ReadTimeout)
	defer cancel()
	resources, resTruncated, err := store.InstanceResourceMinutes(ctx, appID, from, until, api.RouteResourceReadRowsMax)
	if err != nil {
		return api.RouteResourcesResponse{}, err
	}
	if resTruncated && len(resources) > 0 {
		from = later(from, resources[len(resources)-1].Minute.Add(time.Minute))
	}
	routes, routesTruncated, err := store.InstanceRouteMinutes(ctx, appID, from, until, api.RouteResourceReadRowsMax*routeRowsPerResourceRow)
	if err != nil {
		return api.RouteResourcesResponse{}, err
	}
	if routesTruncated && len(routes) > 0 {
		from = later(from, routes[len(routes)-1].Minute.Add(time.Minute))
	}
	resources = keepFrom(resources, from, func(m routeresources.InstanceMinute) time.Time { return m.Minute })
	routes = keepFrom(routes, from, func(m routeresources.RouteMinute) time.Time { return m.Minute })
	return api.RouteResourcesResponse{
		From: from.UTC().Format(time.RFC3339), Until: until.UTC().Format(time.RFC3339),
		Truncated: resTruncated || routesTruncated,
		Report:    routeresources.Analyze(resources, routes, routeresources.Options{}),
	}, nil
}

// SplitFor sizes a split against the app's current RAM and the largest app
// its owner's plan allows (ADR-20261121170000 amendment 1).
func SplitFor(ctx context.Context, owners state.AppOwnerReader, app state.App, rep routeresources.Report) *routeresources.Split {
	planMax := 0
	current := app.RAMMB
	if owner, err := state.AppResourceAccount(ctx, owners, app); err == nil {
		limits := api.MustLimitsFor(owner.Plan)
		planMax = limits.RAMMB
		if current <= 0 {
			current = api.StandaloneAppDefaultRAMMB(limits)
		}
	}
	return routeresources.SuggestSplit(rep, current, planMax, routeresources.SplitOptions{})
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func keepFrom[T any](rows []T, from time.Time, minute func(T) time.Time) []T {
	out := rows[:0]
	for _, r := range rows {
		if !minute(r).Before(from) {
			out = append(out, r)
		}
	}
	return out
}
