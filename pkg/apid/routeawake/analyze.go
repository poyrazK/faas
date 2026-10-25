// Package routeawake reports which routes keep a scale-to-zero app awake
// (ADR-1187). Awake time is the app's billed running time per minute; it is
// attributed to the routes whose requests restarted the idle timer, and for
// each busy route the report estimates how much awake time, and how many cold
// wakes, the app would have had without that route's requests. Awake time no
// request explains is credited to route probes and scheduled prewarms when
// one of them kept the app awake (ADR-1199), and to cron, queue and other
// deliveries schedd made to the app (ADR-20261022184757). waitUntil work
// counts toward the request that registered it.
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

// SourceMinute is one minute of an awake cause other than customer requests
// (ADR-1199): route probes the app answered, instances a scheduled prewarm
// admitted, or deliveries schedd made to the app (ADR-20261022184757).
type SourceMinute struct {
	At     time.Time
	Source string
	// Detail tells deliveries of one source apart: a cron job's id or a
	// queue name. Name is how the report shows it.
	Detail string
	Name   string
	// Events started in the minute: probe requests, admitted instances or
	// deliveries.
	Events int64
	// Running counts deliveries in progress during the minute, including
	// ones that started earlier; zero means Events.
	Running int64
}

// Inputs are the window, the app's settings and its minutes.
type Inputs struct {
	App          string
	From, Until  time.Time
	IdleTimeoutS int
	// TailTimeoutS is the plan's waitUntil task timeout, zero when the plan
	// does not enable waitUntil. An instance parks no earlier than the later
	// of the idle timeout and its last waitUntil task.
	TailTimeoutS int
	RAMMB        int
	Awake        []Minute
	Routes       []RouteMinute
	Sources      []SourceMinute
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

// source accumulates one non-request cause; sourceHits index sources.
type source struct {
	name, detail, label string
	events              int64
	attributed          int64
	active              []int // minute indexes in which events started
	perMinute           []int64
}

type timeline struct {
	in         Inputs
	minutes    int
	keep       int // minutes a request keeps the app awake after its own minute
	mbs        []int64
	hits       [][]hit
	routes     []*route
	other      int
	credited   []bool // awake minutes some request explains
	sourceHits [][]hit
	sources    []*source
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
	// Wakes are the requests that woke the app, the same count as the route
	// rows. usage_minutes only counts restore-to-cold-boot fallbacks
	// (ADR-048), so with snapshots working the header read "0 wakes" above
	// routes that woke it (Hunt 14 H14-18); it still wins when larger.
	var requestWakes, meteredColdBoots int64
	for _, m := range in.Routes {
		if _, ok := t.index(m.At); ok {
			requestWakes += m.ColdBoots
		}
	}
	for _, m := range in.Awake {
		if _, ok := t.index(m.At); ok {
			meteredColdBoots += m.ColdBoots
		}
	}
	out.Wakes = max(requestWakes, meteredColdBoots)
	unattributed := t.attribute()
	out.Sources = []api.RouteAwakeSource{}
	for _, src := range t.sources {
		out.Sources = append(out.Sources, t.sourceReport(src, out.MBSeconds))
	}
	slices.SortStableFunc(out.Sources, func(a, b api.RouteAwakeSource) int {
		return cmp.Or(cmp.Compare(b.AwakeSeconds, a.AwakeSeconds), cmp.Compare(a.Source, b.Source), cmp.Compare(a.Name, b.Name))
	})
	if in.TailTimeoutS > 0 {
		out.WaitUntilTimeoutSeconds = in.TailTimeoutS
	}
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
	t := &timeline{in: in, minutes: n, keep: (max(in.IdleTimeoutS, in.TailTimeoutS)+59)/60 + 1,
		mbs: make([]int64, n), hits: make([][]hit, n), credited: make([]bool, n), sourceHits: make([][]hit, n)}
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
	sourceIndex := map[string]int{}
	for _, m := range in.Sources {
		weight := m.Running
		if weight == 0 {
			weight = m.Events
		}
		i, ok := t.index(m.At)
		if !ok || weight <= 0 {
			continue
		}
		key := m.Source + "\x00" + m.Detail
		idx, seen := sourceIndex[key]
		if !seen {
			idx = len(t.sources)
			sourceIndex[key] = idx
			t.sources = append(t.sources, &source{name: m.Source, detail: m.Detail, label: m.Name})
		}
		src := t.sources[idx]
		src.events += max(0, m.Events)
		if m.Events > 0 {
			if n := len(src.active); n > 0 && src.active[n-1] == i {
				src.perMinute[n-1] += m.Events
			} else {
				src.active = append(src.active, i)
				src.perMinute = append(src.perMinute, m.Events)
			}
		}
		t.sourceHits[i] = append(t.sourceHits[i], hit{route: idx, requests: weight})
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
// that still keeps the app awake, split by request count. Customer requests
// come first; a minute none of them explains goes to the latest probe or
// prewarm minute that still keeps the app awake. It returns the MB-seconds
// nothing explains.
func (t *timeline) attribute() int64 {
	var unattributed int64
	last, lastSource := -1, -1
	for i, mbs := range t.mbs {
		if len(t.hits[i]) > 0 {
			last = i
		}
		if len(t.sourceHits[i]) > 0 {
			lastSource = i
		}
		if mbs == 0 {
			continue
		}
		switch {
		case last >= 0 && i-last <= t.keep:
			t.credited[i] = true
			split(mbs, t.hits[last], func(i int, part int64) { t.routes[i].attributed += part })
		case lastSource >= 0 && i-lastSource <= t.keep:
			split(mbs, t.sourceHits[lastSource], func(i int, part int64) { t.sources[i].attributed += part })
		default:
			unattributed += mbs
		}
	}
	return unattributed
}

// split divides mbs across hits by request count; the remainder goes to the
// first hit so the parts always add up.
func split(mbs int64, hits []hit, credit func(int, int64)) {
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
		credit(h.route, part)
		given += part
	}
	credit(hits[0].route, mbs-given)
}

// whatIf estimates the awake MB-seconds saved and the change in wakes had
// route r's requests never reached the app. Other routes, probes and prewarms
// still keep it awake.
func (t *timeline) whatIf(r int) (int64, int64) {
	covered := make([]bool, t.minutes)
	for i, hits := range t.hits {
		if len(t.sourceHits[i]) == 0 && !slices.ContainsFunc(hits, func(h hit) bool { return h.route != r }) {
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
	return steadyInterval(r.active, r.perMinute)
}

// steadyInterval reports the interval between events that arrive in active
// minutes, perMinute events each, when both hold steady.
func steadyInterval(active []int, events []int64) (int, bool) {
	if len(active) < api.RouteAwakePeriodicMinMinutes {
		return 0, false
	}
	gaps := make([]int64, 0, len(active)-1)
	for i := 1; i < len(active); i++ {
		gaps = append(gaps, int64(active[i]-active[i-1]))
	}
	gap, ok := steady(gaps, func(int64) int64 { return 1 })
	if !ok {
		return 0, false
	}
	perMinute, ok := steady(slices.Clone(events), func(m int64) int64 { return max(1, m/10) })
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

// sourceReport is one source's row. A cron job that fires at a steady
// interval inside the idle timeout, and holds a meaningful share of the awake
// time, gets a suggestion to run less often.
func (t *timeline) sourceReport(src *source, totalMBS int64) api.RouteAwakeSource {
	out := api.RouteAwakeSource{Source: src.name, Name: src.label, Events: src.events,
		AwakeSeconds: t.seconds(src.attributed), GBHours: gbHours(src.attributed)}
	if totalMBS > 0 {
		out.AwakeShare = float64(src.attributed) / float64(totalMBS)
	}
	if src.name != api.RouteAwakeSourceCron {
		return out
	}
	out.Pattern = api.RouteAwakePatternIrregular
	interval, ok := steadyInterval(src.active, src.perMinute)
	if !ok {
		return out
	}
	out.Pattern, out.IntervalSeconds = api.RouteAwakePatternPeriodic, interval
	if interval <= t.in.IdleTimeoutS && out.AwakeShare >= api.RouteAwakeSuggestMinSavedShare {
		name := src.label
		if name == "" {
			name = "a cron job"
		}
		out.Suggestion = &api.RouteAwakeSuggestion{Kind: api.RouteAwakeSuggestRunLessOften, Message: fmt.Sprintf(
			"Cron job %s fires about every %s, within the %ds idle timeout, so %s never parks between runs. Running it less often than every %ds lets it park; each run then wakes it.",
			name, time.Duration(interval)*time.Second, t.in.IdleTimeoutS, t.in.App, t.in.IdleTimeoutS)}
	}
	return out
}

func (t *timeline) seconds(mbs int64) int64 {
	return mbs / int64(max(1, t.in.RAMMB+api.PerVMOverheadMB))
}

func gbHours(mbs int64) float64 {
	return float64(mbs) / mbSecondsPerGBHour
}
