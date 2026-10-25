package api

import (
	"context"
	"time"
)

const ProjectStabilizationHeader = "Project-Stabilization"

type projectStabilizationContextKey struct{}

func ContextWithProjectStabilization(ctx context.Context) context.Context {
	return context.WithValue(ctx, projectStabilizationContextKey{}, true)
}
func projectStabilizationRequested(ctx context.Context) bool {
	requested, _ := ctx.Value(projectStabilizationContextKey{}).(bool)
	return requested
}

type ProjectReleaseStabilization struct {
	Enabled   bool       `json:"enabled"`
	Status    string     `json:"status"`
	Health    string     `json:"health,omitempty"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	Deadline  *time.Time `json:"deadline,omitempty"`
}

func ProjectReleaseMonitoring(safe *ProjectSafeRelease) bool {
	return safe != nil && safe.Verification != nil && safe.Verification.Stabilization != nil && safe.Verification.Stabilization.Enabled && safe.Verification.Stabilization.Status == "monitoring"
}
