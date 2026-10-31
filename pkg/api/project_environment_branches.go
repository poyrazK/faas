package api

import (
	"fmt"
	"time"
)

// Environment branches (ADR-20261031193635): a lazy copy of a project
// environment. Creating one copies configuration and publishes the parent's
// built artifacts; nothing boots until a workload is called.

const (
	EnvironmentBranchEgressLive = "live"

	// CodeEnvironmentBranchUnavailable: branches are switched off.
	CodeEnvironmentBranchUnavailable = "environment_branch_unavailable"
	// CodeEnvironmentBranchManagedData: the parent binds managed PostgreSQL or
	// object storage, which branches cannot copy yet.
	CodeEnvironmentBranchManagedData = "environment_branch_managed_data_unsupported"
	// CodeEnvironmentBranchDepth: a branch of a branch went too deep.
	CodeEnvironmentBranchDepth = "environment_branch_depth_exceeded"
	// CodeEnvironmentBranchHasBranches: delete the branches of an
	// environment before the environment itself.
	CodeEnvironmentBranchHasBranches = "environment_has_branches"
)

// CreateProjectEnvironmentBranchRequest is POST
// /v1/projects/{slug}/environments/{environment}/branches.
type CreateProjectEnvironmentBranchRequest struct {
	Slug string `json:"slug"`
	// TTL is a duration such as "24h". Empty takes the default.
	TTL string `json:"ttl,omitempty"`
	// Egress is "live" (the default). Contained and simulated egress come later.
	Egress string `json:"egress,omitempty"`
}

// ProjectEnvironmentBranchResponse describes a branch and the workloads it
// published from its parent.
type ProjectEnvironmentBranchResponse struct {
	Parent      string                             `json:"parent"`
	BranchPoint string                             `json:"branch_point"`
	ExpiresAt   string                             `json:"expires_at"`
	Egress      string                             `json:"egress"`
	Depth       int                                `json:"depth"`
	Workloads   []ProjectEnvironmentBranchWorkload `json:"workloads,omitempty"`
}

// ProjectEnvironmentBranchWorkload is one workload a branch published from
// its parent's artifact. It activates on its first request.
type ProjectEnvironmentBranchWorkload struct {
	Workload         string `json:"workload"`
	DeploymentID     string `json:"deployment_id"`
	SourceDeployment string `json:"source_deployment_id"`
	Status           string `json:"status"`
}

// ProjectEnvironmentBranchListResponse lists a project's branches.
type ProjectEnvironmentBranchListResponse struct {
	Branches []ProjectEnvironmentBranchSummary `json:"branches"`
}

// ProjectEnvironmentBranchSummary is one row of the branch list.
type ProjectEnvironmentBranchSummary struct {
	Slug        string `json:"slug"`
	Parent      string `json:"parent"`
	BranchPoint string `json:"branch_point"`
	ExpiresAt   string `json:"expires_at"`
	Egress      string `json:"egress"`
	Depth       int    `json:"depth"`
	CreatedAt   string `json:"created_at"`
}

// ParseEnvironmentBranchTTL returns the requested TTL, or the default when
// empty, bounded to [EnvironmentBranchMinTTL, EnvironmentBranchMaxTTL].
func ParseEnvironmentBranchTTL(raw string) (time.Duration, error) {
	if raw == "" {
		return EnvironmentBranchDefaultTTL, nil
	}
	ttl, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("ttl %q is not a duration such as 24h", raw)
	}
	if ttl < EnvironmentBranchMinTTL || ttl > EnvironmentBranchMaxTTL {
		return 0, fmt.Errorf("ttl must be between %s and %s", EnvironmentBranchMinTTL, EnvironmentBranchMaxTTL)
	}
	return ttl, nil
}
