// Package routeresources attributes an app's memory and CPU peaks to the
// routes in flight in the same instance-minute (ADR-20261121170000).
//
// Nothing inside a VM sees individual requests, so attribution is a
// correlation over minutes, not a per-request measurement: a route that is
// present in most minutes where the app's memory rose sharply, and absent in
// the quiet ones, is the route that sizes the app.
package routeresources

import (
	"cmp"
	"math"
	"slices"
	"time"

	"github.com/google/uuid"
)

// InstanceMinute is what one instance's app cgroup did in one UTC minute.
type InstanceMinute struct {
	AppID      uuid.UUID
	InstanceID string
	Minute     time.Time
	// MemStartMB is memory.current at the minute's first sample, MemPeakMB
	// the highest sample and MemEndMB the last one.
	MemStartMB int
	MemPeakMB  int
	MemEndMB   int
	// MemLimitMB is the cgroup's memory.max; 0 when unbounded or unknown.
	MemLimitMB int
	// CPUMS is the CPU time used in the minute; CPUPeakMillicores the
	// busiest sample interval's rate.
	CPUMS              int64
	CPUPeakMillicores  int
	CPULimitMillicores int
	Samples            int
}

// RiseMB is how far memory climbed above where the minute started.
func (m InstanceMinute) RiseMB() int { return max(m.MemPeakMB-m.MemStartMB, 0) }

// RouteMinute is one route's activity on one instance in one UTC minute.
type RouteMinute struct {
	AppID        uuid.UUID
	InstanceID   string
	Minute       time.Time
	Route        string
	Method       string
	Requests     int64
	BusyMS       int64
	MaxLatencyMS int
}

// Options tune the classification. Zero values take the defaults.
type Options struct {
	// MinRiseMB is the smallest memory rise that counts as a peak.
	MinRiseMB int
	// RiseFraction of the window's largest rise also has to be reached.
	RiseFraction float64
	// CPUSaturation is the share of the CPU limit a minute's busiest
	// interval has to reach to count as a CPU peak.
	CPUSaturation float64
	// MaxRoutes bounds the routes reported.
	MaxRoutes int
}

const (
	defaultMinRiseMB     = 16
	defaultRiseFraction  = 0.25
	defaultCPUSaturation = 0.8
	defaultMaxRoutes     = 50
	// fallbackCPULimit is used when no sample carried a CPU limit.
	fallbackCPULimit = 1000
)

func (o Options) withDefaults() Options {
	if o.MinRiseMB <= 0 {
		o.MinRiseMB = defaultMinRiseMB
	}
	if o.RiseFraction <= 0 || o.RiseFraction > 1 {
		o.RiseFraction = defaultRiseFraction
	}
	if o.CPUSaturation <= 0 || o.CPUSaturation > 1 {
		o.CPUSaturation = defaultCPUSaturation
	}
	if o.MaxRoutes <= 0 {
		o.MaxRoutes = defaultMaxRoutes
	}
	return o
}

// Report is the attribution over a window.
type Report struct {
	// ResourceMinutes are the instance-minutes with resource samples;
	// ActiveMinutes the ones that also served requests.
	ResourceMinutes int `json:"resource_minutes"`
	ActiveMinutes   int `json:"active_minutes"`
	// RouteMinutesWithoutResources are instance-minutes that served
	// requests but sent no samples (older guest, lost frames). They are
	// left out of every share.
	RouteMinutesWithoutResources int `json:"route_minutes_without_resources"`

	MemPeakP50MB int `json:"mem_peak_p50_mb"`
	MemPeakP95MB int `json:"mem_peak_p95_mb"`
	MemPeakMaxMB int `json:"mem_peak_max_mb"`
	MemLimitMB   int `json:"mem_limit_mb"`
	// HighMemoryRiseMB is the rise a minute needed to count as a memory
	// peak in this window.
	HighMemoryRiseMB  int `json:"high_memory_rise_mb"`
	HighMemoryMinutes int `json:"high_memory_minutes"`
	// UnattributedHighMemoryMinutes rose with no request in flight
	// (background work, a cron, garbage collection).
	UnattributedHighMemoryMinutes int `json:"unattributed_high_memory_minutes"`

	CPUPeakP95Millicores int `json:"cpu_peak_p95_millicores"`
	CPULimitMillicores   int `json:"cpu_limit_millicores"`
	HighCPUMinutes       int `json:"high_cpu_minutes"`

	Routes []RouteReport `json:"routes"`
	// RoutesOmitted counts routes past Options.MaxRoutes.
	RoutesOmitted int `json:"routes_omitted"`
}

// RouteReport is one route's share of the window's peaks.
type RouteReport struct {
	Route    string `json:"route"`
	Method   string `json:"method"`
	Requests int64  `json:"requests"`
	BusyMS   int64  `json:"busy_ms"`
	// ActiveMinutes counts the instance-minutes the route ran in;
	// PresenceShare is that over every active minute.
	ActiveMinutes int     `json:"active_minutes"`
	PresenceShare float64 `json:"presence_share"`

	// HighMemoryShare is the share of the window's attributed memory peaks
	// the route was in flight for, and SoleHighMemoryMinutes the peaks it
	// was the only route in flight for: the strongest evidence.
	HighMemoryMinutes     int     `json:"high_memory_minutes"`
	HighMemoryShare       float64 `json:"high_memory_share"`
	SoleHighMemoryMinutes int     `json:"sole_high_memory_minutes"`
	// The p95 memory peak and rise in minutes with and without the route.
	// MemPeakP95WithoutMB approximates what the app would need without it.
	MemPeakP95WithMB    int `json:"mem_peak_p95_with_mb"`
	MemPeakP95WithoutMB int `json:"mem_peak_p95_without_mb"`
	MemRiseP95WithMB    int `json:"mem_rise_p95_with_mb"`
	MemRiseP95WithoutMB int `json:"mem_rise_p95_without_mb"`

	HighCPUMinutes              int     `json:"high_cpu_minutes"`
	HighCPUShare                float64 `json:"high_cpu_share"`
	CPUPeakP95WithMillicores    int     `json:"cpu_peak_p95_with_millicores"`
	CPUPeakP95WithoutMillicores int     `json:"cpu_peak_p95_without_millicores"`
}

type minuteKey struct {
	instance string
	minute   int64
}

type routeKey struct{ route, method string }

type routeAcc struct {
	requests, busy int64
	minutes        map[minuteKey]struct{}
}

// Analyze joins resource minutes with route minutes on (instance, minute).
func Analyze(resources []InstanceMinute, routes []RouteMinute, opts Options) Report {
	opts = opts.withDefaults()
	var rep Report

	res := make(map[minuteKey]InstanceMinute, len(resources))
	for _, m := range resources {
		k := minuteKey{m.InstanceID, m.Minute.UTC().Unix()}
		if cur, ok := res[k]; ok {
			m = mergeMinute(cur, m)
		}
		res[k] = m
	}
	rep.ResourceMinutes = len(res)

	accs := map[routeKey]*routeAcc{}
	present := map[minuteKey][]routeKey{}
	missing := map[minuteKey]struct{}{}
	for _, r := range routes {
		k := minuteKey{r.InstanceID, r.Minute.UTC().Unix()}
		if _, ok := res[k]; !ok {
			missing[k] = struct{}{}
			continue
		}
		rk := routeKey{r.Route, r.Method}
		a := accs[rk]
		if a == nil {
			a = &routeAcc{minutes: map[minuteKey]struct{}{}}
			accs[rk] = a
		}
		a.requests += r.Requests
		a.busy += r.BusyMS
		if _, seen := a.minutes[k]; !seen {
			a.minutes[k] = struct{}{}
			present[k] = append(present[k], rk)
		}
	}
	rep.RouteMinutesWithoutResources = len(missing)
	rep.ActiveMinutes = len(present)
	if len(res) == 0 {
		return rep
	}

	peaks := make([]int, 0, len(res))
	cpuPeaks := make([]int, 0, len(res))
	maxRise := 0
	for _, m := range res {
		peaks = append(peaks, m.MemPeakMB)
		cpuPeaks = append(cpuPeaks, m.CPUPeakMillicores)
		maxRise = max(maxRise, m.RiseMB())
		rep.MemLimitMB = max(rep.MemLimitMB, m.MemLimitMB)
		rep.CPULimitMillicores = max(rep.CPULimitMillicores, m.CPULimitMillicores)
	}
	rep.MemPeakP50MB = percentile(peaks, 0.50)
	rep.MemPeakP95MB = percentile(peaks, 0.95)
	rep.MemPeakMaxMB = percentile(peaks, 1)
	rep.CPUPeakP95Millicores = percentile(cpuPeaks, 0.95)
	rep.HighMemoryRiseMB = max(opts.MinRiseMB, int(math.Ceil(float64(maxRise)*opts.RiseFraction)))
	cpuLimit := rep.CPULimitMillicores
	if cpuLimit <= 0 {
		cpuLimit = fallbackCPULimit
	}
	cpuHigh := int(math.Ceil(float64(cpuLimit) * opts.CPUSaturation))

	highMem := map[minuteKey]bool{}
	highCPU := map[minuteKey]bool{}
	attributedHighMem, attributedHighCPU := 0, 0
	for k, m := range res {
		_, active := present[k]
		if m.RiseMB() >= rep.HighMemoryRiseMB {
			rep.HighMemoryMinutes++
			highMem[k] = true
			if active {
				attributedHighMem++
			} else {
				rep.UnattributedHighMemoryMinutes++
			}
		}
		if m.CPUPeakMillicores >= cpuHigh {
			rep.HighCPUMinutes++
			highCPU[k] = true
			if active {
				attributedHighCPU++
			}
		}
	}

	for rk, a := range accs {
		rr := RouteReport{Route: rk.route, Method: rk.method, Requests: a.requests, BusyMS: a.busy, ActiveMinutes: len(a.minutes)}
		rr.PresenceShare = share(len(a.minutes), rep.ActiveMinutes)
		var withPeak, withoutPeak, withRise, withoutRise, withCPU, withoutCPU []int
		for k := range present {
			m := res[k]
			if _, in := a.minutes[k]; in {
				withPeak = append(withPeak, m.MemPeakMB)
				withRise = append(withRise, m.RiseMB())
				withCPU = append(withCPU, m.CPUPeakMillicores)
				if highMem[k] {
					rr.HighMemoryMinutes++
					if len(present[k]) == 1 {
						rr.SoleHighMemoryMinutes++
					}
				}
				if highCPU[k] {
					rr.HighCPUMinutes++
				}
				continue
			}
			withoutPeak = append(withoutPeak, m.MemPeakMB)
			withoutRise = append(withoutRise, m.RiseMB())
			withoutCPU = append(withoutCPU, m.CPUPeakMillicores)
		}
		rr.HighMemoryShare = share(rr.HighMemoryMinutes, attributedHighMem)
		rr.HighCPUShare = share(rr.HighCPUMinutes, attributedHighCPU)
		rr.MemPeakP95WithMB = percentile(withPeak, 0.95)
		rr.MemPeakP95WithoutMB = percentile(withoutPeak, 0.95)
		rr.MemRiseP95WithMB = percentile(withRise, 0.95)
		rr.MemRiseP95WithoutMB = percentile(withoutRise, 0.95)
		rr.CPUPeakP95WithMillicores = percentile(withCPU, 0.95)
		rr.CPUPeakP95WithoutMillicores = percentile(withoutCPU, 0.95)
		rep.Routes = append(rep.Routes, rr)
	}
	slices.SortFunc(rep.Routes, func(a, b RouteReport) int {
		return cmp.Or(
			cmp.Compare(b.HighMemoryShare, a.HighMemoryShare),
			cmp.Compare(b.HighCPUShare, a.HighCPUShare),
			cmp.Compare(b.BusyMS, a.BusyMS),
			cmp.Compare(a.Route, b.Route),
			cmp.Compare(a.Method, b.Method),
		)
	})
	if len(rep.Routes) > opts.MaxRoutes {
		rep.RoutesOmitted = len(rep.Routes) - opts.MaxRoutes
		rep.Routes = rep.Routes[:opts.MaxRoutes]
	}
	return rep
}

// mergeMinute folds two rows of the same instance-minute (vmmd sends a
// minute again when a late slot lands after the minute was sent). a is the
// earlier row, so its start stands.
func mergeMinute(a, b InstanceMinute) InstanceMinute {
	out := a
	if a.Samples == 0 {
		out.MemStartMB = b.MemStartMB
	}
	out.MemPeakMB = max(a.MemPeakMB, b.MemPeakMB)
	out.MemEndMB = b.MemEndMB
	out.MemLimitMB = max(a.MemLimitMB, b.MemLimitMB)
	out.CPUMS = a.CPUMS + b.CPUMS
	out.CPUPeakMillicores = max(a.CPUPeakMillicores, b.CPUPeakMillicores)
	out.CPULimitMillicores = max(a.CPULimitMillicores, b.CPULimitMillicores)
	out.Samples = a.Samples + b.Samples
	return out
}

func share(n, total int) float64 {
	if total <= 0 {
		return 0
	}
	return math.Round(float64(n)/float64(total)*1000) / 1000
}

// percentile is the nearest-rank percentile of values; 0 when empty.
func percentile(values []int, p float64) int {
	if len(values) == 0 {
		return 0
	}
	s := slices.Clone(values)
	slices.Sort(s)
	rank := int(math.Ceil(p*float64(len(s)))) - 1
	return s[min(max(rank, 0), len(s)-1)]
}
