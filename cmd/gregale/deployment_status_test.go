package main

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestTerminalStatusClassifiers(t *testing.T) {
	deploymentCases := map[string]bool{
		"": false, "queued": false, "building": false, "deploying": false,
		statusLive: true, deploymentStatusFailed: true,
		deploymentStatusCancelled: true, deploymentStatusSuperseded: true,
	}
	for status, want := range deploymentCases {
		if got := isTerminalDeploymentStatus(status); got != want {
			t.Errorf("deployment status %q terminal=%v, want %v", status, got, want)
		}
	}
	buildCases := map[string]bool{
		"": false, "queued": false, "running": false,
		buildStatusSucceeded: true, buildStatusFailed: true, buildStatusCancelled: true,
	}
	for status, want := range buildCases {
		if got := isTerminalBuildStatus(status); got != want {
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
		{name: "intermediate live", dep: api.DeploymentResponse{Status: statusLive, StageState: json.RawMessage(`{"current":"snapshot_prepare","history":[]}`)}, want: false},
		{name: "readiness completed", dep: api.DeploymentResponse{Status: statusLive, StageState: json.RawMessage(`{"current":"readiness","history":[{"name":"readiness","status":"completed"}]}`)}, want: true},
		{name: "legacy receipt", dep: api.DeploymentResponse{Status: statusLive, APIHostingReceipt: json.RawMessage(`{"smoke":{"status":"verified"}}`)}, want: true},
		{name: "legacy live", dep: api.DeploymentResponse{Status: statusLive}, want: true},
		{name: "failed", dep: api.DeploymentResponse{Status: deploymentStatusFailed}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCompletedDeployment(tc.dep); got != tc.want {
				t.Fatalf("isCompletedDeployment()=%v, want %v", got, tc.want)
			}
		})
	}
}
