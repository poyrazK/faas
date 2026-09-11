package sched

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDeploymentRuntimePort(t *testing.T) {
	t.Parallel()
	profile, err := json.Marshal(frameworkprofile.Profile{Version: frameworkprofile.Version, Port: 3000})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		dep  state.Deployment
		want int
	}{
		{name: "legacy default", dep: state.Deployment{}, want: 0},
		{name: "source profile", dep: state.Deployment{InferredProfile: profile}, want: 3000},
		{name: "explicit override wins", dep: state.Deployment{OverridePort: 8787, InferredProfile: profile}, want: 8787},
		{name: "malformed profile", dep: state.Deployment{InferredProfile: json.RawMessage(`{"version":"v1","port":70000}`)}, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := deploymentRuntimePort(tc.dep); got != tc.want {
				t.Fatalf("deploymentRuntimePort() = %d, want %d", got, tc.want)
			}
		})
	}
}
