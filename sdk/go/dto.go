package faas

import "github.com/poyrazK/faas-go/internal/api"

// Type aliases for every request/response DTO the Client methods
// accept and return. The aliases preserve identity (a faas.App
// IS an api.App), so methods on either type are interchangeable.
//
// Why aliases instead of wrappers:
//   - 44 DTOs would be ~440 lines of empty wrapper structs.
//   - Identity lets us add fields in internal/api without breaking
//     customers (the alias picks them up automatically).
//   - Identity is the only thing godoc needs to render the
//     relationship correctly.
//
// Every alias here is a peer of an exported type in internal/api
// (dto.go, build.go, appmanifest.go, cliauth.go, secrets.go). New
// DTOs in internal/api should be added here on the next PR.
type (
	// App lifecycle.
	CreateAppRequest   = api.CreateAppRequest
	UpdateAppRequest   = api.UpdateAppRequest
	RenameAppRequest   = api.RenameAppRequest
	AppResponse        = api.AppResponse
	AppEffectiveLimits = api.AppEffectiveLimits

	// Deployments.
	CreateDeploymentRequest = api.CreateDeploymentRequest
	DeploymentResponse      = api.DeploymentResponse
	DeploymentListResponse  = api.DeploymentListResponse

	// Account.
	AccountResponse         = api.AccountResponse
	AccountLimits           = api.AccountLimits
	AccountDeletionResponse = api.AccountDeletionResponse
	AccountExportResponse   = api.AccountExportResponse
	BuildExportResponse     = api.BuildExportResponse
	UsageExportResponse     = api.UsageExportResponse
	APIKeyExportResponse    = api.APIKeyExportResponse
	GdprAuditExportResponse = api.GdprAuditExportResponse
	AppSecretExportResponse = api.AppSecretExportResponse
	StatusPage              = api.StatusPage

	// API keys.
	APIKeyResponse   = api.APIKeyResponse
	CreateKeyRequest = api.CreateKeyRequest

	// Custom domains.
	CustomDomainResponse      = api.CustomDomainResponse
	CreateCustomDomainRequest = api.CreateCustomDomainRequest

	// Crons.
	CronResponse      = api.CronResponse
	CreateCronRequest = api.CreateCronRequest
	UpdateCronRequest = api.UpdateCronRequest

	// Instances.
	InstanceResponse = api.InstanceResponse

	// Usage.
	UsageResponse        = api.UsageResponse
	UsageSummaryResponse = api.UsageSummaryResponse

	// Auth (password).
	PasswordLoginRequest  = api.PasswordLoginRequest
	PasswordLoginResponse = api.PasswordLoginResponse
	PasswordSignupRequest = api.PasswordSignupRequest
	PasswordResetRequest  = api.PasswordResetRequest
	PasswordResetConfirm  = api.PasswordResetConfirm
	SetPasswordRequest    = api.SetPasswordRequest

	// Auth (OAuth + device code).
	OAuthProvider           = api.OAuthProvider
	CliAuthStatus           = api.CliAuthStatus
	CliAuthCodeResponse     = api.CliAuthCodeResponse
	CliAuthExchangeRequest  = api.CliAuthExchangeRequest
	CliAuthExchangeResponse = api.CliAuthExchangeResponse

	// Async + queues + delayed tasks.
	AsyncInvokeResponse  = api.AsyncInvokeResponse
	InvokeResponse       = api.InvokeResponse
	InvokeRequest        = api.InvokeRequest
	QueueSendRequest     = api.QueueSendRequest
	QueueSendResponse    = api.QueueSendResponse
	QueueReceiveResponse = api.QueueReceiveResponse
	DelayedTaskRequest   = api.DelayedTaskRequest
	DelayedTaskResponse  = api.DelayedTaskResponse

	// Audit + invocations.
	Invocation              = api.Invocation
	ListInvocationsResponse = api.ListInvocationsResponse
	AuditEventResponse      = api.AuditEventResponse
	ListAuditEventsResponse = api.ListAuditEventsResponse

	// Wake timeline (issue #517 / PR-C / ADR-064).
	WakeTimelineEvent    = api.WakeTimelineEvent
	WakeTimelineResponse = api.WakeTimelineResponse

	// Secrets.
	AppSecretListResponse = api.AppSecretListResponse
	PutAppSecretRequest   = api.PutAppSecretRequest
	AppSecretResponse     = api.AppSecretResponse

	// Build + manifest.
	BuildManifest   = api.BuildManifest
	BuildDone       = api.BuildDone
	BuildFramework  = api.BuildFramework
	AppManifest     = api.AppManifest
	ServiceReplicas = api.ServiceReplicas

	// Issue #477 / ADR-079: per-app public-URL auth. Both
	// the write-block (PublicAuthBlock, embedded on
	// UpdateAppRequest) and the read-side status
	// (PublicAuthStatus, embedded on AppResponse) are
	// re-exported so a caller can read the resolved mode +
	// has_basic_creds bool off AppResponse without going
	// through the internal package. The alias preserves
	// identity (a faas.PublicAuthStatus IS an
	// api.PublicAuthStatus), so the explanatory godoc on
	// internal/api.PublicAuthStatus renders correctly.
	PublicAuthBlock  = api.PublicAuthBlock
	PublicAuthStatus = api.PublicAuthStatus

	// Issue #679 / PR-B / ADR-082: per-account additive budget
	// on top of the plan's apps.egress_allowlist cap. The
	// write-side request is mirrored as an alias so the
	// Client.SetEgressAllowlistExtra body shape is identical
	// to the SDK's other admin-scope setters (ChangePlan,
	// RaiseOverageCap).
	SetAccountEgressAllowlistExtraRequest = api.SetAccountEgressAllowlistExtraRequest
	AccountEgressAllowlistExtraResponse   = api.AccountEgressAllowlistExtraResponse
)
