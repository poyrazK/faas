package release

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// DefaultProbePath is what apid probes on a safe-policy canary (ADR-1446):
// the path the post-readiness hosting smoke already checks once. Probes keep
// checking it on the candidate and its predecessor while the canary runs, so
// a quiet release is judged on evidence instead of on the absence of errors.
const DefaultProbePath = "/"

// DefaultProbeRoute is the selector the default probe records its results
// under in route_probe_observations.
func DefaultProbeRoute() api.RouteHealthRoute {
	return probeRoute(DefaultProbePath)
}

// DefaultProbeRoutes are the selectors the default probe checks for an app:
// its release_probe_paths (ADR-1449), or "/" when it names none. Paths the
// manifest validator would reject are skipped, and none left means "/".
func DefaultProbeRoutes(paths []string) []api.RouteHealthRoute {
	routes := make([]api.RouteHealthRoute, 0, len(paths))
	for _, p := range paths {
		if api.ValidProbePath(p) && len(routes) < api.ReleaseProbePathsMax {
			routes = append(routes, probeRoute(p))
		}
	}
	if len(routes) == 0 {
		return []api.RouteHealthRoute{DefaultProbeRoute()}
	}
	return routes
}

// DefaultProbeRequests is how many probes each of n default routes gets per
// deployment and round, so one round still meets the circuit breaker's
// request floor in total (ADR-1321, ADR-1449).
func DefaultProbeRequests(n int) int {
	if n <= 1 {
		return api.DefaultRouteProbeRequestsPerRound
	}
	return (api.DefaultRouteProbeRequestsPerRound + n - 1) / n
}

func probeRoute(path string) api.RouteHealthRoute {
	return api.RouteHealthRoute{Method: http.MethodGet, Path: path, Probe: &api.RouteHealthProbe{Path: path}}
}

// DefaultProbesEnabled reports whether default probes run. The operator kill
// switch for the safe default (DefaultEnabledEnv) turns them off with it.
func DefaultProbesEnabled() bool { return defaultEnabled() }

// ProbeTarget is one app's probe round: its ADR-847 selectors when Gated and
// the default probe when Default.
type ProbeTarget struct {
	state.RouteProbeTarget
	Gated, Default bool
}

// MergeProbeTargets joins ADR-847 targets with default-probe targets so each
// canary gets one probe round per minute. A default target's predecessor is
// kept on the merged target.
func MergeProbeTargets(gated, defaults []state.RouteProbeTarget) []ProbeTarget {
	out := make([]ProbeTarget, 0, len(gated)+len(defaults))
	index := map[string]int{}
	for _, t := range gated {
		index[t.AppID+"/"+t.CandidateID] = len(out)
		out = append(out, ProbeTarget{RouteProbeTarget: t, Gated: true})
	}
	for _, t := range defaults {
		if i, ok := index[t.AppID+"/"+t.CandidateID]; ok {
			out[i].Default, out[i].StableID, out[i].ProbePaths = true, t.StableID, t.ProbePaths
			continue
		}
		out = append(out, ProbeTarget{RouteProbeTarget: t, Default: true})
	}
	return out
}
