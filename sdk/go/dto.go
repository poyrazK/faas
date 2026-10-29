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
	RouteCustomerUsageOptions  = api.RouteCustomerUsageOptions
	RouteCustomerUsageResponse = api.RouteCustomerUsageResponse
	RouteCustomerUsage         = api.RouteCustomerUsage
	RouteCustomerObservation   = api.RouteCustomerObservation
	// App lifecycle.
	CreateAppRequest          = api.CreateAppRequest
	UpdateAppRequest          = api.UpdateAppRequest
	RenameAppRequest          = api.RenameAppRequest
	AppResponse               = api.AppResponse
	AppEffectiveLimits        = api.AppEffectiveLimits
	AppConfiguredResources    = api.AppConfiguredResources
	AppServiceBinding         = api.AppServiceBinding
	ServiceReliabilityPolicy  = api.ServiceReliabilityPolicy
	ServiceBindingTransport   = api.ServiceBindingTransport
	ServiceCallScope          = api.ServiceCallScope
	ServiceCallerScopes       = api.ServiceCallerScopes
	ServiceBindingPolicy      = api.ServiceBindingPolicy
	PreviewServiceCallsPolicy = api.PreviewServiceCallsPolicy
	DeclaredRoute             = api.DeclaredRoute
	RetryPolicyDTO            = api.RetryPolicyDTO

	ResourceProfile     = api.ResourceProfile
	ResourceProfileSpec = api.ResourceProfileSpec

	// Deployments.
	CreateDeploymentRequest          = api.CreateDeploymentRequest
	DeploymentResponse               = api.DeploymentResponse
	DeploymentListResponse           = api.DeploymentListResponse
	LatestDeploymentsByAppResponse   = api.LatestDeploymentsByAppResponse
	PreviewStatusResponse            = api.PreviewStatusResponse
	PreviewResourceResponse          = api.PreviewResourceResponse
	PreviewProductionChangesResponse = api.PreviewProductionChangesResponse
	PreviewArtifactResponse          = api.PreviewArtifactResponse
	PreviewResourceLinksResponse     = api.PreviewResourceLinksResponse

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
	DNSRecordInstruction      = api.DNSRecordInstruction
	CreateCustomDomainRequest = api.CreateCustomDomainRequest

	// Crons.
	CronResponse                    = api.CronResponse
	CreateCronRequest               = api.CreateCronRequest
	UpdateCronRequest               = api.UpdateCronRequest
	SchedulePolicy                  = api.SchedulePolicy
	FailureRule                     = api.FailureRule
	FailureRules                    = api.FailureRules
	WorkDecision                    = api.WorkDecision
	ScheduleOccurrenceResponse      = api.ScheduleOccurrenceResponse
	ListScheduleOccurrencesResponse = api.ListScheduleOccurrencesResponse
	CronRun                         = api.CronRun
	CronRunOutcome                  = api.CronRunOutcome
	ListCronRunsResponse            = api.ListCronRunsResponse
	FireCronResponse                = api.FireCronResponse
	FireCronRequestResponse         = api.FireCronRequestResponse

	// Jobs (issue #1184 Workstream A).
	CreateJobRequest            = api.CreateJobRequest
	UpdateJobRequest            = api.UpdateJobRequest
	CreateJobRunRequest         = api.CreateJobRunRequest
	JobResponse                 = api.JobResponse
	JobRunResponse              = api.JobRunResponse
	JobTaskResponse             = api.JobTaskResponse
	JobTaskLogResponse          = api.JobTaskLogResponse
	JobTaskAttemptResponse      = api.JobTaskAttemptResponse
	ListJobTaskAttemptsResponse = api.ListJobTaskAttemptsResponse
	JobArtifactDownloadResponse = api.JobArtifactDownloadResponse
	JobTaskRetryResponse        = api.JobTaskRetryResponse
	ListJobsResponse            = api.ListJobsResponse
	ListJobRunsResponse         = api.ListJobRunsResponse
	ListJobTasksResponse        = api.ListJobTasksResponse
	JobRunCancelledResponse     = api.JobRunCancelledResponse
	JobDeletedResponse          = api.JobDeletedResponse

	// Instances.
	InstanceResponse = api.InstanceResponse

	// Usage.
	UsageResponse                 = api.UsageResponse
	UsageSummaryResponse          = api.UsageSummaryResponse
	ExecutionUsageSummaryResponse = api.ExecutionUsageSummaryResponse
	AccountUsageResponse          = api.AccountUsageResponse
	ObjectStorageUsageResponse    = api.ObjectStorageUsageResponse
	ManagedPostgresUsageResponse  = api.ManagedPostgresUsageResponse

	ExecutionWorkflowResponse     = api.ExecutionWorkflowResponse
	ExecutionWorkflowStatusCounts = api.ExecutionWorkflowStatusCounts
	ExecutionWorkflowUsage        = api.ExecutionWorkflowUsage
	ExecutionEventParseError      = api.ExecutionEventParseError

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

	AsyncInvokeResponse                = api.AsyncInvokeResponse
	InvokeResponse                     = api.InvokeResponse
	InvokeRequest                      = api.InvokeRequest
	ManagedOperationResult             = api.ManagedOperationResult
	ManagedOperationEffect             = api.ManagedOperationEffect
	OperationEffectPayload             = api.OperationEffectPayload
	InvocationDestinations             = api.InvocationDestinations
	QueueSendRequest                   = api.QueueSendRequest
	QueueSendResponse                  = api.QueueSendResponse
	QueueReceiveResponse               = api.QueueReceiveResponse
	CreateAppWebhookRequest            = api.CreateAppWebhookRequest
	UpdateAppWebhookRequest            = api.UpdateAppWebhookRequest
	AppWebhookResponse                 = api.AppWebhookResponse
	RotateAppWebhookSecretRequest      = api.RotateAppWebhookSecretRequest
	RotateAppWebhookSecretResponse     = api.RotateAppWebhookSecretResponse
	AppWebhookDeliveryResponse         = api.AppWebhookDeliveryResponse
	AppWebhookDeliveryListResponse     = api.AppWebhookDeliveryListResponse
	AppWebhookRetryDeliveryResponse    = api.AppWebhookRetryDeliveryResponse
	ListAppWebhookDeliveriesOptions    = api.ListAppWebhookDeliveriesOptions
	CreateAccountReleaseWebhookRequest = api.CreateAccountReleaseWebhookRequest
	UpdateAccountReleaseWebhookRequest = api.UpdateAccountReleaseWebhookRequest
	AccountReleaseWebhookResponse      = api.AccountReleaseWebhookResponse
	DelayedTaskRequest                 = api.DelayedTaskRequest
	DelayedTaskResponse                = api.DelayedTaskResponse
	ListDelayedTasksResponse           = api.ListDelayedTasksResponse

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
	BuildManifest        = api.BuildManifest
	BuildDone            = api.BuildDone
	BuildFramework       = api.BuildFramework
	AppManifest          = api.AppManifest
	AfterRestoreHook     = api.AfterRestoreHook
	BeforeCheckpointHook = api.BeforeCheckpointHook
	ServiceReplicas      = api.ServiceReplicas

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
	ServiceBindingTransportHTTP  = api.ServiceBindingTransportHTTP
	ServiceBindingTransportHTTPS = api.ServiceBindingTransportHTTPS
	ServiceBindingPolicyAccount  = api.ServiceBindingPolicyAccount
	ServiceBindingPolicyDeclared = api.ServiceBindingPolicyDeclared
	ServiceBindingPolicyProject  = api.ServiceBindingPolicyProject
	PreviewServiceCallsAllow     = api.PreviewServiceCallsAllow
	PreviewServiceCallsDeny      = api.PreviewServiceCallsDeny
)

const (
	CronRunSuccess    = api.CronRunSuccess
	CronRunFailed     = api.CronRunFailed
	CronRunTimeout    = api.CronRunTimeout
	CronRunDeadLetter = api.CronRunDeadLetter
	CronRunCancelled  = api.CronRunCancelled
	CronRunUncertain  = api.CronRunUncertain
	CronRunRunning    = api.CronRunRunning
)

const (
	AppTaskKindManual  = api.AppTaskKindManual
	AppTaskKindRelease = api.AppTaskKindRelease
	AppTaskKindCron    = api.AppTaskKindCron

	AppTaskStatusQueued    = api.AppTaskStatusQueued
	AppTaskStatusRestoring = api.AppTaskStatusRestoring
	AppTaskStatusRunning   = api.AppTaskStatusRunning
	AppTaskStatusSucceeded = api.AppTaskStatusSucceeded
	AppTaskStatusFailed    = api.AppTaskStatusFailed
	AppTaskStatusTimedOut  = api.AppTaskStatusTimedOut
	AppTaskStatusCancelled = api.AppTaskStatusCancelled

	ResourceProfileMicro     = api.ResourceProfileMicro
	ResourceProfileSmall     = api.ResourceProfileSmall
	ResourceProfileMedium    = api.ResourceProfileMedium
	ResourceProfileLarge     = api.ResourceProfileLarge
	ResourceProfileXLarge    = api.ResourceProfileXLarge
	ConsumerAuthModeOptional = api.ConsumerAuthModeOptional
	ConsumerAuthModeRequired = api.ConsumerAuthModeRequired

	ExecutionRuntimeNode22       = api.ExecutionRuntimeNode22
	ExecutionRuntimeNode24       = api.ExecutionRuntimeNode24
	ExecutionRuntimePython312    = api.ExecutionRuntimePython312
	ExecutionRuntimePython313    = api.ExecutionRuntimePython313
	ExecutionProfileStandard     = api.ExecutionProfileStandard
	ExecutionProfilePythonDataV1 = api.ExecutionProfilePythonDataV1
	ExecutionNetworkNone         = api.ExecutionNetworkNone

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

// Issue reporting and lifecycle.
type (
	IssueEvent                          = api.IssueEvent
	IssueFrame                          = api.IssueFrame
	IssueCause                          = api.IssueCause
	IssueGroupingFrame                  = api.IssueGroupingFrame
	IssueGroupingExplanation            = api.IssueGroupingExplanation
	Issue                               = api.Issue
	IssueOccurrence                     = api.IssueOccurrence
	IssueRelease                        = api.IssueRelease
	IssueActivity                       = api.IssueActivity
	IssueImpact                         = api.IssueImpact
	IssueImpactAlertPolicy              = api.IssueImpactAlertPolicy
	UpdateIssueImpactAlertPolicyRequest = api.UpdateIssueImpactAlertPolicyRequest
	IssueOwnershipRule                  = api.IssueOwnershipRule
	IssueOwnershipRules                 = api.IssueOwnershipRules
	IssueDetail                         = api.IssueDetail
	ListIssuesResponse                  = api.ListIssuesResponse
	IssueEventResponse                  = api.IssueEventResponse
	IssueActionRequest                  = api.IssueActionRequest
	CreateIssueIngestTokenRequest       = api.CreateIssueIngestTokenRequest
	IssueIngestToken                    = api.IssueIngestToken
)

// Queue bindings preserve their immutable project environment identity.
type (
	QueueBindingResponse       = api.QueueBindingResponse
	QueueBindingStatusResponse = api.QueueBindingStatusResponse
	CreateQueueBindingRequest  = api.CreateQueueBindingRequest
	UpdateQueueBindingRequest  = api.UpdateQueueBindingRequest
	InvokeWork                 = api.InvokeWork
)

type IssueSplitMatcher = api.IssueSplitMatcher
type IssueSplitPreview = api.IssueSplitPreview

type IssueSplitRule = api.IssueSplitRule
type IssueSplitRuleUndoRequest = api.IssueSplitRuleUndoRequest
type IssueSplitRuleUpdate = api.IssueSplitRuleUpdate

// EventPendingWaitingReasonCountsComplete verifies an exact known-reason partition.
func EventPendingWaitingReasonCountsComplete(counts map[string]int64, pending int64) bool {
	return api.EventPendingWaitingReasonCountsComplete(counts, pending)
}

// Deployment-bound source-map artifact publication and verified occurrence metadata.
type IssueSourceMapArtifact = api.IssueSourceMapArtifact
type IssueSourceMapManifest = api.IssueSourceMapManifest
type PublishIssueSourceMapsRequest = api.PublishIssueSourceMapsRequest
type IssueSourceMapsMetadata = api.IssueSourceMapsMetadata
type IssueSymbolicationResult = api.IssueSymbolicationResult

// IssueBreadcrumb is bounded reported operational context.
type IssueBreadcrumb = api.IssueBreadcrumb

type IssueReleaseHealth = api.IssueReleaseHealth
type IssueReleaseComparison = api.IssueReleaseComparison
type BulkIssueActionRequest = api.BulkIssueActionRequest
type BulkIssueActionResponse = api.BulkIssueActionResponse

type BulkIssueLifecycleAction = api.BulkIssueLifecycleAction

type IssueSavedViewFilters = api.IssueSavedViewFilters
type IssueSavedView = api.IssueSavedView
type PutIssueSavedViewRequest = api.PutIssueSavedViewRequest
type ListIssueSavedViewsResponse = api.ListIssueSavedViewsResponse

type IssueTeam = api.IssueTeam
type PutIssueTeamRequest = api.PutIssueTeamRequest
type ListIssueTeamsResponse = api.ListIssueTeamsResponse

type PutIssueTrackerLinkRequest = api.PutIssueTrackerLinkRequest
type IssueTrackerLink = api.IssueTrackerLink
type ListIssueTrackerLinksResponse = api.ListIssueTrackerLinksResponse

type IssueTrackerProviderStatus = api.IssueTrackerProviderStatus
type NativeIssueTrackerStatus = api.NativeIssueTrackerStatus
type UpdateNativeIssueTrackerStatusRequest = api.UpdateNativeIssueTrackerStatusRequest

type IssueTrackerAutoSync = api.IssueTrackerAutoSync
type UpdateIssueTrackerAutoSyncRequest = api.UpdateIssueTrackerAutoSyncRequest

type NativeLinearIssueTrackerStatus = api.NativeLinearIssueTrackerStatus
type UpdateNativeLinearIssueTrackerStatusRequest = api.UpdateNativeLinearIssueTrackerStatusRequest

// Subscription token-bucket controls and retained command evidence.
type (
	EventSubscriptionRateRequest        = api.EventSubscriptionRateRequest
	EventSubscriptionRateLimit          = api.EventSubscriptionRateLimit
	EventSubscriptionRateCommandReceipt = api.EventSubscriptionRateCommandReceipt
	EventSubscriptionRateCommands       = api.EventSubscriptionRateCommands
)

// Read-only delivery pacing previews.
type (
	EventSubscriptionRatePreviewRequest = api.EventSubscriptionRatePreviewRequest
	EventSubscriptionRatePreview        = api.EventSubscriptionRatePreview
	EventSubscriptionRateDeadlineRisk   = api.EventSubscriptionRateDeadlineRisk
)

// Opt-in Go SDK consumer pacing and deadline-risk watch diagnostics.

// Complete retained pending-recipient observations.
type EventSubscriptionPendingSummary = api.EventSubscriptionPendingSummary

// Read-only effective-deadline triage.
type (
	EventSubscriptionDeadlineQuery     = api.EventSubscriptionDeadlineQuery
	EventSubscriptionDeadlineRecipient = api.EventSubscriptionDeadlineRecipient
	EventSubscriptionDeadlinePage      = api.EventSubscriptionDeadlinePage
)

// Full, disjoint deadline-window counts and opt-in sustained count alerts.
type (
	EventSubscriptionDeadlineWindow = api.EventSubscriptionDeadlineWindow
)

// EventSubscriptionDeadlineWindowCountsComplete checks complete known cohort partitions.
func EventSubscriptionDeadlineWindowCountsComplete(v EventSubscriptionDeadlineWindow) bool {
	return api.EventSubscriptionDeadlineWindowCountsComplete(v)
}

// EventDeliveryDeadlineRatioBasisPointsMax represents 100% in deadline ratio options.
const EventDeliveryDeadlineRatioBasisPointsMax = api.EventDeliveryDeadlineRatioBasisPointsMax

// Read-only alert-to-current-triage correlation helpers.

// Lifecycle alert policy and draft preview types.
type (
	IssueLifecycleAlertPolicy              = api.IssueLifecycleAlertPolicy
	UpdateIssueLifecycleAlertPolicyRequest = api.UpdateIssueLifecycleAlertPolicyRequest
	PreviewIssueLifecycleAlertRequest      = api.PreviewIssueLifecycleAlertRequest
	IssueLifecycleAlertPreview             = api.IssueLifecycleAlertPreview
)

type IssueCompoundAlertRule = api.IssueCompoundAlertRule
type IssueCompoundAlertPolicy = api.IssueCompoundAlertPolicy
type UpdateIssueCompoundAlertPolicyRequest = api.UpdateIssueCompoundAlertPolicyRequest
type PreviewIssueCompoundAlertRequest = api.PreviewIssueCompoundAlertRequest
type IssueCompoundAlertPreview = api.IssueCompoundAlertPreview
