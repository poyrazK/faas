package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// ProjectDeploymentCheckRequest describes intent without uploading source or
// staging credentials. ExpectedStateHash optionally compares a previous check.
type ProjectDeploymentCheckRequest struct {
	Environment       string                           `json:"environment"`
	FromEnvironment   string                           `json:"from_environment,omitempty"`
	ExpectedStateHash string                           `json:"expected_state_hash,omitempty"`
	Atomic            bool                             `json:"atomic,omitempty"`
	AddServices       bool                             `json:"add_services,omitempty"`
	Verify            bool                             `json:"verify,omitempty"`
	Workers           bool                             `json:"workers,omitempty"`
	WorkerHealth      bool                             `json:"worker_health,omitempty"`
	Migrate           bool                             `json:"migrate,omitempty"`
	Progressive       bool                             `json:"progressive,omitempty"`
	Stabilize         bool                             `json:"stabilize,omitempty"`
	SyncConfig        bool                             `json:"sync_config,omitempty"`
	ReleaseConfig     json.RawMessage                  `json:"release_config,omitempty"`
	Workloads         []ProjectDeploymentCheckWorkload `json:"workloads,omitempty"`
	Managed           []string                         `json:"managed,omitempty"`
}

type ProjectDeploymentCheckWorkload struct {
	Workload            PlanWorkload `json:"workload"`
	CommandShell        bool         `json:"command_shell,omitempty"`
	ReleaseCommand      []string     `json:"release_command,omitempty"`
	ReleaseCommandShell bool         `json:"release_command_shell,omitempty"`
	ProvidedKeys        []string     `json:"provided_keys,omitempty"`
}

// Checks contain only bounded public messages, identities and key names.
// Status is passed, blocked or unknown. Unreadable state cannot pass a check.
type ProjectDeploymentCheck struct {
	Code     string `json:"code"`
	Status   string `json:"status"`
	Workload string `json:"workload,omitempty"`
	Message  string `json:"message"`
	Remedy   string `json:"remedy,omitempty"`
	Observed *int   `json:"observed,omitempty"`
	Limit    *int   `json:"limit,omitempty"`
}

type ProjectDeploymentReadiness struct {
	SchemaVersion   int                      `json:"schema_version"`
	ProjectSlug     string                   `json:"project_slug"`
	Environment     string                   `json:"environment"`
	FromEnvironment string                   `json:"from_environment,omitempty"`
	Status          string                   `json:"status"`
	StateHash       string                   `json:"state_hash,omitempty"`
	CheckedAt       time.Time                `json:"checked_at"`
	RuntimeChecked  bool                     `json:"runtime_checked"`
	Checks          []ProjectDeploymentCheck `json:"checks"`
}

// DeploymentReadinessStatus is shared by the report producer and CI clients.
// A definite blocker takes precedence; otherwise any unknown prevents ready.
func DeploymentReadinessStatus(checks []ProjectDeploymentCheck) string {
	status := "ready"
	if len(checks) == 0 {
		return "unknown"
	}
	for _, check := range checks {
		if check.Status == "blocked" {
			return "blocked"
		}
		if check.Status != "passed" {
			status = "unknown"
		}
	}
	return status
}

func (c *Client) CheckProjectDeployment(ctx context.Context, project string, request ProjectDeploymentCheckRequest) (ProjectDeploymentReadiness, error) {
	if request.Environment == "" {
		request.Environment = "production"
	}
	var out ProjectDeploymentReadiness
	err := c.do(ctx, http.MethodPost, "/v1/projects/"+url.PathEscape(project)+"/deployment-readiness", request, &out)
	if err == nil && (out.SchemaVersion != 1 || out.ProjectSlug != project || out.Environment != request.Environment || out.FromEnvironment != request.FromEnvironment || out.RuntimeChecked || out.Status != DeploymentReadinessStatus(out.Checks)) {
		err = fmt.Errorf("invalid deployment readiness response")
	}
	return out, err
}
