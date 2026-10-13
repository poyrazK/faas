package api

import (
	"fmt"
	"net/http"
	"time"
)

const (
	MCPTaskCompatibilityMaxEntries                 = 256
	MCPTaskCompatibilityMaxDeploymentEntries       = 768
	MCPTaskCompatibilityMaxRequestBytes            = 128 << 10
	MCPTaskCompatibilityFreshnessSeconds           = 45
	MCPTaskCompatibilityCountMax             int64 = 1_000_000_000_000
)

// MCPTaskHandlerCompatibility is an aggregate count of unexpired, nonterminal
// tasks for one registered handler version. It deliberately contains no task,
// owner, or payload data.
type MCPTaskHandlerCompatibility struct {
	ToolName                 string                            `json:"tool_name"`
	HandlerVersion           string                            `json:"handler_version"`
	WaitingTasks             int64                             `json:"waiting_tasks"`
	LiveCompatibleWorkers    int64                             `json:"live_compatible_workers"`
	UnknownDeploymentWorkers int64                             `json:"unknown_deployment_workers,omitempty"`
	Deployments              []MCPTaskHandlerDeploymentWorkers `json:"deployments,omitempty"`
}

// MCPTaskHandlerDeploymentWorkers identifies how many live workers on one
// deployment can execute a queued tool version. Deployment IDs are trusted
// scheduler identities injected into starter worker processes.
type MCPTaskHandlerDeploymentWorkers struct {
	DeploymentID string `json:"deployment_id"`
	Workers      int64  `json:"workers"`
}

// MCPTaskCompatibilityReportRequest is published by the always-on MCP task
// observer. Each request replaces the app's previous bounded snapshot.
type MCPTaskCompatibilityReportRequest struct {
	Handlers  []MCPTaskHandlerCompatibility `json:"handlers"`
	Truncated bool                          `json:"truncated,omitempty"`
}

// MCPTaskCompatibilityReportResponse includes server-received freshness so
// operator clients can distinguish an empty queue from a stopped observer.
type MCPTaskCompatibilityReportResponse struct {
	Published        bool                          `json:"published"`
	Handlers         []MCPTaskHandlerCompatibility `json:"handlers"`
	Truncated        bool                          `json:"truncated"`
	ObservedAt       *time.Time                    `json:"observed_at,omitempty"`
	FreshnessSeconds int                           `json:"freshness_seconds"`
	Stale            bool                          `json:"stale"`
}

// ValidateMCPTaskCompatibilityReport bounds the observer's cardinality and
// values before the report is persisted. Names use the same visible ASCII
// contract as the MCP starters, and duplicate pairs are rejected.
func ValidateMCPTaskCompatibilityReport(report MCPTaskCompatibilityReportRequest) *Problem {
	if report.Handlers == nil {
		return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
			"Invalid MCP task compatibility report", "handlers must be an array, including an empty array when no claimable tasks exist.")
	}
	if len(report.Handlers) > MCPTaskCompatibilityMaxEntries {
		return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
			"Invalid MCP task compatibility report", fmt.Sprintf("handlers must contain at most %d entries.", MCPTaskCompatibilityMaxEntries))
	}
	seen := make(map[string]struct{}, len(report.Handlers))
	deploymentEntries := 0
	for i, handler := range report.Handlers {
		if !validMCPTaskCompatibilityToolName(handler.ToolName) {
			return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
				"Invalid MCP task compatibility report", fmt.Sprintf("handlers[%d].tool_name must contain 1 to 128 visible ASCII characters.", i))
		}
		if !validMCPTaskCompatibilityHandlerVersion(handler.HandlerVersion) {
			return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
				"Invalid MCP task compatibility report", fmt.Sprintf("handlers[%d].handler_version must match [A-Za-z0-9._-]{1,64}.", i))
		}
		if handler.WaitingTasks < 1 || handler.WaitingTasks > MCPTaskCompatibilityCountMax {
			return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
				"Invalid MCP task compatibility report", fmt.Sprintf("handlers[%d].waiting_tasks must be between 1 and %d.", i, MCPTaskCompatibilityCountMax))
		}
		if handler.LiveCompatibleWorkers < 0 || handler.LiveCompatibleWorkers > MCPTaskCompatibilityCountMax {
			return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
				"Invalid MCP task compatibility report", fmt.Sprintf("handlers[%d].live_compatible_workers must be between 0 and %d.", i, MCPTaskCompatibilityCountMax))
		}
		if handler.UnknownDeploymentWorkers < 0 || handler.UnknownDeploymentWorkers > handler.LiveCompatibleWorkers {
			return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
				"Invalid MCP task compatibility report", fmt.Sprintf("handlers[%d].unknown_deployment_workers must be between 0 and live_compatible_workers.", i))
		}
		if len(handler.Deployments) > 64 {
			return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
				"Invalid MCP task compatibility report", fmt.Sprintf("handlers[%d].deployments must contain at most 64 entries.", i))
		}
		deploymentEntries += len(handler.Deployments)
		if deploymentEntries > MCPTaskCompatibilityMaxDeploymentEntries {
			return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
				"Invalid MCP task compatibility report", fmt.Sprintf("handlers may contain at most %d deployment entries in total.", MCPTaskCompatibilityMaxDeploymentEntries))
		}
		deploymentIDs := make(map[string]struct{}, len(handler.Deployments))
		var deploymentWorkers int64
		for j, deployment := range handler.Deployments {
			if len(deployment.DeploymentID) != 36 || !validMCPTaskCompatibilityDeploymentID(deployment.DeploymentID) {
				return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
					"Invalid MCP task compatibility report", fmt.Sprintf("handlers[%d].deployments[%d].deployment_id must be a UUID.", i, j))
			}
			if _, exists := deploymentIDs[deployment.DeploymentID]; exists {
				return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
					"Invalid MCP task compatibility report", fmt.Sprintf("handlers[%d].deployments contains a duplicate deployment id.", i))
			}
			if deployment.Workers < 1 || deployment.Workers > MCPTaskCompatibilityCountMax {
				return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
					"Invalid MCP task compatibility report", fmt.Sprintf("handlers[%d].deployments[%d].workers must be between 1 and %d.", i, j, MCPTaskCompatibilityCountMax))
			}
			deploymentIDs[deployment.DeploymentID] = struct{}{}
			deploymentWorkers += deployment.Workers
		}
		if deploymentWorkers+handler.UnknownDeploymentWorkers > handler.LiveCompatibleWorkers {
			return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
				"Invalid MCP task compatibility report", fmt.Sprintf("handlers[%d] deployment worker counts cannot exceed live_compatible_workers.", i))
		}
		key := handler.ToolName + "\x00" + handler.HandlerVersion
		if _, ok := seen[key]; ok {
			return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
				"Invalid MCP task compatibility report", fmt.Sprintf("handlers contains a duplicate tool/version pair at index %d.", i))
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validMCPTaskCompatibilityDeploymentID(id string) bool {
	for i := 0; i < len(id); i++ {
		c := id[i]
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

func validMCPTaskCompatibilityToolName(name string) bool {
	if len(name) < 1 || len(name) > 128 {
		return false
	}
	for i := 0; i < len(name); i++ {
		if name[i] < 0x21 || name[i] > 0x7e {
			return false
		}
	}
	return true
}

func validMCPTaskCompatibilityHandlerVersion(version string) bool {
	if len(version) < 1 || len(version) > 64 {
		return false
	}
	for i := 0; i < len(version); i++ {
		c := version[i]
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}
