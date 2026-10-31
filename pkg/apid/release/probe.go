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

// ProbeTarget is one app's probe round: its ADR-847 selectors when Gated,
// the default probe when Default, and the checks of its synthetic release
// gates (ADR-20261022082305).
type ProbeTarget struct {
	state.RouteProbeTarget
	Gated, Default bool
	Synthetic      []state.SyntheticGateProbeTarget
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

// AddSyntheticGates joins synthetic release gates to the round of their
// canary, adding a round for a canary nothing else probes. A canary beside
// several serving predecessors is skipped, as for the default probe.
func AddSyntheticGates(targets []ProbeTarget, gates []state.SyntheticGateProbeTarget) []ProbeTarget {
	stables := map[string]map[string]bool{}
	for _, g := range gates {
		key := g.AppID + "/" + g.CandidateID
		if stables[key] == nil {
			stables[key] = map[string]bool{}
		}
		stables[key][g.StableID] = true
	}
	index := map[string]int{}
	for i, t := range targets {
		index[t.AppID+"/"+t.CandidateID] = i
	}
	for _, g := range gates {
		key := g.AppID + "/" + g.CandidateID
		if len(stables[key]) != 1 {
			continue
		}
		i, ok := index[key]
		if !ok {
			i = len(targets)
			index[key] = i
			targets = append(targets, ProbeTarget{RouteProbeTarget: state.RouteProbeTarget{AccountID: g.AccountID, AppID: g.AppID,
				Slug: g.Slug, CandidateID: g.CandidateID, StableID: g.StableID}})
		}
		if targets[i].StableID == "" {
			targets[i].StableID = g.StableID
		}
		if targets[i].StableID == g.StableID {
			targets[i].Synthetic = append(targets[i].Synthetic, g)
		}
	}
	return targets
}
