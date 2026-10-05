package debugger

import (
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// RouteLatencyRow is an owned, exactly scoped retained row, never a full trace
// for every request represented by a collapsed telemetry bucket.
type RouteLatencyRow struct {
	Example    api.RouteHealthInvestigationExample
	Spans      []byte
	GuestMS    *int64
	ColdBoot   bool
	WakeID     string
	WakeBootMS *int64
}

type latencyValue struct{ ms, weight int64 }
type dependencyKey struct{ typ, kind string }
type dependencySample struct {
	timing          api.RouteHealthDependencyTiming
	wall, exclusive []latencyValue
}

// RouteLatencyDiagnostics compares independently selected recent samples. Its
// percentiles must not be added to each other or to the full route percentile.
func RouteLatencyDiagnostics(candidate, stable []RouteLatencyRow, candidateRows, stableRows int64) *api.RouteHealthLatencyDiagnostics {
	c, cg := latencySamples(candidate, candidateRows)
	s, sg := latencySamples(stable, stableRows)
	out := &api.RouteHealthLatencyDiagnostics{Coverage: "retained_samples", RowsLimit: api.RouteHealthLatencyEvidenceRowsLimit, Candidate: c, Stable: s, Dependencies: []api.RouteHealthDependencyComparison{}}
	keys := map[dependencyKey]bool{}
	for k := range cg {
		keys[k] = true
	}
	for k := range sg {
		keys[k] = true
	}
	for k := range keys {
		d := api.RouteHealthDependencyComparison{Type: k.typ, Kind: k.kind, Status: "one_sided", Candidate: dependencyTiming(cg[k], c), Stable: dependencyTiming(sg[k], s)}
		if d.Candidate.SpanSamples > 0 && d.Stable.SpanSamples > 0 {
			d.Status = "compared"
		}
		d.P95DeltaMS = latencyDelta(d.Candidate.P95MS, d.Stable.P95MS)
		d.ExclusiveP95DeltaMS = latencyDelta(d.Candidate.ExclusiveP95MS, d.Stable.ExclusiveP95MS)
		out.Dependencies = append(out.Dependencies, d)
	}
	sort.Slice(out.Dependencies, func(i, j int) bool {
		a, b := out.Dependencies[i], out.Dependencies[j]
		// Positive comparable changes precede one-sided or unchanged samples.
		ad, bd := int64(0), int64(0)
		if a.P95DeltaMS != nil {
			ad = max(0, *a.P95DeltaMS)
		}
		if b.P95DeltaMS != nil {
			bd = max(0, *b.P95DeltaMS)
		}
		if ad != bd {
			return ad > bd
		}
		ap, bp := int64(0), int64(0)
		if a.Candidate.P95MS != nil {
			ap = *a.Candidate.P95MS
		}
		if b.Candidate.P95MS != nil {
			bp = *b.Candidate.P95MS
		}
		if ap != bp {
			return ap > bp
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return a.Kind < b.Kind
	})
	out.DependenciesTruncated = len(out.Dependencies) > api.RouteHealthLatencyDependenciesLimit
	if out.DependenciesTruncated {
		out.Dependencies = out.Dependencies[:api.RouteHealthLatencyDependenciesLimit]
	}
	return out
}

func latencySamples(rows []RouteLatencyRow, observedRows int64) (api.RouteHealthLatencySample, map[dependencyKey]*dependencySample) {
	if len(rows) > api.RouteHealthLatencyEvidenceRowsLimit {
		rows = rows[:api.RouteHealthLatencyEvidenceRowsLimit]
	}
	s := api.RouteHealthLatencySample{SampledRows: int64(len(rows)), SamplesTruncated: observedRows > int64(len(rows))}
	groups := map[dependencyKey]*dependencySample{}
	guest, wakes := []latencyValue{}, []latencyValue{}
	seenWakes := map[string]bool{}
	for _, row := range rows {
		weight := row.Example.RepresentedRequests
		s.SampledRequests += weight
		if row.GuestMS != nil && *row.GuestMS >= 0 {
			s.GuestRows++
			s.GuestRequests += weight
			guest = append(guest, latencyValue{boundedLatency(*row.GuestMS), weight})
		}
		if row.ColdBoot {
			s.ColdBootRequests += weight
		}
		if row.ColdBoot && row.WakeID != "" && row.WakeBootMS != nil && *row.WakeBootMS >= 0 && !seenWakes[row.WakeID] {
			seenWakes[row.WakeID] = true
			wakes = append(wakes, latencyValue{boundedLatency(*row.WakeBootMS), 1})
		}
		spans, truncated := ParseSpans(row.Spans)
		s.SpansTruncated = s.SpansTruncated || truncated
		if len(spans) == 0 {
			s.MissingSpanRows++
			continue
		}
		s.SpanRows++
		s.SpanSamples += int64(len(spans))
		path := BuildCriticalPath(spans)
		s.TimingIncomplete = s.TimingIncomplete || truncated || path == nil || !path.Complete || !uniqueLatencySpanIDs(spans)
		exclusive := SpanExclusiveDurations(spans)
		rowGroups := map[dependencyKey]bool{}
		for i, span := range spans {
			key := dependencyKey{span.DependencyType, span.DependencyKind}
			if key.typ == "" {
				key.typ = "application"
			}
			g := groups[key]
			if g == nil {
				g = &dependencySample{timing: api.RouteHealthDependencyTiming{Examples: []api.RouteHealthInvestigationExample{}}}
				groups[key] = g
			}
			g.timing.SpanSamples++
			g.timing.RepresentedCalls += weight
			if strings.EqualFold(span.Status, "error") {
				g.timing.ErrorCalls += weight
			}
			g.wall = append(g.wall, latencyValue{int64(minDebugDependencyDuration(span.DurationNanos) / uint64(time.Millisecond)), weight})
			g.exclusive = append(g.exclusive, latencyValue{int64(exclusive[i] / uint64(time.Millisecond)), weight})
			if !rowGroups[key] && len(g.timing.Examples) < api.RouteHealthInvestigationExamplesLimit {
				g.timing.Examples = append(g.timing.Examples, row.Example)
			}
			rowGroups[key] = true
		}
	}
	s.GuestP95MS, s.WakeBootP95MS = latencyP95(guest), latencyP95(wakes)
	s.WakeSamples = int64(len(wakes))
	return s, groups
}

func dependencyTiming(g *dependencySample, sample api.RouteHealthLatencySample) api.RouteHealthDependencyTiming {
	if g == nil {
		return api.RouteHealthDependencyTiming{Examples: []api.RouteHealthInvestigationExample{}}
	}
	out := g.timing
	out.P95MS = latencyP95(g.wall)
	if !sample.TimingIncomplete && !sample.SpansTruncated {
		out.ExclusiveP95MS = latencyP95(g.exclusive)
	}
	return out
}

func latencyP95(values []latencyValue) *int64 {
	if len(values) == 0 {
		return nil
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ms < values[j].ms })
	var total int64
	for _, v := range values {
		total += v.weight
	}
	if total <= 0 {
		return nil
	}
	// Exact integer nearest rank, avoiding floating point error on weights.
	rank, seen := total-total/20, int64(0)
	for _, v := range values {
		seen += v.weight
		if seen >= rank {
			ms := v.ms
			return &ms
		}
	}
	return nil
}

func latencyDelta(candidate, stable *int64) *int64 {
	if candidate == nil || stable == nil {
		return nil
	}
	delta := *candidate - *stable
	return &delta
}

func boundedLatency(ms int64) int64 { return min(ms, int64((24*time.Hour)/time.Millisecond)) }

func uniqueLatencySpanIDs(spans []api.DebugTelemetrySpan) bool {
	seen := map[string]bool{}
	for _, span := range spans {
		if span.SpanID == "" || seen[span.SpanID] {
			return false
		}
		seen[span.SpanID] = true
	}
	return true
}
