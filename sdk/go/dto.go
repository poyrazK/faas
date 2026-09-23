package faas

import "github.com/poyrazK/faas/sdk/go/internal/api"

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
	CreateAppRequest       = api.CreateAppRequest
	UpdateAppRequest       = api.UpdateAppRequest
	RenameAppRequest       = api.RenameAppRequest
	AppResponse            = api.AppResponse
	AppEffectiveLimits     = api.AppEffectiveLimits
	AppConfiguredResources = api.AppConfiguredResources
	AppServiceBinding      = api.AppServiceBinding
	DeclaredRoute          = api.DeclaredRoute
	RetryPolicyDTO         = api.RetryPolicyDTO

	// End-customer consumers and credentials (ADR-120).
	CreateAPIConsumerRequest                 = api.CreateAPIConsumerRequest
	APIConsumerResponse                      = api.APIConsumerResponse
	APIConsumerListResponse                  = api.APIConsumerListResponse
	CreateConsumerKeyRequest                 = api.CreateConsumerKeyRequest
	ConsumerKeyResponse                      = api.ConsumerKeyResponse
	ConsumerKeyListResponse                  = api.ConsumerKeyListResponse
	APIConsumerUsageBucketResponse           = api.APIConsumerUsageBucketResponse
	APIConsumerUsageResponse                 = api.APIConsumerUsageResponse
	CreateAPIConsumerRateCardRequest         = api.CreateAPIConsumerRateCardRequest
	APIConsumerRateCardResponse              = api.APIConsumerRateCardResponse
	APIConsumerRateCardListResponse          = api.APIConsumerRateCardListResponse
	APIConsumerUsageQuoteBucketResponse      = api.APIConsumerUsageQuoteBucketResponse
	APIConsumerUsageQuoteResponse            = api.APIConsumerUsageQuoteResponse
	CreateAPIConsumerUsageStatementRequest   = api.CreateAPIConsumerUsageStatementRequest
	APIConsumerUsageStatementBucketResponse  = api.APIConsumerUsageStatementBucketResponse
	APIConsumerUsageStatementResponse        = api.APIConsumerUsageStatementResponse
	APIConsumerUsageStatementListResponse    = api.APIConsumerUsageStatementListResponse
	ClaimAPIConsumerUsageStatementRequest    = api.ClaimAPIConsumerUsageStatementRequest
	APIConsumerUsageStatementHandoffResponse = api.APIConsumerUsageStatementHandoffResponse
	ResourceProfile                          = api.ResourceProfile
	ResourceProfileSpec                      = api.ResourceProfileSpec

	// Deployments.
	CreateDeploymentRequest        = api.CreateDeploymentRequest
	DeploymentResponse             = api.DeploymentResponse
	DeploymentListResponse         = api.DeploymentListResponse
	LatestDeploymentsByAppResponse = api.LatestDeploymentsByAppResponse
	PreviewStatusResponse          = api.PreviewStatusResponse

	// Account.
	RepoResponse            = api.RepoResponse
	AccountResponse         = api.AccountResponse
	CapabilitiesResponse    = api.CapabilitiesResponse
	CapabilityStatus        = api.CapabilityStatus
	AccountLimits           = api.AccountLimits
	AccountDeletionResponse = api.AccountDeletionResponse
	AccountExportResponse   = api.AccountExportResponse
	BuildExportResponse     = api.BuildExportResponse
	UsageExportResponse     = api.UsageExportResponse
	APIKeyExportResponse    = api.APIKeyExportResponse
	GdprAuditExportResponse = api.GdprAuditExportResponse
	AppSecretExportResponse = api.AppSecretExportResponse
	StatusPage              = api.StatusPage
	StatusUptimeBucket      = api.StatusUptimeBucket
	StatusIncident          = api.StatusIncident

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

	// Jobs (issue #1184 Workstream A).
	CreateJobRequest        = api.CreateJobRequest
	UpdateJobRequest        = api.UpdateJobRequest
	CreateJobRunRequest     = api.CreateJobRunRequest
	JobResponse             = api.JobResponse
	JobRunResponse          = api.JobRunResponse
	JobTaskResponse         = api.JobTaskResponse
	JobTaskLogResponse      = api.JobTaskLogResponse
	JobTaskRetryResponse    = api.JobTaskRetryResponse
	ListJobsResponse        = api.ListJobsResponse
	ListJobRunsResponse     = api.ListJobRunsResponse
	ListJobTasksResponse    = api.ListJobTasksResponse
	JobRunCancelledResponse = api.JobRunCancelledResponse
	JobDeletedResponse      = api.JobDeletedResponse

	// Instances.
	InstanceResponse = api.InstanceResponse

	// Usage.
	UsageResponse                 = api.UsageResponse
	UsageSummaryResponse          = api.UsageSummaryResponse
	ExecutionUsageSummaryResponse = api.ExecutionUsageSummaryResponse
	AccountUsageResponse          = api.AccountUsageResponse
	ObjectStorageUsageResponse    = api.ObjectStorageUsageResponse
	ManagedPostgresUsageResponse  = api.ManagedPostgresUsageResponse

	// Disposable agent executions. Source and input are accepted only by
	// CreateExecutionRequest and are never returned in execution receipts.
	ExecutionRuntime         = api.ExecutionRuntime
	ExecutionNetworkMode     = api.ExecutionNetworkMode
	ExecutionNetworkPolicy   = api.ExecutionNetworkPolicy
	ExecutionLimitRequest    = api.ExecutionLimitRequest
	ExecutionFile            = api.ExecutionFile
	CreateExecutionRequest   = api.CreateExecutionRequest
	ResolvedExecutionLimits  = api.ResolvedExecutionLimits
	ExecutionUsage           = api.ExecutionUsage
	ExecutionFailure         = api.ExecutionFailure
	ExecutionStatus          = api.ExecutionStatus
	ExecutionResponse        = api.ExecutionResponse
	ExecutionListResponse    = api.ExecutionListResponse
	ExecutionEventType       = api.ExecutionEventType
	ExecutionEventData       = api.ExecutionEventData
	ExecutionEvent           = api.ExecutionEvent
	ExecutionEventParseError = api.ExecutionEventParseError
	WatchExecutionOptions    = api.WatchExecutionOptions
	ExecutionWatcher         = api.ExecutionWatcher
	RunOptions               = api.RunOptions

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
	AsyncInvokeResponse      = api.AsyncInvokeResponse
	InvokeResponse           = api.InvokeResponse
	InvokeRequest            = api.InvokeRequest
	InvocationDestinations   = api.InvocationDestinations
	QueueSendRequest         = api.QueueSendRequest
	QueueSendResponse        = api.QueueSendResponse
	QueueReceiveResponse     = api.QueueReceiveResponse
	DelayedTaskRequest       = api.DelayedTaskRequest
	DelayedTaskResponse      = api.DelayedTaskResponse
	ListDelayedTasksResponse = api.ListDelayedTasksResponse

	// Audit + invocations.
	Invocation               = api.Invocation
	ListInvocationsResponse  = api.ListInvocationsResponse
	AuditEventResponse       = api.AuditEventResponse
	ListAuditEventsResponse  = api.ListAuditEventsResponse
	ActivityActorResponse    = api.ActivityActorResponse
	ActivityResourceResponse = api.ActivityResourceResponse
	OrgActivityResponse      = api.OrgActivityResponse
	ListOrgActivityResponse  = api.ListOrgActivityResponse

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

const (
	ResourceProfileMicro     = api.ResourceProfileMicro
	ResourceProfileSmall     = api.ResourceProfileSmall
	ResourceProfileMedium    = api.ResourceProfileMedium
	ResourceProfileLarge     = api.ResourceProfileLarge
	ResourceProfileXLarge    = api.ResourceProfileXLarge
	ConsumerAuthModeOptional = api.ConsumerAuthModeOptional
	ConsumerAuthModeRequired = api.ConsumerAuthModeRequired

	ExecutionRuntimeNode22    = api.ExecutionRuntimeNode22
	ExecutionRuntimeNode24    = api.ExecutionRuntimeNode24
	ExecutionRuntimePython312 = api.ExecutionRuntimePython312
	ExecutionRuntimePython313 = api.ExecutionRuntimePython313
	ExecutionNetworkNone      = api.ExecutionNetworkNone

	ExecutionStatusQueued      = api.ExecutionStatusQueued
	ExecutionStatusRestoring   = api.ExecutionStatusRestoring
	ExecutionStatusRunning     = api.ExecutionStatusRunning
	ExecutionStatusSucceeded   = api.ExecutionStatusSucceeded
	ExecutionStatusFailed      = api.ExecutionStatusFailed
	ExecutionStatusTimedOut    = api.ExecutionStatusTimedOut
	ExecutionStatusOutOfMemory = api.ExecutionStatusOutOfMemory
	ExecutionStatusCancelled   = api.ExecutionStatusCancelled

	ExecutionEventStatus   = api.ExecutionEventStatus
	ExecutionEventStdout   = api.ExecutionEventStdout
	ExecutionEventStderr   = api.ExecutionEventStderr
	ExecutionEventTerminal = api.ExecutionEventTerminal
	ExecutionEventError    = api.ExecutionEventError
)
