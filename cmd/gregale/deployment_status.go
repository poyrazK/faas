package main

import (
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
)

func isTerminalDeploymentStatus(status string) bool {
	switch status {
	case statusLive, deploymentStatusFailed, deploymentStatusCancelled, deploymentStatusSuperseded:
		return true
	default:
		return false
	}
}

// isCompletedDeployment keeps the temporary routable state used by hosting
// verification from becoming a CLI success. Modern deployments complete the
// readiness stage after smoke/receipt persistence; legacy rows without stage
// data may use the durable receipt as their completion proof.
func isCompletedDeployment(dep api.DeploymentResponse) bool {
	if dep.Status != statusLive {
		return isTerminalDeploymentStatus(dep.Status)
	}
	if len(dep.StageState) == 0 {
		// Pre-stage-state servers have no intermediate routable state. Keep
		// their historical live-is-complete contract for compatibility.
		return true
	}
	var stages struct {
		History []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"history"`
	}
	if json.Unmarshal(dep.StageState, &stages) != nil {
		return false
	}
	for _, stage := range stages.History {
		if stage.Name == "readiness" && stage.Status == "completed" {
			return true
		}
	}
	return false
}

func isTerminalBuildStatus(status string) bool {
	switch status {
	case buildStatusSucceeded, buildStatusFailed, buildStatusCancelled:
		return true
	default:
		return false
	}
}
