package api

import "strings"

// Request-scoped response headers shared by the gateway and optional edge
// adapters. Keep these names stable: a CDN/Worker may need to recover a
// platform response after replacing an origin error page.
const (
	// RequestIDHeader carries the platform correlation id for every request.
	RequestIDHeader = "X-Faas-Request-Id"
	// AppIDHeader identifies the application selected by the gateway.
	AppIDHeader = "X-Faas-App-Id"
	// DeploymentIDHeader identifies the deployment selected by the gateway.
	// The gateway overwrites guest attempts to author this reserved header.
	DeploymentIDHeader = "X-Faas-Deployment-Id"
	// TenantIDHeader identifies the owning account for the selected app.
	TenantIDHeader = "X-Faas-Tenant-Id"
	// InstanceIDHeader identifies the VM that handled the request.
	InstanceIDHeader = "X-Faas-Instance-Id"
	// NodeIDHeader identifies the compute node hosting the VM.
	NodeIDHeader = "X-Faas-Node-Id"
	// RegionHeader carries the compute locality when the edge has it.
	RegionHeader = "X-Faas-Region"
	// CommitSHAHeader carries the immutable source revision when available.
	CommitSHAHeader = "X-Faas-Commit-Sha"
	// DeploymentTagHeader carries the deployment's operator/customer tag.
	DeploymentTagHeader = "X-Faas-Deployment-Tag"
	// DeploymentCreatedAtHeader carries the deployment creation timestamp.
	DeploymentCreatedAtHeader = "X-Faas-Deployment-Created-At"
	// InvocationIDHeader carries the durable invocation id for synthetic work
	// and the public request id for direct HTTP function calls.
	InvocationIDHeader = "X-Faas-Invocation-Id"
	// ErrorCodeHeader identifies a platform-owned error independently of the
	// response body. Edge adapters use it to distinguish a Gregale timeout
	// from a genuine CDN/origin failure.
	ErrorCodeHeader = "X-Faas-Error-Code"
)

// IsGuestIdentityHeader reports whether a platform-authored identity header
// may cross the gateway→guest boundary. The handler overwrites these values
// immediately before forwarding; keeping the allowlist here makes the same
// trust policy apply to HTTP/1, streaming, and upgrade forwarding paths.
func IsGuestIdentityHeader(name string) bool {
	switch strings.ToLower(name) {
	case "x-faas-request-id", "x-faas-app-id", "x-faas-deployment-id",
		"x-faas-tenant-id", "x-faas-instance-id", "x-faas-node-id",
		"x-faas-region", "x-faas-commit-sha", "x-faas-deployment-tag",
		"x-faas-deployment-created-at":
		return true
	default:
		return false
	}
}

// Platform identity variables are injected by schedd into the workload's
// process environment. They use a reserved namespace so guest-init can keep
// them authoritative even when a customer image or secret uses the same key.
const (
	PlatformAppIDEnv             = "FAAS_APP_ID"
	PlatformDeploymentIDEnv      = "FAAS_DEPLOYMENT_ID"
	PlatformTenantIDEnv          = "FAAS_TENANT_ID"
	PlatformInstanceIDEnv        = "FAAS_INSTANCE_ID"
	PlatformNodeIDEnv            = "FAAS_NODE_ID"
	PlatformRegionEnv            = "FAAS_REGION"
	PlatformCommitSHAEnv         = "FAAS_COMMIT_SHA"
	PlatformDeploymentTagEnv     = "FAAS_DEPLOYMENT_TAG"
	PlatformDeploymentCreatedEnv = "FAAS_DEPLOYMENT_CREATED_AT"
)

// IsPlatformIdentityEnvKey identifies keys that are owned by Gregale rather
// than by the customer app. It is shared by the scheduler and guest-init so
// the precedence rule remains explicit at both ends of the wake pipeline.
func IsPlatformIdentityEnvKey(key string) bool {
	switch key {
	case PlatformAppIDEnv, PlatformDeploymentIDEnv, PlatformTenantIDEnv,
		PlatformInstanceIDEnv, PlatformNodeIDEnv, PlatformRegionEnv,
		PlatformCommitSHAEnv, PlatformDeploymentTagEnv,
		PlatformDeploymentCreatedEnv:
		return true
	default:
		return false
	}
}
