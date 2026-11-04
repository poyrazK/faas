package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// CreateProjectEnvironmentBranch branches environmentSlug into req.Slug.
func (c *Client) CreateProjectEnvironmentBranch(ctx context.Context, projectSlug, environmentSlug string, req CreateProjectEnvironmentBranchRequest) (ProjectEnvironmentResponse, error) {
	var out ProjectEnvironmentResponse
	path := "/v1/projects/" + url.PathEscape(projectSlug) + "/environments/" + url.PathEscape(environmentSlug) + "/branches"
	return out, c.do(ctx, http.MethodPost, path, req, &out)
}

// ListProjectEnvironmentBranches lists the direct branches of environmentSlug.
func (c *Client) ListProjectEnvironmentBranches(ctx context.Context, projectSlug, environmentSlug string) (ProjectEnvironmentBranchListResponse, error) {
	var out ProjectEnvironmentBranchListResponse
	path := "/v1/projects/" + url.PathEscape(projectSlug) + "/environments/" + url.PathEscape(environmentSlug) + "/branches"
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// Environment branches (ADR-20261031193635): a lazy copy of a project
// environment. Creating one copies configuration and publishes the parent's
// built artifacts; nothing boots until a workload is called.

const (
	EnvironmentBranchEgressLive = "live"
	// EnvironmentBranchEgressContained keeps a branch's guests to the
	// platform services of their branch (ADR-20261031193635 section 7).
	EnvironmentBranchEgressContained = "contained"
	// EnvironmentBranchContainmentEnv is apid's operator gate for contained
	// branches: set it to 1 only once every serving vmmd, schedd and
	// gatewayd-internal enforces the contained profile.
	EnvironmentBranchContainmentEnv = "FAAS_ENVIRONMENT_BRANCH_CONTAINMENT"

	// CodeEnvironmentBranchUnavailable: branches are switched off.
	CodeEnvironmentBranchUnavailable = "environment_branch_unavailable"
	// CodeEnvironmentBranchManagedData: the parent binds managed data a
	// branch cannot copy. Managed PostgreSQL and object storage are copied
	// after creation (ADR-20261031193635 slices 2 and 3).
	CodeEnvironmentBranchManagedData = "environment_branch_managed_data_unsupported"
	// CodeEnvironmentBranchDepth: a branch of a branch went too deep.
	CodeEnvironmentBranchDepth = "environment_branch_depth_exceeded"
	// CodeEnvironmentBranchPrivateNetwork: a project cannot have both
	// environment branches and a workload on a private network yet.
	CodeEnvironmentBranchPrivateNetwork = "environment_branch_private_network_unsupported"
	// CodeEnvironmentBranchContainedObjects: a contained branch cannot use
	// object storage yet; its overlay buckets are served by the public S3
	// gateway, which contained guests do not reach.
	CodeEnvironmentBranchContainedObjects = "environment_branch_contained_objects_unsupported"
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
	Parent      string `json:"parent"`
	BranchPoint string `json:"branch_point"`
	ExpiresAt   string `json:"expires_at"`
	Egress      string `json:"egress"`
	Depth       int    `json:"depth"`
	// Data is ready, pending or failed. A branch whose parent binds managed
	// PostgreSQL copies each database as of the branch point; its workloads
	// start only once the copies exist.
	Data      string                             `json:"data"`
	DataError string                             `json:"data_error,omitempty"`
	Workloads []ProjectEnvironmentBranchWorkload `json:"workloads,omitempty"`
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
	Data        string `json:"data"`
	DataError   string `json:"data_error,omitempty"`
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
