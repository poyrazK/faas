package sched

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// quietPauseAfterMS is the ADR-1151 quiet pause delay schedd asks vmmd for.
// The first release covers function apps that serve HTTP, woken as
// ordinary instances: their contract is request-driven, as on AWS Lambda,
// so pausing between requests changes the least for them. Workers,
// services, mirror instances and private-network peers (their traffic
// bypasses vmmd's forward path) keep running between requests. vmmd
// applies quiet pause only where its own quiet_pause switch is on.
func quietPauseAfterMS(app state.App, mode string, privateNetworkID string) int64 {
	if app.Type != state.AppTypeFunction || privateNetworkID != "" {
		return 0
	}
	if app.WorkloadClass != "" && app.WorkloadClass != state.WorkloadClassHTTP {
		return 0
	}
	if mode != "" && mode != string(state.InstanceModeNormal) {
		return 0
	}
	return api.QuietPauseAfterMillis
}
