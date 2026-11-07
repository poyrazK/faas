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

// SeedDevSessionFromEnvironmentBranch deploys a simulated branch's workload
// into the developer session of a rehearsal run (ADR-20261031193635 slice 6b).
func (c *Client) SeedDevSessionFromEnvironmentBranch(ctx context.Context, project string, req SeedDevSessionFromEnvironmentBranchRequest) (SeedDevSessionFromEnvironmentBranchResponse, error) {
	var out SeedDevSessionFromEnvironmentBranchResponse
	return out, c.do(ctx, http.MethodPost, "/v1/dev/sessions/"+url.PathEscape(project)+"/seed", req, &out)
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
	// EnvironmentBranchEgressSimulated is contained, and the branch can seed
	// rehearsal twins whose side effects are simulated and recorded
	// (ADR-20261031193635 slice 6b).
	EnvironmentBranchEgressSimulated = "simulated"
	// EnvironmentBranchPrivateNetworksOff keeps a branch's instances off the
	// private networks their workloads are attached to (the default);
	// EnvironmentBranchPrivateNetworksJoin attaches them with addresses of
	// their own, never a workload's service address (ADR-20261031193635
	// section 6).
	EnvironmentBranchPrivateNetworksOff  = "off"
	EnvironmentBranchPrivateNetworksJoin = "join"
	// EnvironmentBranchContainmentEnv is apid's operator gate for contained
	// branches: set it to 1 only once every serving vmmd, schedd and
	// gatewayd-internal enforces the contained profile.
	EnvironmentBranchContainmentEnv = "FAAS_ENVIRONMENT_BRANCH_CONTAINMENT"
	// EnvironmentBranchContainedObjectsEnv is apid's operator gate for
	// contained branches of environments with object storage: set it to 1
	// only once every serving gatewayd-internal runs the bridge object relay,
	// every vmmd admits its port and every s3-gatewayd accepts its host
	// (ADR-20261031193635 slice 6c).
	EnvironmentBranchContainedObjectsEnv = "FAAS_ENVIRONMENT_BRANCH_CONTAINED_OBJECTS"
	// EnvironmentBranchObjectRelayHost is the name a contained branch guest
	// dials for object storage. The guest resolver answers it with the
	// node's tenant bridge address, so it follows the guest to any node; it
	// is outside svc.gregale, so no workload name can take it.
	EnvironmentBranchObjectRelayHost = "objects.gregale"
	// EnvironmentBranchObjectRelayPort is the relay's bridge port.
	EnvironmentBranchObjectRelayPort = 10085

	// CodeEnvironmentBranchUnavailable: branches are switched off.
	CodeEnvironmentBranchUnavailable = "environment_branch_unavailable"
	// CodeEnvironmentBranchManagedData: the parent binds managed data a
	// branch cannot copy. Managed PostgreSQL and object storage are copied
	// after creation (ADR-20261031193635 slices 2 and 3).
	CodeEnvironmentBranchManagedData = "environment_branch_managed_data_unsupported"
	// CodeEnvironmentBranchDepth: a branch of a branch went too deep.
	CodeEnvironmentBranchDepth = "environment_branch_depth_exceeded"
	// CodeEnvironmentBranchContainedObjects: a contained branch cannot use
	// object storage while the bridge object relay is switched off
	// (EnvironmentBranchContainedObjectsEnv); its overlay buckets are then
	// served only by the public S3 gateway, which contained guests do not reach.
	CodeEnvironmentBranchContainedObjects = "environment_branch_contained_objects_unsupported"
	// CodeEnvironmentBranchNotSimulated: only a simulated branch seeds a
	// rehearsal twin.
	CodeEnvironmentBranchNotSimulated = "environment_branch_not_simulated"
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
	// Egress is "live" (the default), "contained" or "simulated".
	Egress string `json:"egress,omitempty"`
	// PrivateNetworks is "off" (the default) or "join": whether the
	// branch's instances join the private networks their workloads are
	// attached to. Only a live branch may join.
	PrivateNetworks string `json:"private_networks,omitempty"`
}

// SeedDevSessionFromEnvironmentBranchRequest is POST
// /v1/dev/sessions/{project}/seed: it deploys one workload of a simulated
// environment branch into the developer session of WorkspaceID (a rehearsal
// run member), reusing the branch's build (ADR-20261031193635 slice 6b).
type SeedDevSessionFromEnvironmentBranchRequest struct {
	WorkspaceID string `json:"workspace_id"`
	// Project, Environment and Workload name the branch workload.
	Project     string `json:"project"`
	Environment string `json:"environment"`
	Workload    string `json:"workload"`
}

// SeedDevSessionFromEnvironmentBranchResponse names the member's deployment,
// which goes live after one boot.
type SeedDevSessionFromEnvironmentBranchResponse struct {
	AppSlug      string `json:"app_slug"`
	DeploymentID string `json:"deployment_id"`
	Status       string `json:"status"`
}

// ProjectEnvironmentBranchResponse describes a branch and the workloads it
// published from its parent.
type ProjectEnvironmentBranchResponse struct {
	Parent      string `json:"parent"`
	BranchPoint string `json:"branch_point"`
	ExpiresAt   string `json:"expires_at"`
	Egress      string `json:"egress"`
	// PrivateNetworks is off or join.
	PrivateNetworks string `json:"private_networks"`
	Depth           int    `json:"depth"`
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
	// PrivateNetworks is off or join.
	PrivateNetworks string `json:"private_networks"`
	Depth           int    `json:"depth"`
	Data            string `json:"data"`
	DataError       string `json:"data_error,omitempty"`
	CreatedAt       string `json:"created_at"`
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

// EnvironmentBranchObjectRelayEndpoint is the S3 endpoint a contained
// branch's object storage binding names (ADR-20261031193635 slice 6c).
func EnvironmentBranchObjectRelayEndpoint() string {
	return fmt.Sprintf("http://%s:%d", EnvironmentBranchObjectRelayHost, EnvironmentBranchObjectRelayPort)
}
