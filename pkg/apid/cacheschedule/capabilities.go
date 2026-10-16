package cacheschedule

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Capabilities reports interfaces actually wired into the serving API. It does
// not query customer data or assert that all fleet writers have been upgraded.
func Capabilities(store any) *api.EnvironmentCacheScheduleCapabilities {
	_, listing := store.(state.EnvironmentCacheScheduleManagement)
	_, activity := store.(state.EnvironmentCacheScheduleActivityReader)
	_, checkpoint := store.(state.EnvironmentCacheScheduleActivityCheckpointReader)
	_, retention := store.(state.EnvironmentCacheScheduleActivityPruner)
	out := &api.EnvironmentCacheScheduleCapabilities{Listing: listing, Activity: activity, Checkpoint: checkpoint}
	if activity && retention {
		out.RetentionSeconds = api.EnvironmentCacheActivityRetentionSeconds
	}
	return out
}
