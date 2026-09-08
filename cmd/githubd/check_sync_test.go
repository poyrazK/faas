package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/githubdgrpc"
)

func TestCheckPhaseForDeploymentStatusForRollout(t *testing.T) {
	cases := []struct {
		name             string
		status           string
		rolloutState     string
		canaryTotalSteps int
		want             githubdgrpc.CheckPhase
		ok               bool
	}{
		{name: "building", status: "building", want: githubdgrpc.CheckPhaseBuilding, ok: true},
		{name: "canary pending remains in progress", status: "live", rolloutState: "pending", canaryTotalSteps: 4, want: githubdgrpc.CheckPhaseBuilding, ok: true},
		{name: "canary rolling remains in progress", status: "live", rolloutState: "rolling_out", canaryTotalSteps: 4, want: githubdgrpc.CheckPhaseBuilding, ok: true},
		{name: "complete is successful", status: "live", rolloutState: "complete", canaryTotalSteps: 4, want: githubdgrpc.CheckPhaseLive, ok: true},
		{name: "aborted fails the check", status: "live", rolloutState: "aborted", canaryTotalSteps: 4, want: githubdgrpc.CheckPhaseFailed, ok: true},
		{name: "non-canary live is successful", status: "live", rolloutState: "pending", want: githubdgrpc.CheckPhaseLive, ok: true},
		{name: "unknown status is ignored", status: "deleted", want: githubdgrpc.CheckPhaseUnspecified, ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := checkPhaseForDeploymentStatusForRollout(tc.status, tc.rolloutState, tc.canaryTotalSteps)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("projection = (%v, %v), want (%v, %v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}
