package routehealth

import (
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func validateLatencyDiagnostics(w api.RouteHealthInvestigationWindow, signal, slug string) error {
	d := w.Diagnostics
	if signal != "latency" {
		if d != nil {
			return errors.New("unexpected latency diagnostics for error selection")
		}
		return nil
	}
	if d == nil || d.Coverage != "retained_samples" || d.RowsLimit != api.RouteHealthLatencyEvidenceRowsLimit || len(d.Dependencies) > api.RouteHealthLatencyDependenciesLimit || d.DependenciesTruncated && len(d.Dependencies) != api.RouteHealthLatencyDependenciesLimit {
		return errors.New("invalid latency diagnostic coverage")
	}
	for i, s := range []api.RouteHealthLatencySample{d.Candidate, d.Stable} {
		side := w.Candidate
		if i == 1 {
			side = w.Stable
		}
		if !validLatencySample(s, side) {
			return errors.New("latency sample inventory does not reconcile")
		}
	}
	keys := map[string]bool{}
	rowSides := map[string]int{}
	for _, dep := range d.Dependencies {
		key := dep.Type + "/" + dep.Kind
		if !validDependencyType(dep.Type) || !validDependencyKind(dep.Kind) || keys[key] || dep.Candidate.SpanSamples+dep.Stable.SpanSamples == 0 {
			return errors.New("invalid dependency group")
		}
		keys[key] = true
		status := "one_sided"
		if dep.Candidate.SpanSamples > 0 && dep.Stable.SpanSamples > 0 {
			status = "compared"
		}
		if dep.Status != status || !validLatencyDelta(dep.P95DeltaMS, dep.Candidate.P95MS, dep.Stable.P95MS) || !validLatencyDelta(dep.ExclusiveP95DeltaMS, dep.Candidate.ExclusiveP95MS, dep.Stable.ExclusiveP95MS) {
			return errors.New("dependency comparison does not match measured samples")
		}
		for i, g := range []api.RouteHealthDependencyTiming{dep.Candidate, dep.Stable} {
			s := d.Candidate
			if i == 1 {
				s = d.Stable
			}
			if !validDependencyTiming(g, s, w, slug) {
				return errors.New("invalid dependency sample or example")
			}
			for _, e := range g.Examples {
				if old, ok := rowSides[e.TelemetryID]; ok && old != i {
					return errors.New("dependency example crosses deployment sides")
				}
				rowSides[e.TelemetryID] = i
			}
		}
	}
	for i, s := range []api.RouteHealthLatencySample{d.Candidate, d.Stable} {
		var spans, calls int64
		for _, dep := range d.Dependencies {
			g := dep.Candidate
			if i == 1 {
				g = dep.Stable
			}
			spans += g.SpanSamples
			calls += g.RepresentedCalls
		}
		if spans > s.SpanSamples || calls > s.SampledRequests*api.DebugEvidenceMaxSpans || !d.DependenciesTruncated && spans != s.SpanSamples {
			return errors.New("dependency groups do not reconcile with retained spans")
		}
	}
	return nil
}

func validLatencySample(s api.RouteHealthLatencySample, side api.RouteHealthInvestigationSide) bool {
	if s.SampledRows != min(side.ObservedRows, int64(api.RouteHealthLatencyEvidenceRowsLimit)) || s.SamplesTruncated != (side.ObservedRows > s.SampledRows) || s.SampledRequests < s.SampledRows || s.SampledRequests > side.MatchingRequests || !s.SamplesTruncated && s.SampledRequests != side.MatchingRequests {
		return false
	}
	if s.SpanRows < 0 || s.MissingSpanRows < 0 || s.SpanRows+s.MissingSpanRows != s.SampledRows || s.SpanSamples < s.SpanRows || s.SpanSamples > s.SpanRows*api.DebugEvidenceMaxSpans || s.SpanRows == 0 && (s.SpansTruncated || s.TimingIncomplete) {
		return false
	}
	if s.GuestRows < 0 || s.GuestRows > s.SampledRows || s.GuestRequests < s.GuestRows || s.GuestRequests > s.SampledRequests || (s.GuestRows == 0) != (s.GuestRequests == 0) || !validLatencyValue(s.GuestP95MS, s.GuestRows > 0) {
		return false
	}
	return s.ColdBootRequests >= 0 && s.ColdBootRequests <= s.SampledRequests && s.WakeSamples >= 0 && s.WakeSamples <= min(s.SampledRows, s.ColdBootRequests) && validLatencyValue(s.WakeBootP95MS, s.WakeSamples > 0)
}

func validDependencyTiming(g api.RouteHealthDependencyTiming, s api.RouteHealthLatencySample, w api.RouteHealthInvestigationWindow, slug string) bool {
	if g.SpanSamples < 0 || g.SpanSamples > s.SpanSamples || g.RepresentedCalls < g.SpanSamples || g.RepresentedCalls > s.SampledRequests*api.DebugEvidenceMaxSpans || g.ErrorCalls < 0 || g.ErrorCalls > g.RepresentedCalls || !validLatencyValue(g.P95MS, g.SpanSamples > 0) {
		return false
	}
	if !validLatencyValue(g.ExclusiveP95MS, g.SpanSamples > 0 && !s.TimingIncomplete && !s.SpansTruncated) || len(g.Examples) > api.RouteHealthInvestigationExamplesLimit || g.SpanSamples == 0 && (g.RepresentedCalls != 0 || len(g.Examples) != 0) || g.SpanSamples > 0 && len(g.Examples) == 0 {
		return false
	}
	seen, represented := map[string]bool{}, int64(0)
	for _, e := range g.Examples {
		id, err := uuid.Parse(e.TelemetryID)
		if err != nil || id.String() != e.TelemetryID || seen[e.TelemetryID] || e.EvidencePath != "/v1/apps/"+url.PathEscape(slug)+"/debug/requests/"+e.TelemetryID+"/evidence" || e.ReceivedAt.Before(w.Start) || !e.ReceivedAt.Before(w.End) || e.Status < 100 || e.Status > 599 || e.LatencyMS < 0 || e.RepresentedRequests < 1 || e.RepresentedRequests > s.SampledRequests || e.RepresentedRequests > g.RepresentedCalls-represented {
			return false
		}
		seen[e.TelemetryID] = true
		represented += e.RepresentedRequests
	}
	return true
}

func validLatencyValue(value *int64, expected bool) bool {
	return (value != nil) == expected && (value == nil || *value >= 0 && *value <= int64(24*time.Hour/time.Millisecond))
}

func validLatencyDelta(delta, candidate, stable *int64) bool {
	if candidate == nil || stable == nil {
		return delta == nil
	}
	return delta != nil && *delta == *candidate-*stable
}

func validDependencyType(value string) bool {
	switch value {
	case "application", "managed_binding", "outbound_integration", "guest_transport", "platform_internal":
		return true
	}
	return false
}

func validDependencyKind(value string) bool {
	return len(value) <= 64 && strings.IndexFunc(value, func(c rune) bool {
		return (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '.' && c != '_' && c != '-'
	}) == -1
}
