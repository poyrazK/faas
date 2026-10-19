package routeadvisor

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ContentStats counts a route's observed anonymous GET responses (ADR-1188).
// Same and Changed are indexed like api.RouteContentWindows and are not
// cumulative: Same[i] counts identical responses whose previous response for
// the URL was seen within window i but not within window i-1.
type ContentStats struct {
	First       int64
	Uncacheable int64
	Same        []int64
	Changed     []int64
	// Stale counts refills under a cache rule whose body changed within the
	// rule's max-age (ADR-1199).
	Stale int64
}

// ContentVerdict is what the observed responses say about caching a route.
type ContentVerdict struct {
	Observed    int64
	Repeats     int64 // identical within the longest window
	Changes     int64 // changed within the longest window
	Uncacheable int64
	// SafeMaxAge is the longest window in which at least
	// RouteContentMinRepeats repeats were observed, at least
	// RouteContentMinBandRepeats of them at that window's spacing, and at most
	// RouteContentMaxStaleShare of them had changed; 0 when none qualifies.
	// Repeats seen only seconds apart say nothing about an hour-long cache.
	SafeMaxAge time.Duration
	// Unsafe explains why a cache rule should not be suggested at all.
	Unsafe string
}

// Verdict judges a route's content evidence.
func (c ContentStats) Verdict() ContentVerdict {
	v := ContentVerdict{Uncacheable: c.Uncacheable, Observed: c.First + c.Uncacheable}
	windows := min(len(api.RouteContentWindows), len(c.Same), len(c.Changed))
	var hits, stale int64
	for i := 0; i < windows; i++ {
		hits += c.Same[i]
		stale += c.Changed[i]
		band := c.Same[i] + c.Changed[i]
		if hits+stale >= api.RouteContentMinRepeats && (i == 0 || band >= api.RouteContentMinBandRepeats) &&
			share(stale, hits+stale) <= api.RouteContentMaxStaleShare {
			v.SafeMaxAge = api.RouteContentWindows[i]
		}
	}
	v.Repeats, v.Changes = hits, stale
	v.Observed += hits + stale
	switch {
	case v.Observed >= api.RouteContentMinRepeats && share(v.Uncacheable, v.Observed) > api.RouteContentMaxUncacheableShare:
		v.Unsafe = "most responses cannot be stored by the edge cache (Set-Cookie, Cache-Control: no-store or private, Vary, or a body over the cache entry size)"
	case windows > 0 && c.Same[0]+c.Changed[0] >= api.RouteContentMinRepeats &&
		share(c.Changed[0], c.Same[0]+c.Changed[0]) > api.RouteContentUnsafeStaleShare:
		v.Unsafe = "the response changes between requests less than a minute apart"
	}
	return v
}
