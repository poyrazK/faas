package cacheschedule

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type checkpointOnlyBackend struct{}

func (checkpointOnlyBackend) GetEnvironmentCacheScheduleActivityCheckpoint(context.Context, string, string, string) (api.EnvironmentCacheActivityRecovery, error) {
	return api.EnvironmentCacheActivityRecovery{}, nil
}

func TestScheduleCapabilitiesUseServingBackend(t *testing.T) {
	for _, tc := range []struct {
		name  string
		store any
		want  api.EnvironmentCacheScheduleCapabilities
	}{
		{name: "unconfigured"},
		{name: "legacy", store: struct{}{}},
		{name: "checkpoint only", store: checkpointOnlyBackend{}, want: api.EnvironmentCacheScheduleCapabilities{Checkpoint: true}},
		{name: "current", store: state.NewMemStore(), want: api.EnvironmentCacheScheduleCapabilities{Listing: true, Activity: true, Checkpoint: true, RetentionSeconds: api.EnvironmentCacheActivityRetentionSeconds}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Capabilities(tc.store); *got != tc.want {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}
