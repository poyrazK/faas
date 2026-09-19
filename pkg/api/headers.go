package api

import (
	"net/http"
	"strings"
)

// Request-scoped response headers shared by the gateway and optional edge
// adapters. Keep these names stable: a CDN/Worker may need to recover a
// platform response after replacing an origin error page.
const (
	// RequestIDHeader carries the platform correlation id for every request.
	RequestIDHeader = "X-Faas-Request-Id"
	// TraceIDHeader carries the canonical W3C trace id for a request. Unlike
	// RequestIDHeader, this value is always the 32-character lowercase OTel
	// trace id when tracing is active.
	TraceIDHeader = "X-Gregale-Trace-Id"
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
	// ImageDigestHeader carries the immutable image/artifact digest when the
	// deployment was built from an OCI image.
	ImageDigestHeader = "X-Faas-Image-Digest"
	// InvocationIDHeader carries the durable invocation id for synthetic work
	// and the public request id for direct HTTP function calls.
	InvocationIDHeader = "X-Faas-Invocation-Id"
	// ErrorCodeHeader identifies a platform-owned error independently of the
	// response body. Edge adapters use it to distinguish a Gregale timeout
	// from a genuine CDN/origin failure.
	ErrorCodeHeader = "X-Faas-Error-Code"
)

// PlatformIdentity is the immutable identity of the workload that is about
// to receive a request. It is intentionally transport-neutral: the gateway
// renders it as HTTP headers, while the scheduler renders the equivalent
// FAAS_* environment variables. Keeping the shape here prevents each daemon
// from growing a subtly different list of deployment fields.
//
// Optional provenance fields are left empty when the deployment source does
// not provide them (for example, an image deploy has no Git commit). Callers
// must never fill an unavailable value from customer-controlled input.
type PlatformIdentity struct {
	RequestID           string
	AppID               string
	DeploymentID        string
	TenantID            string
	InstanceID          string
	NodeID              string
	Region              string
	CommitSHA           string
	DeploymentTag       string
	DeploymentCreatedAt string
	ImageDigest         string
}

// ApplyGuestHeaders clears any inbound platform claims and stamps the
// authoritative identity. It also preserves the legacy short headers used by
// the vmmd bridge (x-faas-app/instance/node); those are transport hints, not
// customer-visible identity fields.
func (i PlatformIdentity) ApplyGuestHeaders(h http.Header) {
	if h == nil {
		return
	}
	ClearGuestIdentityHeaders(h)
	set := func(name, value string) {
		if value != "" {
			h.Set(name, value)
		}
	}
	set(RequestIDHeader, i.RequestID)
	set(AppIDHeader, i.AppID)
	set(DeploymentIDHeader, i.DeploymentID)
	set(TenantIDHeader, i.TenantID)
	set(InstanceIDHeader, i.InstanceID)
	set(NodeIDHeader, i.NodeID)
	set(RegionHeader, i.Region)
	set(CommitSHAHeader, i.CommitSHA)
	set(DeploymentTagHeader, i.DeploymentTag)
	set(DeploymentCreatedAtHeader, i.DeploymentCreatedAt)
	set(ImageDigestHeader, i.ImageDigest)
	set("X-Faas-App", i.AppID)
	set("X-Faas-Instance", i.InstanceID)
	set("X-Faas-Node", i.NodeID)
}

// ClearGuestIdentityHeaders removes every reserved identity header before a
// request crosses a trust boundary. RequestID is included because it is
// platform-authored too; callers that need to preserve it should copy it into
// PlatformIdentity.RequestID and call ApplyGuestHeaders afterwards.
func ClearGuestIdentityHeaders(h http.Header) {
	if h == nil {
		return
	}
	for name := range h {
		if IsGuestIdentityHeader(name) {
			h.Del(name)
		}
	}
	for _, name := range []string{"X-Faas-App", "X-Faas-Instance", "X-Faas-Node"} {
		h.Del(name)
	}
}

// IsGuestIdentityHeader reports whether a platform-authored identity header
// may cross the gateway→guest boundary. The handler overwrites these values
// immediately before forwarding; keeping the allowlist here makes the same
// trust policy apply to HTTP/1, streaming, and upgrade forwarding paths.
func IsGuestIdentityHeader(name string) bool {
	switch strings.ToLower(name) {
	case "x-faas-request-id", "x-faas-app-id", "x-faas-deployment-id",
		"x-faas-tenant-id", "x-faas-instance-id", "x-faas-node-id",
		"x-faas-region", "x-faas-commit-sha", "x-faas-deployment-tag",
		"x-faas-deployment-created-at", "x-faas-image-digest":
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
	PlatformImageDigestEnv       = "FAAS_IMAGE_DIGEST"
)

// IsPlatformIdentityEnvKey identifies keys that are owned by Gregale rather
// than by the customer app. It is shared by the scheduler and guest-init so
// the precedence rule remains explicit at both ends of the wake pipeline.
func IsPlatformIdentityEnvKey(key string) bool {
	switch key {
	case PlatformAppIDEnv, PlatformDeploymentIDEnv, PlatformTenantIDEnv,
		PlatformInstanceIDEnv, PlatformNodeIDEnv, PlatformRegionEnv,
		PlatformCommitSHAEnv, PlatformDeploymentTagEnv,
		PlatformDeploymentCreatedEnv, PlatformImageDigestEnv:
		return true
	default:
		return false
	}
}
