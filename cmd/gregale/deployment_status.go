package main

func isTerminalDeploymentStatus(status string) bool {
	switch status {
	case statusLive, deploymentStatusFailed, deploymentStatusCancelled, deploymentStatusSuperseded:
		return true
	default:
		return false
	}
}

func isTerminalBuildStatus(status string) bool {
	switch status {
	case buildStatusSucceeded, buildStatusFailed, buildStatusCancelled:
		return true
	default:
		return false
	}
}
