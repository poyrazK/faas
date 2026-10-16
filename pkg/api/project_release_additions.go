package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

const ProjectAddServicesHeader = "Project-Add-Services"

// ProjectReleaseAddition binds the planned source-owned settings of a new
// service. AppID is assigned only when the durable attempt reserves the app.
type ProjectReleaseAddition struct {
	AppID       string `json:"app_id,omitempty"`
	Slug        string `json:"slug"`
	RootDir     string `json:"root_dir"`
	Fingerprint string `json:"fingerprint"`
}

func ProjectReleaseAdditionsPolicyID(additions []ProjectReleaseAddition) string {
	policy := append([]ProjectReleaseAddition(nil), additions...)
	for i := range policy {
		policy[i].AppID = ""
	}
	data, _ := json.Marshal(policy)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// ProjectSafeReleasePolicyID excludes progress and physical reservation IDs.
func ProjectSafeReleasePolicyID(safe *ProjectSafeRelease) string {
	if safe == nil {
		return ""
	}
	identity := safe.PreviousReleaseID
	if safe.AddServices {
		identity += ":add-services:" + ProjectReleaseAdditionsPolicyID(safe.Additions)
	}
	if safe.Verification != nil {
		identity += ":" + ProjectReleaseVerificationPolicyID(safe.Verification)
	}
	if safe.Configuration != nil {
		identity += ":" + ProjectReleaseConfigurationPolicyID(safe.Configuration)
	}
	if safe.ReleaseCommand != nil {
		identity += ":backward-compatible-migration"
	}
	if safe.Worker != nil {
		identity += ":" + ProjectReleaseWorkerPolicyID(safe.Worker)
	}
	return identity
}

type projectAddServicesKey struct{}

func ContextWithProjectServiceAdditions(ctx context.Context) context.Context {
	if projectSafeReleaseMode(ctx) == "" {
		ctx = ContextWithProjectSafeRelease(ctx)
	}
	return context.WithValue(ctx, projectAddServicesKey{}, true)
}

func projectServiceAdditionsRequested(ctx context.Context) bool {
	requested, _ := ctx.Value(projectAddServicesKey{}).(bool)
	return requested
}
