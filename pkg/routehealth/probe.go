package routehealth

import (
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/onebox-faas/faas/pkg/api"
)

func probePath(p *api.RouteHealthProbe) string {
	if p == nil {
		return ""
	}
	return p.Path
}

// ProbeMatchesSelector reports whether a concrete probe path has the
// selector's shape: literal segments are equal and each {param} segment
// matches exactly one non-empty segment.
func ProbeMatchesSelector(selector, path string) bool {
	want, got := strings.Split(selector, "/"), strings.Split(path, "/")
	if len(want) != len(got) {
		return false
	}
	for i := range want {
		if strings.HasPrefix(want[i], "{") && strings.HasSuffix(want[i], "}") {
			if got[i] == "" {
				return false
			}
			continue
		}
		if want[i] != got[i] {
			return false
		}
	}
	return true
}

// ValidateProbe enforces ADR-847: probes are read-only, concrete and bound to
// their selector's shape so a probe cannot reach a different route.
func ValidateProbe(route api.RouteHealthRoute) error {
	if route.Probe == nil {
		return nil
	}
	if route.Method != "GET" && route.Method != "HEAD" {
		return errors.New("synthetic probes are limited to GET and HEAD selectors")
	}
	path := route.Probe.Path
	if len(path) == 0 || len(path) > api.RouteHealthMaxPathBytes || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#*\\{}") ||
		strings.IndexFunc(path, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return errors.New("probe path must be a concrete absolute path without queries, fragments, wildcards or parameters")
	}
	if !ProbeMatchesSelector(route.Path, path) {
		return errors.New("probe path must match the selector path, with a concrete value for each {parameter}")
	}
	return nil
}

// ProbedRoutes returns the selectors that opted into probes.
func ProbedRoutes(routes []api.RouteHealthRoute) []api.RouteHealthRoute {
	out := []api.RouteHealthRoute{}
	for _, r := range routes {
		if r.Probe != nil {
			out = append(out, r)
		}
	}
	return out
}

// probeWindow evaluates the 5xx signal of one synthetic window. Probes that
// customer auth gates rejected cannot establish health.
func probeWindow(w api.RouteHealthWindowEvidence, anchor *time.Time, unavailable string) (string, string) {
	for _, c := range []api.RouteHealthCounts{w.Candidate, w.Stable} {
		if c.Requests > 0 && float64(c.Unauthenticated) >= float64(c.Requests)*api.RouteHealthProbeUnauthenticatedShare {
			return "unknown", "probe_unauthenticated"
		}
	}
	return errorWindow(w, anchor, unavailable)
}

// applySyntheticEvidence adopts probe evidence for a route that one-minute
// and pooled evidence left sparse. Probes settle only the 5xx signal; a
// selected latency check still needs organic traffic, so a healthy probe
// verdict with unknown latency stays unknown while a regression is adopted.
func applySyntheticEvidence(f *api.RouteHealthFinding, anchor *time.Time, unavailable string) {
	if len(f.SyntheticWindows) != api.RouteHealthWindows {
		f.SyntheticWindows = nil
		return
	}
	statuses := make([]string, 0, len(f.SyntheticWindows))
	for i := range f.SyntheticWindows {
		w := &f.SyntheticWindows[i]
		rate(&w.Candidate)
		rate(&w.Stable)
		w.ErrorStatus, w.ErrorReason = probeWindow(*w, anchor, unavailable)
		w.LatencyStatus, w.LatencyReason, w.LatencyDeltaMS, w.LatencyFactor = "", "", nil, nil
		w.Status, w.Reason = w.ErrorStatus, w.ErrorReason
		statuses = append(statuses, w.ErrorStatus)
	}
	if unavailable != "" || f.EvidenceWindow != "" || !NeedsPooledEvidence(*f) {
		return
	}
	errStatus, errReason := consecutive(statuses, "consecutive_server_error_regression")
	status, reason := errStatus, errReason
	if LatencyEnabled(f.CheckLatency, f.MaxP95MS) {
		status, reason = combine(errStatus, errReason, f.LatencyStatus, f.LatencyReason)
	}
	if status != "healthy" && status != "regressed" {
		return
	}
	f.ErrorStatus, f.ErrorReason = errStatus, errReason
	f.Status, f.Reason = status, reason
	f.EvidenceWindow = "synthetic"
}
