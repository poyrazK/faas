package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"
)

const ProjectSafeReleaseHeader = "Project-Release-Mode"

type ProjectSafeRelease struct {
	AddServices       bool                         `json:"add_services,omitempty"`
	Additions         []ProjectReleaseAddition     `json:"additions,omitempty"`
	RemoveServices    bool                         `json:"remove_services,omitempty"`
	Removals          []ProjectReleaseRemoval      `json:"removals,omitempty"`
	Worker            *ProjectReleaseWorker        `json:"worker,omitempty"`
	ReleaseCommand    *ProjectReleaseCommand       `json:"release_command,omitempty"`
	Status            string                       `json:"status"`
	PreviousReleaseID string                       `json:"previous_release_id"`
	ReleaseID         string                       `json:"release_id,omitempty"`
	TTLSeconds        int                          `json:"ttl_seconds"`
	Deadline          time.Time                    `json:"deadline"`
	Message           string                       `json:"message,omitempty"`
	Verification      *ProjectReleaseVerification  `json:"verification,omitempty"`
	Configuration     *ProjectReleaseConfiguration `json:"configuration,omitempty"`
	RestoredReleaseID string                       `json:"restored_release_id,omitempty"`
}

type projectSafeReleaseContextKey struct{}

func ContextWithProjectSafeRelease(ctx context.Context) context.Context {
	return context.WithValue(ctx, projectSafeReleaseContextKey{}, "atomic")
}
func projectSafeReleaseRequested(ctx context.Context) bool {
	return projectSafeReleaseMode(ctx) != ""
}

func projectApplyRequestPath(ctx context.Context) string {
	if projectSafeReleaseRequested(ctx) {
		return "/v1/projects/safe-deploy"
	}
	return "/v1/projects"
}

// ContextWithProjectReleaseVerification requests durable post-cutover probes.
func ContextWithProjectReleaseVerification(ctx context.Context) context.Context {
	return context.WithValue(ctx, projectSafeReleaseContextKey{}, "atomic-verify")
}
func projectSafeReleaseMode(ctx context.Context) string {
	mode, _ := ctx.Value(projectSafeReleaseContextKey{}).(string)
	return mode
}

// ProjectReleaseVerification snapshots concrete read-only routes at admission.
type ProjectReleaseVerification struct {
	Stabilization   *ProjectReleaseStabilization  `json:"stabilization,omitempty"`
	Control         *ProjectReleaseControl        `json:"control,omitempty"`
	Actions         []string                      `json:"actions,omitempty"`
	Progressive     bool                          `json:"progressive,omitempty"`
	Percent         int                           `json:"percent,omitempty"`
	Generation      uint64                        `json:"generation,omitempty"`
	WorkerHealth    *ProjectWorkerHealth          `json:"worker_health,omitempty"`
	Probes          []ProjectReleaseProbe         `json:"probes"`
	Deadline        *time.Time                    `json:"deadline,omitempty"`
	CheckedAt       *time.Time                    `json:"checked_at,omitempty"`
	HealthyRounds   int                           `json:"healthy_rounds"`
	RegressedRounds int                           `json:"regressed_rounds"`
	Evidence        []ProjectReleaseProbeEvidence `json:"evidence,omitempty"`
}
type ProjectReleaseProbe struct {
	AppID  string `json:"app_id"`
	Slug   string `json:"slug"`
	Method string `json:"method"`
	Path   string `json:"path"`
}
type ProjectReleaseProbeEvidence struct {
	Probe               ProjectReleaseProbe `json:"probe"`
	CandidateRequests   int                 `json:"candidate_requests"`
	CandidateErrors     int                 `json:"candidate_errors"`
	StableRequests      int                 `json:"stable_requests"`
	StableErrors        int                 `json:"stable_errors"`
	Unauthenticated     int                 `json:"unauthenticated"`
	UnexpectedResponses int                 `json:"unexpected_responses"`
}

// ProjectReleaseVerificationPolicyID excludes progress and binds only the probe policy.
func ProjectReleaseVerificationPolicyID(v *ProjectReleaseVerification) string {
	if v == nil {
		return ""
	}
	var policy any = v.Probes
	if v.Progressive {
		policy = struct {
			Probes                    []ProjectReleaseProbe
			WorkerHealth, Progressive bool
		}{v.Probes, v.WorkerHealth != nil, true}
	} else if v.WorkerHealth != nil {
		policy = struct {
			Probes       []ProjectReleaseProbe
			WorkerHealth bool
		}{v.Probes, true}
	}
	if v.Stabilization != nil && v.Stabilization.Enabled {
		policy = struct {
			Policy        any
			Stabilization bool
		}{policy, true}
	}
	data, _ := json.Marshal(policy)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

const ProjectProgressiveHeader = "Project-Progressive"

type projectProgressiveContextKey struct{}

func ContextWithProjectProgressiveRelease(ctx context.Context) context.Context {
	return context.WithValue(ctx, projectProgressiveContextKey{}, true)
}
func projectProgressiveRequested(ctx context.Context) bool {
	requested, _ := ctx.Value(projectProgressiveContextKey{}).(bool)
	return requested
}
