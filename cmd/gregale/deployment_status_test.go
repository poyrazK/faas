package main

import "testing"

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
