package sched

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// quietPauseAfterMS is the ADR-1151 quiet pause delay schedd asks vmmd for.
// It applies to apps whose declared pause is between_requests, the default
// for functions: their contract is request-driven, as on AWS Lambda, so
// pausing between requests changes the least for them. Any other app opts
// in by declaring it. vmmd applies quiet pause only where its own
// quiet_pause switch is on.
func quietPauseAfterMS(app state.App, mode string, privateNetworkID string) int64 {
	if !pauseTierWorkload(app, mode, privateNetworkID) {
		return 0
	}
	if api.EffectivePause(app.Manifest.Pause, app.Type == state.AppTypeFunction) != api.PauseBetweenRequests {
		return 0
	}
	return api.QuietPauseAfterMillis
}

// pauseTierWorkload reports whether the pause tiers may pause an instance
// at all (ADR-1151): a request-serving workload woken as an ordinary
// instance. Workers, services, jobs and mirror instances keep running, and
// private-network peers stay awake because their traffic bypasses vmmd's
// forward path.
func pauseTierWorkload(app state.App, mode, privateNetworkID string) bool {
	if privateNetworkID != "" {
		return false
	}
	switch app.WorkloadClass {
	case "", state.WorkloadClassHTTP, state.WorkloadClassGraphQL, state.WorkloadClassGRPC:
	default:
		return false
	}
	return mode == "" || mode == string(state.InstanceModeNormal)
}
