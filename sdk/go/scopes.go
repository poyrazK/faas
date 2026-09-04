package faas

import "github.com/poyrazK/faas-go/internal/api"

// Plan and Limits re-exports. The Plan enum is the customer-facing
// plan identifier (free / hobby / pro / scale); the Limits struct
// is the per-plan quota set returned by /v1/account. Server-only
// helpers (MustLimitsFor, ConntrackCapProbe, planLimits table) stay
// in internal/api/limits.go and are not part of the public SDK —
// the spec documents limits.go as the single source of truth, so
// any quota change there is canonical, and the SDK's Limits type
// is the wire form.
type (
	// Plan wraps api.Plan — identity-preserving alias so a customer
	// can use faas.Plan anywhere an api.Plan is expected.
	Plan = api.Plan
	// Limits wraps api.Limits — same identity guarantee as Plan.
	Limits = api.Limits
)

// Plans wraps api.Plans ([]Plan low-to-high) so callers iterating
// plans (e.g. a dashboard upgrade page) don't have to import
// internal/api.
var Plans = api.Plans

// Plan enum re-exports.
const (
	PlanFree  = api.PlanFree
	PlanHobby = api.PlanHobby
	PlanPro   = api.PlanPro
	PlanScale = api.PlanScale
)

// API key scope vocabulary (IAM-1). The closed set is enforced
// server-side by migration 00044's api_keys_scopes_vocab_chk CHECK
// constraint; the SDK re-exports the constants so customers can
// pass them to CreateKey without a string-literal risk.
const (
	ScopeAdmin        = api.ScopeAdmin
	ScopeAppsRead     = api.ScopeAppsRead
	ScopeDeployWrite  = api.ScopeDeployWrite
	ScopeSecretsRead  = api.ScopeSecretsRead
	ScopeSecretsWrite = api.ScopeSecretsWrite
	ScopeUsageRead    = api.ScopeUsageRead
)

// IsValidScope wraps api.IsValidScope (canonical definition). Use the
// faas re-export so callers don't have to import internal/api.
func IsValidScope(s string) bool { return api.IsValidScope(s) }

// NormalizeCreateKeyScopes wraps api.NormalizeCreateKeyScopes:
// validates, defaults, and dedupes a slice of requested scopes for
// the CreateKey method.
func NormalizeCreateKeyScopes(requested []string) ([]string, error) {
	return api.NormalizeCreateKeyScopes(requested)
}

// Build framework enum re-exports. The framework is the customer's
// preferred build pipeline (Railpack for Node/Python/Go, Dockerfile
// for custom builds, or Auto for server-side detection).
const (
	FrameworkRailpackNode   = api.FrameworkRailpackNode
	FrameworkRailpackPython = api.FrameworkRailpackPython
	FrameworkRailpackGo     = api.FrameworkRailpackGo
	FrameworkDockerfile     = api.FrameworkDockerfile
	FrameworkAuto           = api.FrameworkAuto
)

// OAuth provider re-exports.
const (
	OAuthProviderGoogle = api.OAuthProviderGoogle
	OAuthProviderGitHub = api.OAuthProviderGitHub
)

// CLI auth event + status re-exports.
const (
	EventCliAuthAutoCreated = api.EventCliAuthAutoCreated

	CliAuthStatusPending  = api.CliAuthStatusPending
	CliAuthStatusConsumed = api.CliAuthStatusConsumed
	CliAuthStatusExpired  = api.CliAuthStatusExpired
)

// Secret-key pattern + max length (re-exported for client-side
// validation; the server enforces the same constraints).
const (
	SecretKeyPattern = api.SecretKeyPattern
	MaxSecretKeyLen  = api.MaxSecretKeyLen
)

// App manifest defaults re-exports. Customers writing their own
// manifest file (e.g. via /v1/apps/{slug}/deployments JSON variant)
// use these to know the daemon's expectations.
const (
	AppManifestPath = api.AppManifestPath
	DefaultAppPort  = api.DefaultAppPort
	DefaultAppUser  = api.DefaultAppUser
	DefaultAppUID   = api.DefaultAppUID
)

// App lifecycle enum re-exports.
const (
	ExecutionModeRequest = api.ExecutionModeRequest
	ExecutionModeService = api.ExecutionModeService
	ExecutionModeWorker  = api.ExecutionModeWorker
	ExecutionModeJob     = api.ExecutionModeJob

	RestartPolicyNo            = api.RestartPolicyNo
	RestartPolicyOnFailure     = api.RestartPolicyOnFailure
	RestartPolicyAlways        = api.RestartPolicyAlways
	RestartPolicyUnlessStopped = api.RestartPolicyUnlessStopped
)
