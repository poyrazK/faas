package main_test

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestTerminalStatusClassifiers(t *testing.T) {
	deploymentCases := map[string]bool{
		"": false, "queued": false, "building": false, "deploying": false,
		main.StatusLive: true, main.DeploymentStatusFailed: true,
		main.DeploymentStatusCancelled: true, main.DeploymentStatusSuperseded: true,
	}
	for status, want := range deploymentCases {
		if got := main.IsTerminalDeploymentStatus(status); got != want {
			t.Errorf("deployment status %q terminal=%v, want %v", status, got, want)
		}
	}
	buildCases := map[string]bool{
		"": false, "queued": false, "running": false,
		main.BuildStatusSucceeded: true, main.BuildStatusFailed: true, main.BuildStatusCancelled: true,
	}
	for status, want := range buildCases {
		if got := main.IsTerminalBuildStatus(status); got != want {
			t.Errorf("build status %q terminal=%v, want %v", status, got, want)
		}
	}
}

func TestCompletedDeploymentRequiresReadinessClosure(t *testing.T) {
	cases := []struct {
		name string
		dep  api.DeploymentResponse
		want bool
	}{
		{name: "intermediate live", dep: api.DeploymentResponse{Status: main.StatusLive, StageState: json.RawMessage(`{"current":"snapshot_prepare","history":[]}`)}, want: false},
		{name: "readiness completed", dep: api.DeploymentResponse{Status: main.StatusLive, StageState: json.RawMessage(`{"current":"readiness","history":[{"name":"readiness","status":"completed"}]}`)}, want: true},
		{name: "legacy receipt", dep: api.DeploymentResponse{Status: main.StatusLive, APIHostingReceipt: json.RawMessage(`{"smoke":{"status":"verified"}}`)}, want: true},
		{name: "legacy live", dep: api.DeploymentResponse{Status: main.StatusLive}, want: true},
		{name: "failed", dep: api.DeploymentResponse{Status: main.DeploymentStatusFailed}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := main.IsCompletedDeployment(tc.dep); got != tc.want {
				t.Fatalf("isCompletedDeployment()=%v, want %v", got, tc.want)
			}
		})
	}
}
