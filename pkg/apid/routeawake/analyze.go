// Package routeawake reports which routes keep a scale-to-zero app awake
// (ADR-1187). Awake time is the app's billed running time per minute; it is
// attributed to the routes whose requests restarted the idle timer, and for
// each busy route the report estimates how much awake time, and how many cold
// wakes, the app would have had without that route's requests.
package routeawake

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// mbSecondsPerGBHour converts billed MB-seconds to GB-hours.
const mbSecondsPerGBHour = 1024 * 3600

// Minute is one minute of the app's billed running time, summed over its
// instances.
type Minute struct {
	At        time.Time
	MBSeconds int64
	ColdBoots int64
}

// RouteMinute is one minute of a route's requests that restart the idle
// timer. An empty Method stands for every route outside the busiest ones.
type RouteMinute struct {
	At                time.Time
	Method            string
	Path              string
	Requests          int64
	ColdBoots         int64
	AnonymousRequests int64
}

// Inputs are the window, the app's settings and its minutes.
type Inputs struct {
	App          string
	From, Until  time.Time
	IdleTimeoutS int
	RAMMB        int
	Awake        []Minute
	Routes       []RouteMinute
}

type routeKey struct{ method, path string }

// route accumulates one route's minutes; index len(routes) is "other routes".
type route struct {
	key        routeKey
	requests   int64
	coldBoots  int64
	anonymous  int64
	attributed int64
	active     []int // minute indexes with requests, ascending
	perMinute  []int64
}

type hit struct {
	route    int
	requests int64
}

type timeline struct {
	in       Inputs
	minutes  int
	keep     int // minutes a request keeps the app awake after its own minute
	mbs      []int64
	hits     [][]hit
	routes   []*route
	other    int
	credited []bool // awake minutes some request explains
}

// Analyze computes the report body; the caller fills in the slug and window.
func Analyze(in Inputs) api.RouteAwakeResponse {
	t := newTimeline(in)
	out := api.RouteAwakeResponse{
		IdleTimeoutSeconds: in.IdleTimeoutS,
		WindowSeconds:      int64(in.Until.Sub(in.From) / time.Second),
		RoutesLimit:        api.RouteAwakeMaxRoutes,
		Routes:             []api.RouteAwakeRoute{},
	}
	awakeMinutes := 0
	for _, mbs := range t.mbs {
		out.MBSeconds += mbs
		if mbs > 0 {
			awakeMinutes++
		}
	}
	for _, m := range in.Awake {
		if _, ok := t.index(m.At); ok {
			out.Wakes += m.ColdBoots
		}
	}
	unattributed := t.attribute()
	out.AwakeSeconds = t.seconds(out.MBSeconds)
	out.GBHours = gbHours(out.MBSeconds)
	out.UnattributedSeconds = t.seconds(unattributed)
	if t.minutes > 0 {
		out.AwakeShare = float64(awakeMinutes) / float64(t.minutes)
	}
	for i, r := range t.routes {
		if i == t.other {
			out.OtherRoutesSeconds = t.seconds(r.attributed)
			continue
		}
		out.Routes = append(out.Routes, t.report(i, out.MBSeconds))
	}
	slices.SortStableFunc(out.Routes, func(a, b api.RouteAwakeRoute) int {
		return cmp.Or(cmp.Compare(b.AwakeSeconds, a.AwakeSeconds), cmp.Compare(b.Requests, a.Requests),
			cmp.Compare(a.Route, b.Route), cmp.Compare(a.Method, b.Method))
	})
	return out
}

func newTimeline(in Inputs) *timeline {
	n := 0
	if in.Until.After(in.From) {
		n = int((in.Until.Sub(in.From) + time.Minute - 1) / time.Minute)
	}
	t := &timeline{in: in, minutes: n, keep: (in.IdleTimeoutS+59)/60 + 1,
		mbs: make([]int64, n), hits: make([][]hit, n), credited: make([]bool, n)}
	for _, m := range in.Awake {
		if i, ok := t.index(m.At); ok {
			t.mbs[i] += m.MBSeconds
		}
	}
	index := map[routeKey]int{}
	var other route
	for _, m := range in.Routes {
		i, ok := t.index(m.At)
		if !ok || m.Requests <= 0 {
			continue
		}
		r := &other
		ri := -1
		if m.Method != "" {
			key := routeKey{m.Method, m.Path}
			idx, seen := index[key]
			if !seen {
				idx = len(t.routes)
				index[key] = idx
				t.routes = append(t.routes, &route{key: key})
			}
			r, ri = t.routes[idx], idx
		}
		r.requests += m.Requests
		r.coldBoots += m.ColdBoots
		r.anonymous += m.AnonymousRequests
		if n := len(r.active); n > 0 && r.active[n-1] == i {
			r.perMinute[n-1] += m.Requests
		} else {
			r.active = append(r.active, i)
			r.perMinute = append(r.perMinute, m.Requests)
		}
		t.hits[i] = append(t.hits[i], hit{route: ri, requests: m.Requests})
	}
	t.other = len(t.routes)
	t.routes = append(t.routes, &other)
	for i := range t.hits {
		for j := range t.hits[i] {
			if t.hits[i][j].route < 0 {
				t.hits[i][j].route = t.other
			}
		}
	}
	return t
}

func (t *timeline) index(at time.Time) (int, bool) {
	if at.Before(t.in.From) {
		return 0, false
	}
	i := int(at.Sub(t.in.From) / time.Minute)
	return i, i < t.minutes
}

// attribute credits each awake minute to the requests of the latest minute
// that still keeps the app awake, split by request count, and returns the
// MB-seconds no request explains.
func (t *timeline) attribute() int64 {
	var unattributed int64
	last := -1
	for i, mbs := range t.mbs {
		if len(t.hits[i]) > 0 {
			last = i
		}
		if mbs == 0 {
			continue
		}
		if last < 0 || i-last > t.keep {
			unattributed += mbs
			continue
		}
		t.credited[i] = true
		split(mbs, t.hits[last], t.routes)
	}
	return unattributed
}

// split divides mbs across hits by request count; the remainder goes to the
// first hit so the parts always add up.
func split(mbs int64, hits []hit, routes []*route) {
	var total int64
	for _, h := range hits {
		total += h.requests
	}
	if total == 0 {
		return
	}
	given := int64(0)
	for _, h := range hits {
		part := mbs * h.requests / total
		routes[h.route].attributed += part
		given += part
	}
	routes[hits[0].route].attributed += mbs - given
}

// whatIf estimates the awake MB-seconds saved and the change in wakes had
// route r's requests never reached the app.
func (t *timeline) whatIf(r int) (int64, int64) {
	covered := make([]bool, t.minutes)
	for i, hits := range t.hits {
		if !slices.ContainsFunc(hits, func(h hit) bool { return h.route != r }) {
			continue
		}
		for j := i; j <= i+t.keep && j < t.minutes; j++ {
			covered[j] = true
		}
	}
	var saved int64
	before, after := make([]bool, t.minutes), make([]bool, t.minutes)
	for i, mbs := range t.mbs {
		before[i] = mbs > 0
		after[i] = mbs > 0 && (!t.credited[i] || covered[i])
		if before[i] && !after[i] {
			saved += mbs
		}
	}
	return saved, int64(runs(after) - runs(before))
}

// runs counts the awake stretches that start inside the window, each of
// which began with a wake.
func runs(awake []bool) int {
	n := 0
	for i := 1; i < len(awake); i++ {
		if awake[i] && !awake[i-1] {
			n++
		}
	}
	return n
}

func (t *timeline) report(i int, totalMBS int64) api.RouteAwakeRoute {
	r := t.routes[i]
	saved, wakes := t.whatIf(i)
	out := api.RouteAwakeRoute{
		Method: r.key.method, Route: r.key.path, Requests: r.requests, Wakes: r.coldBoots,
		AwakeSeconds: t.seconds(r.attributed), GBHours: gbHours(r.attributed),
		Pattern:   api.RouteAwakePatternIrregular,
		IfRemoved: api.RouteAwakeWhatIf{AwakeSecondsSaved: t.seconds(saved), GBHoursSaved: gbHours(saved), WakesChange: wakes},
	}
	if totalMBS > 0 {
		out.AwakeShare = float64(r.attributed) / float64(totalMBS)
	}
	if interval, ok := periodic(r); ok {
		out.Pattern, out.IntervalSeconds = api.RouteAwakePatternPeriodic, interval
	}
	if totalMBS > 0 && float64(saved)/float64(totalMBS) >= api.RouteAwakeSuggestMinSavedShare {
		out.Suggestion = t.suggest(r, out)
	}
	return out
}

// periodic reports whether a client calls the route at a steady interval:
// the gaps between its minutes and its requests per minute both hold steady.
// The interval is approximate at minute resolution.
func periodic(r *route) (int, bool) {
	if len(r.active) < api.RouteAwakePeriodicMinMinutes {
		return 0, false
	}
	gaps := make([]int64, 0, len(r.active)-1)
	for i := 1; i < len(r.active); i++ {
		gaps = append(gaps, int64(r.active[i]-r.active[i-1]))
	}
	gap, ok := steady(gaps, func(int64) int64 { return 1 })
	if !ok {
		return 0, false
	}
	perMinute, ok := steady(slices.Clone(r.perMinute), func(m int64) int64 { return max(1, m/10) })
	if !ok || perMinute < 1 {
		return 0, false
	}
	return int(max(1, gap*60/perMinute)), true
}

// steady returns the median of values when enough of them lie within
// tolerance(median) of it.
func steady(values []int64, tolerance func(int64) int64) (int64, bool) {
	if len(values) == 0 {
		return 0, false
	}
	slices.Sort(values)
	median := values[len(values)/2]
	within := 0
	for _, v := range values {
		if d := v - median; d <= tolerance(median) && -d <= tolerance(median) {
			within++
		}
	}
	return median, float64(within)/float64(len(values)) >= api.RouteAwakePeriodicMinShare
}

func (t *timeline) suggest(r *route, out api.RouteAwakeRoute) *api.RouteAwakeSuggestion {
	if out.Pattern != api.RouteAwakePatternPeriodic {
		return nil
	}
	cacheable := (r.key.method == "GET" || r.key.method == "HEAD") && r.requests > 0 &&
		float64(r.anonymous)/float64(r.requests) >= api.RouteAwakeCacheMinAnonymousShare
	every := (time.Duration(out.IntervalSeconds) * time.Second).String()
	if cacheable {
		return &api.RouteAwakeSuggestion{Kind: api.RouteAwakeSuggestEdgeCache, Message: fmt.Sprintf(
			"%s %s is called about every %s by anonymous clients and keeps %s awake. Serve it from the edge cache (`gregale routes advise %s` drafts the rule): a cached response never wakes the app.",
			r.key.method, r.key.path, every, t.in.App, t.in.App)}
	}
	if out.IntervalSeconds > t.in.IdleTimeoutS {
		return nil
	}
	return &api.RouteAwakeSuggestion{Kind: api.RouteAwakeSuggestCallLessOften, Message: fmt.Sprintf(
		"A client calls %s %s about every %s, within the %ds idle timeout, so %s never parks. Calling it less often than every %ds, or pushing changes to the client instead of being polled, lets it park.",
		r.key.method, r.key.path, every, t.in.IdleTimeoutS, t.in.App, t.in.IdleTimeoutS)}
}

func (t *timeline) seconds(mbs int64) int64 {
	return mbs / int64(max(1, t.in.RAMMB+api.PerVMOverheadMB))
}

func gbHours(mbs int64) float64 {
	return float64(mbs) / mbSecondsPerGBHour
}
