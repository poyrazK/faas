package main

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestValidateDeploymentCheckTarget(t *testing.T) {
	tests := []struct {
		name           string
		kind           string
		commit         string
		repo           string
		installationID int64
		wantProject    bool
		wantErr        bool
	}{
		{name: "generic deployment ignores absent GitHub metadata", kind: "manual"},
		{name: "GitHub deployment requires commit", kind: string(state.DeploymentKindGitHub), repo: "owner/repo", installationID: 42, wantErr: true},
		{name: "preview deployment requires commit", kind: string(state.DeploymentKindPreview), repo: "owner/repo", installationID: 42, wantErr: true},
		{name: "disconnected repository is complete without projection", kind: string(state.DeploymentKindGitHub), commit: "abc", installationID: 42},
		{name: "disconnected installation is complete without projection", kind: string(state.DeploymentKindGitHub), commit: "abc", repo: "owner/repo"},
		{name: "disconnected preview is complete without projection", kind: string(state.DeploymentKindPreview), commit: "abc", repo: "owner/repo"},
		{name: "GitHub deployment projects", kind: string(state.DeploymentKindGitHub), commit: "abc", repo: "owner/repo", installationID: 42, wantProject: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			project, err := validateDeploymentCheckTarget("dep-1", tc.kind, tc.commit, tc.repo, tc.installationID)
			if project != tc.wantProject || (err != nil) != tc.wantErr {
				t.Fatalf("validate = (%v, %v), want project=%v err=%v", project, err, tc.wantProject, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "dep-1") {
				t.Fatalf("error %q does not identify the deployment", err)
			}
		})
	}
}

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
