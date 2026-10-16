package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
)

// ProjectReleaseWorker pins one existing production push consumer to the graph.
type ProjectReleaseWorker struct {
	AppID                string `json:"app_id"`
	Slug                 string `json:"slug"`
	BindingID            string `json:"binding_id"`
	BindingHash          string `json:"binding_hash"`
	PreviousDeploymentID string `json:"previous_deployment_id"`
	DrainSeconds         int    `json:"drain_seconds"`
	Status               string `json:"status"`
	DeploymentID         string `json:"deployment_id,omitempty"`
	InstanceID           string `json:"instance_id,omitempty"`
}

func ProjectReleaseWorkerPolicyID(worker *ProjectReleaseWorker) string {
	if worker == nil {
		return ""
	}
	copy := *worker
	copy.Status = ""
	copy.DeploymentID = ""
	copy.InstanceID = ""
	raw, _ := json.Marshal(copy)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

// ContextWithProjectReleaseWorkers requires graph-owned worker admission.
// Compose after configuration/migration context to preserve those capabilities.
func ContextWithProjectReleaseWorkers(ctx context.Context) context.Context {
	mode := projectSafeReleaseMode(ctx)
	if mode == "" || mode == "atomic" {
		mode = "atomic-verify"
	}
	if !strings.HasSuffix(mode, "-workers") {
		mode += "-workers"
	}
	return context.WithValue(ctx, projectSafeReleaseContextKey{}, mode)
}

const ProjectWorkerHealthHeader = "Project-Worker-Health"

type projectWorkerHealthContextKey struct{}

func ContextWithProjectWorkerHealth(ctx context.Context) context.Context {
	return context.WithValue(ctx, projectWorkerHealthContextKey{}, true)
}
func projectWorkerHealthRequested(ctx context.Context) bool {
	requested, _ := ctx.Value(projectWorkerHealthContextKey{}).(bool)
	return requested
}

// ProjectWorkerHealth reports disjoint delivery windows; no job payload is exposed.
type ProjectWorkerHealth struct {
	OldestPendingAgeMS         int64                  `json:"oldest_pending_age_ms"`
	BaselinePendingJobs        int64                  `json:"baseline_pending_jobs"`
	BaselineOldestPendingAgeMS int64                  `json:"baseline_oldest_pending_age_ms"`
	Status                     string                 `json:"status"`
	PendingJobs                int64                  `json:"pending_jobs"`
	Candidate                  ProjectWorkerJobSample `json:"candidate"`
	Baseline                   ProjectWorkerJobSample `json:"baseline"`
}
type ProjectWorkerJobSample struct {
	Attempts     int64 `json:"attempts"`
	Failures     int64 `json:"failures"`
	Incomplete   int64 `json:"incomplete"`
	DurationMS   int64 `json:"duration_ms"`
	Retries      int64 `json:"retries"`
	QueueDelayMS int64 `json:"queue_delay_ms"`
}
