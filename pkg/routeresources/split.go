package routeresources

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

// Split kinds (ADR-20261121170000 amendment 2).
const (
	// SplitMemory moves a route that sizes the app's memory; both apps get
	// sized for what they need.
	SplitMemory = "memory"
	// SplitCPU moves a route whose CPU bursts slow the app's other routes.
	// Billing is plan RAM, so both apps keep the current size.
	SplitCPU = "cpu"
)

// Split suggests moving one heavy route to its own app (ADR-20261121170000
// amendments 1 and 2): one route holds most of the app's memory or CPU peaks
// while it runs in few minutes, so the whole app is shaped by a path most
// requests never take.
type Split struct {
	// Kind is SplitMemory or SplitCPU.
	Kind   string `json:"kind"`
	Route  string `json:"route"`
	Method string `json:"method,omitempty"`
	// PathGlob is the edge-rule match for the route: path parameters become
	// one-segment wildcards.
	PathGlob string `json:"path_glob"`
	// RouteRAMMB sizes the new app for the route; RouteRAMOverPlan is set
	// when that is above the plan's largest app.
	RouteRAMMB       int  `json:"route_ram_mb"`
	RouteRAMOverPlan bool `json:"route_ram_over_plan,omitempty"`
	// CurrentRAMMB is the app's size now; MainRAMMB what it needs without the
	// route.
	CurrentRAMMB int `json:"current_ram_mb"`
	MainRAMMB    int `json:"main_ram_mb"`
	// Evidence copied from the route's report row.
	HighMemoryShare   float64 `json:"high_memory_share"`
	PresenceShare     float64 `json:"presence_share"`
	HighMemoryMinutes int     `json:"high_memory_minutes"`
	// CPU evidence, set on CPU splits.
	HighCPUShare   float64 `json:"high_cpu_share,omitempty"`
	HighCPUMinutes int     `json:"high_cpu_minutes,omitempty"`
	Reason         string  `json:"reason"`
}

// SplitOptions tune the detector. Zero values take the defaults.
type SplitOptions struct {
	// MinPeakShare of attributed memory peaks the route must hold.
	MinPeakShare float64
	// MaxPresence of active minutes the route may run in.
	MaxPresence float64
	// MinPeaks the route must hold, so one burst is not a pattern.
	MinPeaks int
	// Headroom is added to each measured need before rounding.
	Headroom float64
	// MinRAMMB is the smallest size suggested.
	MinRAMMB int
}

const (
	defaultMinPeakShare = 0.8
	defaultMaxPresence  = 0.3
	defaultMinPeaks     = 3
	defaultHeadroom     = 0.25
	defaultMinRAMMB     = 128
	ramStepMB           = 64
)

func (o SplitOptions) withDefaults() SplitOptions {
	if o.MinPeakShare <= 0 || o.MinPeakShare > 1 {
		o.MinPeakShare = defaultMinPeakShare
	}
	if o.MaxPresence <= 0 || o.MaxPresence > 1 {
		o.MaxPresence = defaultMaxPresence
	}
	if o.MinPeaks <= 0 {
		o.MinPeaks = defaultMinPeaks
	}
	if o.Headroom <= 0 {
		o.Headroom = defaultHeadroom
	}
	if o.MinRAMMB <= 0 {
		o.MinRAMMB = defaultMinRAMMB
	}
	return o
}

// SuggestSplit returns the split worth making for an app currently at
// currentRAMMB whose plan allows apps up to planMaxRAMMB, or nil.
//
// The main app's need is the median peak plus the p95 rise in minutes
// without the route, not the p95 peak without it: an app often keeps the
// memory the route raised for minutes afterwards, and those minutes would
// otherwise size the main app for the route it no longer serves.
//
// A memory split wins; a CPU split is suggested only when none qualifies.
func SuggestSplit(rep Report, currentRAMMB, planMaxRAMMB int, opts SplitOptions) *Split {
	opts = opts.withDefaults()
	if s := suggestMemorySplit(rep, currentRAMMB, planMaxRAMMB, opts); s != nil {
		return s
	}
	return suggestCPUSplit(rep, currentRAMMB, opts)
}

func suggestMemorySplit(rep Report, currentRAMMB, planMaxRAMMB int, opts SplitOptions) *Split {
	for _, r := range rep.Routes {
		if isFoldedRoute(r.Route) || r.HighMemoryMinutes < opts.MinPeaks ||
			r.HighMemoryShare < opts.MinPeakShare || r.PresenceShare > opts.MaxPresence {
			continue
		}
		mainNeed := rep.MemPeakP50MB + r.MemRiseP95WithoutMB
		s := &Split{
			Kind:  SplitMemory,
			Route: r.Route, Method: r.Method, PathGlob: PathGlob(r.Route),
			RouteRAMMB:      sizeFor(r.MemPeakP95WithMB, opts),
			CurrentRAMMB:    currentRAMMB,
			MainRAMMB:       sizeFor(mainNeed, opts),
			HighMemoryShare: r.HighMemoryShare, PresenceShare: r.PresenceShare,
			HighMemoryMinutes: r.HighMemoryMinutes,
		}
		if currentRAMMB > 0 && s.MainRAMMB >= currentRAMMB {
			// The rest of the app needs its current size anyway.
			continue
		}
		if planMaxRAMMB > 0 && s.RouteRAMMB > planMaxRAMMB {
			s.RouteRAMOverPlan = true
		}
		s.Reason = fmt.Sprintf("%s was in flight for %d of the app's %d memory peaks (%.0f%%) but ran in only %.0f%% of minutes; "+
			"without it the app peaks near %d MB", r.Route, r.HighMemoryMinutes, rep.HighMemoryMinutes, r.HighMemoryShare*100, r.PresenceShare*100, mainNeed)
		return s
	}
	return nil
}

// suggestCPUSplit picks a route holding most CPU peaks while running in few
// minutes. Both apps keep the current size: the split isolates the route
// rather than saving memory.
func suggestCPUSplit(rep Report, currentRAMMB int, opts SplitOptions) *Split {
	for _, r := range rep.Routes {
		if isFoldedRoute(r.Route) || r.HighCPUMinutes < opts.MinPeaks ||
			r.HighCPUShare < opts.MinPeakShare || r.PresenceShare > opts.MaxPresence {
			continue
		}
		return &Split{
			Kind: SplitCPU, Route: r.Route, Method: r.Method, PathGlob: PathGlob(r.Route),
			RouteRAMMB: currentRAMMB, CurrentRAMMB: currentRAMMB, MainRAMMB: currentRAMMB,
			HighMemoryShare: r.HighMemoryShare, PresenceShare: r.PresenceShare,
			HighMemoryMinutes: r.HighMemoryMinutes,
			HighCPUShare:      r.HighCPUShare, HighCPUMinutes: r.HighCPUMinutes,
			Reason: fmt.Sprintf("%s was in flight for %d of the app's %d CPU peaks (%.0f%%) but ran in only %.0f%% of minutes; "+
				"on its own app its bursts stop slowing the app's other routes",
				r.Route, r.HighCPUMinutes, rep.HighCPUMinutes, r.HighCPUShare*100, r.PresenceShare*100),
		}
	}
	return nil
}

func sizeFor(needMB int, opts SplitOptions) int {
	withHeadroom := int(math.Ceil(float64(needMB) * (1 + opts.Headroom)))
	rounded := (withHeadroom + ramStepMB - 1) / ramStepMB * ramStepMB
	return max(rounded, opts.MinRAMMB)
}

func isFoldedRoute(route string) bool {
	return route == "" || strings.HasPrefix(route, "__")
}

var pathParam = regexp.MustCompile(`\{[^/{}]*\}|:[A-Za-z_][A-Za-z0-9_]*`)

// PathGlob turns a route template (GET /exports/{id}) into an
// edge-rule path glob (/exports/*). A route without a path, or one
// already ending in a wildcard, is returned as is.
func PathGlob(route string) string {
	path := route
	if i := strings.IndexByte(route, ' '); i >= 0 && !strings.HasPrefix(route, "/") {
		path = route[i+1:]
	}
	if !strings.HasPrefix(path, "/") {
		return path
	}
	return pathParam.ReplaceAllString(path, "*")
}
