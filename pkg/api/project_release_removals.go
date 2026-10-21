package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

const ProjectRemoveServicesHeader = "Project-Remove-Services"

// ProjectReleaseRemoval names a serving service that an atomic release retires
// (ADR-20261021120000). The fingerprint binds the app's settings at planning;
// the app stays retained for rollback until it is deleted.
type ProjectReleaseRemoval struct {
	AppID       string `json:"app_id"`
	Slug        string `json:"slug"`
	RootDir     string `json:"root_dir"`
	Fingerprint string `json:"fingerprint"`
}

func ProjectReleaseRemovalsPolicyID(removals []ProjectReleaseRemoval) string {
	data, _ := json.Marshal(removals)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

type projectRemoveServicesKey struct{}

// ContextWithProjectServiceRemovals opts an atomic project deploy into retiring
// serving services that the source no longer declares.
func ContextWithProjectServiceRemovals(ctx context.Context) context.Context {
	if projectSafeReleaseMode(ctx) == "" {
		ctx = ContextWithProjectSafeRelease(ctx)
	}
	return context.WithValue(ctx, projectRemoveServicesKey{}, true)
}

func projectServiceRemovalsRequested(ctx context.Context) bool {
	requested, _ := ctx.Value(projectRemoveServicesKey{}).(bool)
	return requested
}
