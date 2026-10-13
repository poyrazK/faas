package release

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// DefaultProbePath is what apid probes on a safe-policy canary (ADR-1160):
// the path the post-readiness hosting smoke already checks once. Probes keep
// checking it on the candidate and its predecessor while the canary runs, so
// a quiet release is judged on evidence instead of on the absence of errors.
const DefaultProbePath = "/"

// DefaultProbeRoute is the selector the default probe records its results
// under in route_probe_observations.
func DefaultProbeRoute() api.RouteHealthRoute {
	return api.RouteHealthRoute{Method: http.MethodGet, Path: DefaultProbePath, Probe: &api.RouteHealthProbe{Path: DefaultProbePath}}
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
			out[i].Default, out[i].StableID = true, t.StableID
			continue
		}
		out = append(out, ProbeTarget{RouteProbeTarget: t, Default: true})
	}
	return out
}
