package api

import (
	"context"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/routeresources"
)

// Route resource peaks (ADR-20261121170000) attribute an app's memory and
// CPU peaks to the routes in flight in the same instance-minute, so a
// customer can see that one route (an export, a PDF render) sets the size
// the whole app runs at. guest-init samples the app's cgroup, vmmd folds the
// samples into wall-clock minutes per instance, the gateway counts each
// instance's route activity per minute, and apid joins the two.
const (
	// RouteResourceSampleInterval is how often guest-init reads the app
	// cgroup's memory.current and cpu.stat.
	RouteResourceSampleInterval = time.Second
	// RouteResourceSlotInterval is how often guest-init sends what it saw.
	// vmmd stamps each slot with its own clock, so guest clock skew after a
	// snapshot restore cannot move a peak into another minute by more than
	// one slot.
	RouteResourceSlotInterval = 10 * time.Second
	// RouteResourceFlushInterval is how often vmmd and the gateway send
	// closed minutes to apid.
	RouteResourceFlushInterval = 15 * time.Second
	// RouteResourceMinuteGrace is how long after a minute closes vmmd and
	// the gateway wait for late slots or requests before sending it.
	RouteResourceMinuteGrace = 15 * time.Second
	// RouteResourceDisabledBackoff is how long vmmd stops forwarding an
	// app's samples after apid answered that the app is not enabled.
	RouteResourceDisabledBackoff = 5 * time.Minute

	// RouteResourceGuestFrameMaxBytes bounds one guest slot frame.
	RouteResourceGuestFrameMaxBytes = 256
	// RouteResourceBatchRowsMax bounds the rows of one apid write.
	RouteResourceBatchRowsMax = 500
	// RouteResourcePendingRowsMax bounds the rows vmmd or one gateway keeps
	// unsent; past it new minutes are dropped and counted.
	RouteResourcePendingRowsMax = 20000
	// RouteResourceRoutesPerInstanceMinuteMax bounds the distinct routes the
	// gateway keeps per instance-minute. Later routes fold into
	// RouteResourceOtherRoute, so a burst of distinct paths cannot grow the
	// buffer; route templates keep the count small in practice.
	RouteResourceRoutesPerInstanceMinuteMax = 64
	// RouteResourceOtherRoute holds the routes folded past the bound.
	RouteResourceOtherRoute = "__other__"

	// RouteResourceWindowMax is the longest window the read API analyses.
	RouteResourceWindowMax = 14 * 24 * time.Hour
	// RouteResourceWindowDefault is the read API's window when none is given.
	RouteResourceWindowDefault = 24 * time.Hour
	// RouteResourceReadRowsMax bounds the instance-minutes one read loads.
	RouteResourceReadRowsMax = 50000
	// RouteResourceRetention is how long instance-minutes are kept.
	RouteResourceRetention = 14 * 24 * time.Hour
)

// RouteResourcesResponse is GET /v1/apps/{slug}/analytics/route-resources:
// which routes an app's memory and CPU peaks come from over a window.
type RouteResourcesResponse struct {
	Slug  string `json:"slug"`
	From  string `json:"from"`
	Until string `json:"until"`
	// Truncated reports that the window held more instance-minutes than
	// one read analyses; the newest ones were used.
	Truncated bool `json:"truncated"`
	routeresources.Report
	// Split, when present, suggests moving one heavy route to its own app.
	Split *routeresources.Split `json:"split,omitempty"`
}

// RouteResourcesOptions selects the window. Since is a duration back from
// now (24h by default, at most 14 days).
type RouteResourcesOptions struct {
	Since string
}

// GetAppRouteResources reads which routes an app's memory and CPU peaks
// come from.
func (c *Client) GetAppRouteResources(ctx context.Context, slug string, opts RouteResourcesOptions) (RouteResourcesResponse, error) {
	var out RouteResourcesResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/analytics/route-resources"
	if opts.Since != "" {
		path += "?" + url.Values{"since": {opts.Since}}.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}
