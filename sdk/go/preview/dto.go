package preview

import "github.com/gregale-dev/gregale-go/internal/api"

// Request and response types of preview capabilities, and their helpers
// (ADR-20261023115741). faas re-exports the generally available and beta
// ones.

// End-customer consumers and credentials (ADR-120).
type CreateAPIConsumerRequest = api.CreateAPIConsumerRequest

type APIConsumerResponse = api.APIConsumerResponse

type APIConsumerListResponse = api.APIConsumerListResponse

type CreateConsumerKeyRequest = api.CreateConsumerKeyRequest

type ConsumerKeyResponse = api.ConsumerKeyResponse

type ConsumerKeyListResponse = api.ConsumerKeyListResponse

type APIConsumerUsageBucketResponse = api.APIConsumerUsageBucketResponse

type APIConsumerUsageResponse = api.APIConsumerUsageResponse

type CreateAPIConsumerRateCardRequest = api.CreateAPIConsumerRateCardRequest

type APIConsumerRateCardResponse = api.APIConsumerRateCardResponse

type APIConsumerRateCardListResponse = api.APIConsumerRateCardListResponse

type APIConsumerUsageQuoteBucketResponse = api.APIConsumerUsageQuoteBucketResponse

type APIConsumerUsageQuoteResponse = api.APIConsumerUsageQuoteResponse

type CreateAPIConsumerUsageStatementRequest = api.CreateAPIConsumerUsageStatementRequest

type APIConsumerUsageStatementBucketResponse = api.APIConsumerUsageStatementBucketResponse

type APIConsumerUsageStatementResponse = api.APIConsumerUsageStatementResponse

type APIConsumerUsageStatementListResponse = api.APIConsumerUsageStatementListResponse

type ClaimAPIConsumerUsageStatementRequest = api.ClaimAPIConsumerUsageStatementRequest

type APIConsumerUsageStatementHandoffResponse = api.APIConsumerUsageStatementHandoffResponse

// Disposable agent executions. Source and input are accepted only by
// CreateExecutionRequest and are never returned in execution receipts.
type ExecutionRuntime = api.ExecutionRuntime

type ExecutionProfile = api.ExecutionProfile

type ExecutionNetworkMode = api.ExecutionNetworkMode

type ExecutionNetworkPolicy = api.ExecutionNetworkPolicy

type ExecutionLimitRequest = api.ExecutionLimitRequest

type ExecutionFile = api.ExecutionFile

type ExecutionArtifactInput = api.ExecutionArtifactInput

type ExecutionArtifact = api.ExecutionArtifact

type CreateExecutionArtifactGrantRequest = api.CreateExecutionArtifactGrantRequest

type ExecutionArtifactGrantResponse = api.ExecutionArtifactGrantResponse

type RevokeExecutionArtifactGrantResponse = api.RevokeExecutionArtifactGrantResponse

type CreateExecutionRequest = api.CreateExecutionRequest

type ResolvedExecutionLimits = api.ResolvedExecutionLimits

type ExecutionProfileCapability = api.ExecutionProfileCapability

type ExecutionCapabilityLimits = api.ExecutionCapabilityLimits

type ExecutionCapabilitiesResponse = api.ExecutionCapabilitiesResponse

type ExecutionUsage = api.ExecutionUsage

type ExecutionFailure = api.ExecutionFailure

type ExecutionStatus = api.ExecutionStatus

type ExecutionResponse = api.ExecutionResponse

type ExecutionListResponse = api.ExecutionListResponse

type ExecutionEventType = api.ExecutionEventType

type ExecutionEventData = api.ExecutionEventData

type ExecutionEvent = api.ExecutionEvent

type WatchExecutionOptions = api.WatchExecutionOptions

type ExecutionWatcher = api.ExecutionWatcher

type RunOptions = api.RunOptions

// Async + queues + delayed tasks.
type CreateAppTaskRequest = api.CreateAppTaskRequest

type AppTaskKind = api.AppTaskKind

type AppTaskStatus = api.AppTaskStatus

type AppTaskFailure = api.AppTaskFailure

type AppTaskResponse = api.AppTaskResponse

type AppTaskListResponse = api.AppTaskListResponse

type ExclusiveOperationRequest = api.ExclusiveOperationRequest

type ExclusiveJobOperationRequest = api.ExclusiveJobOperationRequest

type ExclusiveAppTaskOperationRequest = api.ExclusiveAppTaskOperationRequest

type ExclusiveTriggerBindingRequest = api.ExclusiveTriggerBindingRequest

type ExclusiveTriggerBindingRecord = api.ExclusiveTriggerBindingRecord

type ExclusiveOperationAccepted = api.ExclusiveOperationAccepted

type ExclusiveOperationPolicy = api.ExclusiveOperationPolicy

type ExclusiveWorkPolicyRecord = api.ExclusiveWorkPolicyRecord

type ExclusiveWorkPolicyList = api.ExclusiveWorkPolicyList

type ExclusiveOperationRecord = api.ExclusiveOperationRecord

type OperationEffectRecord = api.OperationEffectRecord

type SendAppMessageRequest = api.SendAppMessageRequest

type SendAppMessageResponse = api.SendAppMessageResponse

type DeliverAppEventRequest = api.DeliverAppEventRequest

type DeliverAppEventResponse = api.DeliverAppEventResponse

type CommitEventPublication = api.CommitEventPublication

type CommitSourceResponse = api.CommitSourceResponse

type CommitReceiptResponse = api.CommitReceiptResponse

type CommitOperationResponse = api.CommitOperationResponse

type CommitEventRequest = api.CommitEventRequest

type CommitRouting = api.CommitRouting

type CreateCommitSourceRequest = api.CreateCommitSourceRequest

type CommitBlockedEventResponse = api.CommitBlockedEventResponse

type CommitBlockedEventsResponse = api.CommitBlockedEventsResponse

type CommitPublicationHistoryItem = api.CommitPublicationHistoryItem

type CommitPublicationHistoryResponse = api.CommitPublicationHistoryResponse

type CommitSubscriberWaitResult = api.CommitSubscriberWaitResult

type CommitDeliveryReport = api.CommitDeliveryReport

type CommitDeliveryReportItem = api.CommitDeliveryReportItem

type CommitDeliveryReportChange = api.CommitDeliveryReportChange

// Commit delivery report recovery evidence aliases.
type CommitDeliveryReportSubscriber = api.CommitDeliveryReportSubscriber

type CommitDeliveryReportRecovery = api.CommitDeliveryReportRecovery

// CommitRecoveryPlan describes current read-only recovery eligibility.
type CommitRecoveryPlan = api.CommitRecoveryPlan

type CommitRecoveryPlanItem = api.CommitRecoveryPlanItem

type CommitRecoveryPlanAction = api.CommitRecoveryPlanAction

type CommitRecoveryApplyResult = api.CommitRecoveryApplyResult

type CommitRecoveryApplyItem = api.CommitRecoveryApplyItem

type EventRoutingRecoveryExpectation = api.EventRoutingRecoveryExpectation

type ReplayEventFanoutFailureRequest = api.ReplayEventFanoutFailureRequest

type ReplayEventFanoutFailureResponse = api.ReplayEventFanoutFailureResponse

// CommitRecoveryStatusResult keeps current delivery evidence separate from saved attempts.
type CommitRecoveryStatusResult = api.CommitRecoveryStatusResult

type CommitRecoveryStatusItem = api.CommitRecoveryStatusItem

type CommitRecoveryDiagnostics = api.CommitRecoveryDiagnostics

type CommitRecoveryStatusSelection = api.CommitRecoveryStatusSelection

type CommitRecoveryStatusObservation = api.CommitRecoveryStatusObservation

type CommitRecoveryStatusChange = api.CommitRecoveryStatusChange

// CommitRecoveryMonitoring describes a bounded watch invocation.
type CommitRecoveryMonitoring = api.CommitRecoveryMonitoring

// CommitRecoveryWatchOptions controls retries and optional observation freshness.
type CommitRecoveryWatchOptions = api.CommitRecoveryWatchOptions

type EventBacklogRecipient = api.EventBacklogRecipient

type EventBacklogResponse = api.EventBacklogResponse

type EventBacklogConsumer = api.EventBacklogConsumer

type EventOrderingBlocker = api.EventOrderingBlocker

type EventBacklogOptions = api.EventBacklogOptions

type EventBacklogFilters = api.EventBacklogFilters

type EventRecipientBlockerQuery = api.EventRecipientBlockerQuery

type EventRecipientBlockerResponse = api.EventRecipientBlockerResponse

type CommitRecoveryDiagnosticProgress = api.CommitRecoveryDiagnosticProgress

type EventRecipientBlockerBatchRequest = api.EventRecipientBlockerBatchRequest

type EventRecipientBlockerBatchItem = api.EventRecipientBlockerBatchItem

type EventRecipientBlockerBatchResponse = api.EventRecipientBlockerBatchResponse

type CommitRecoveryDiagnosticsOptions = api.CommitRecoveryDiagnosticsOptions

type CommitRecoveryDiagnosticObservation = api.CommitRecoveryDiagnosticObservation

type CommitRecoveryDiagnosticRouting = api.CommitRecoveryDiagnosticRouting

type CommitRecoveryDiagnosticOrdering = api.CommitRecoveryDiagnosticOrdering

type CommitRecoveryDiagnosticChange = api.CommitRecoveryDiagnosticChange

type CommitRecoveryDiagnosticEstimate = api.CommitRecoveryDiagnosticEstimate

type CommitRecoveryDiagnosticConsumer = api.CommitRecoveryDiagnosticConsumer

type CommitRecoveryDiagnosticWaiting = api.CommitRecoveryDiagnosticWaiting

type CommitRecoveryConsumerStall = api.CommitRecoveryConsumerStall

type CommitRecoveryPersistentBlocker = api.CommitRecoveryPersistentBlocker

type CommitRecoveryBlockerIncident = api.CommitRecoveryBlockerIncident

type CommitRecoveryBlockerTransition = api.CommitRecoveryBlockerTransition

type CommitRecoveryAlertOutbox = api.CommitRecoveryAlertOutbox

type CommitRecoveryAlertAcknowledgement = api.CommitRecoveryAlertAcknowledgement

// EventConsumerDeliveryHealth combines independent delivery health reads.
type EventConsumerDeliveryHealth = api.EventConsumerDeliveryHealth

type EventConsumerPendingSample = api.EventConsumerPendingSample

type EventConsumerHealth = api.EventConsumerHealth

type EventConsumerExecutionHealth = api.EventConsumerExecutionHealth

type EventSubscriptionDeliveryControl = api.EventSubscriptionDeliveryControl

type EventSubscriptionCapacityRequest = api.EventSubscriptionCapacityRequest

type EventSubscriptionCapacityGuard = api.EventSubscriptionCapacityGuard

type EventSubscriptionCapacityCommandReceipt = api.EventSubscriptionCapacityCommandReceipt

type EventCircuitBreakerResponse = api.EventCircuitBreakerResponse

type EventCircuitBreakerPolicy = api.EventCircuitBreakerPolicy

// EventDeliveryHealthWatchOptions configures health alerts and optional checkpointing.
type EventDeliveryHealthWatchOptions = api.EventDeliveryHealthWatchOptions

type EventDeliveryHealthWatchResult = api.EventDeliveryHealthWatchResult

type EventDeliveryHealthCondition = api.EventDeliveryHealthCondition

type EventDeliveryHealthAlert = api.EventDeliveryHealthAlert

type EventConsumerCapacityWaits = api.EventConsumerCapacityWaits

type EventConsumerCapacityPressure = api.EventConsumerCapacityPressure

type EventConsumerCapacityScope = api.EventConsumerCapacityScope

type EventConsumerWaitingReasons = api.EventConsumerWaitingReasons

// EventDeliveryHealthCheckpoint stores historical watch evidence and pending alerts.
type EventDeliveryHealthCheckpoint = api.EventDeliveryHealthCheckpoint

// EventDeliveryHealthAcknowledgement selects an exact pending notification.
type EventDeliveryHealthAcknowledgement = api.EventDeliveryHealthAcknowledgement

// EventDeliveryHealthSLOOptions configures sustained window targets.
type EventDeliveryHealthSLOOptions = api.EventDeliveryHealthSLOOptions

type EventDeliveryHealthSLOObservation = api.EventDeliveryHealthSLOObservation

type CreateNativeIssueTicketRequest = api.CreateNativeIssueTicketRequest

type NativeIssueTicketResult = api.NativeIssueTicketResult

type IssueTrackerLinearConnection = api.IssueTrackerLinearConnection

type UpdateIssueTrackerLinearConnectionRequest = api.UpdateIssueTrackerLinearConnectionRequest

type CreateNativeLinearIssueTicketRequest = api.CreateNativeLinearIssueTicketRequest

type EventExecutionReconciliation = api.EventExecutionReconciliation

type EventExecutionLineageCase = api.EventExecutionLineageCase

type EventExecutionLineageCases = api.EventExecutionLineageCases

type RetryEventExecutionLineageCaseRequest = api.RetryEventExecutionLineageCaseRequest

type EventExecutionLineageRetrySelection = api.EventExecutionLineageRetrySelection

type CreateEventExecutionLineageRetryJobRequest = api.CreateEventExecutionLineageRetryJobRequest

type EventExecutionLineageRetryJobItem = api.EventExecutionLineageRetryJobItem

type EventExecutionLineageRetryJob = api.EventExecutionLineageRetryJob

type EventExecutionLineageRetryJobHealth = api.EventExecutionLineageRetryJobHealth

type EventExecutionLineageRetryJobHealthSample = api.EventExecutionLineageRetryJobHealthSample

type CancelEventExecutionLineageRetryJobRequest = api.CancelEventExecutionLineageRetryJobRequest

type EventExecutionLineageCaseBackfill = api.EventExecutionLineageCaseBackfill

type RequestEventExecutionLineageCaseBackfillRequest = api.RequestEventExecutionLineageCaseBackfillRequest

type ControlEventExecutionLineageCaseBackfillRequest = api.ControlEventExecutionLineageCaseBackfillRequest

type EventExecutionLineageRetryJobSummary = api.EventExecutionLineageRetryJobSummary

type EventExecutionLineageRetryJobs = api.EventExecutionLineageRetryJobs

type EventExecutionLineageRetryJobAttentionOptions = api.EventExecutionLineageRetryJobAttentionOptions

type EventExecutionLineageRetryJobAttentionPage = api.EventExecutionLineageRetryJobAttentionPage

type ControlEventExecutionLineageRetryJobRequest = api.ControlEventExecutionLineageRetryJobRequest

type EventExecutionLineageRetryJobControlReceipt = api.EventExecutionLineageRetryJobControlReceipt

type EventExecutionLineageRetryJobControls = api.EventExecutionLineageRetryJobControls

type EventExecutionLineageRetryJobControlFilters = api.EventExecutionLineageRetryJobControlFilters

type EventSubscriptionCapacityCommands = api.EventSubscriptionCapacityCommands

type EventSubscriptionCapacityCommandsQuery = api.EventSubscriptionCapacityCommandsQuery

type EventSubscriptionCapacityCommandBudget = api.EventSubscriptionCapacityCommandBudget

type EventDeliveryHealthConfirmation = api.EventDeliveryHealthConfirmation

type EventDeliveryRateRiskOptions = api.EventDeliveryRateRiskOptions

type EventDeliveryRateEvidence = api.EventDeliveryRateEvidence

type EventDeliveryDeadlineWindowOptions = api.EventDeliveryDeadlineWindowOptions

type EventDeliveryDeadlineRatioOptions = api.EventDeliveryDeadlineRatioOptions

type EventDeadlineInvestigationOptions = api.EventDeadlineInvestigationOptions

type EventDeadlineAlertReference = api.EventDeadlineAlertReference

type EventDeadlineInvestigation = api.EventDeadlineInvestigation

type EventDeadlineExplanations = api.EventDeadlineExplanations

type EventDeadlineExplanation = api.EventDeadlineExplanation

type EventDeadlineRoutingExplanation = api.EventDeadlineRoutingExplanation

type EventDeadlineOrderingExplanation = api.EventDeadlineOrderingExplanation

// Plan wraps api.Plan — identity-preserving alias so a customer
// can use faas.Plan anywhere an api.Plan is expected.
type Plan = api.Plan

type AlertRollback = api.AlertRollback

type AlertRollbackDeploymentEvidence = api.AlertRollbackDeploymentEvidence

type SetObjectStorageBindingReleasePolicyRequest = api.SetObjectStorageBindingReleasePolicyRequest

type SetQueueBindingReleasePolicyRequest = api.SetQueueBindingReleasePolicyRequest

type CreateQueueBindingCanaryRequest = api.CreateQueueBindingCanaryRequest

// CommitRecoveryStatusSettled reports terminal acknowledged execution coverage.
func CommitRecoveryStatusSettled(out CommitRecoveryStatusResult) bool {
	return api.CommitRecoveryStatusSettled(out)
}

// AcknowledgeCommitRecoveryAlerts removes exact pending alerts without live reads.
func AcknowledgeCommitRecoveryAlerts(status CommitRecoveryStatusResult, acks []CommitRecoveryAlertAcknowledgement) (CommitRecoveryStatusResult, error) {
	return api.AcknowledgeCommitRecoveryAlerts(status, acks)
}

func ValidateEventDeliveryHealthCheckpoint(saved EventDeliveryHealthCheckpoint) error {
	return api.ValidateEventDeliveryHealthCheckpoint(saved)
}

func AcknowledgeEventDeliveryHealthAlerts(saved EventDeliveryHealthCheckpoint, acks []EventDeliveryHealthAcknowledgement) (EventDeliveryHealthCheckpoint, error) {
	return api.AcknowledgeEventDeliveryHealthAlerts(saved, acks)
}

func EvaluateEventConsumerDeliverySLO(h EventConsumerDeliveryHealth, options EventDeliveryHealthSLOOptions) ([]EventDeliveryHealthSLOObservation, error) {
	return api.EvaluateEventConsumerDeliverySLO(h, options)
}

// Stored file and standalone contract wire types.
type ExecutionFileInput = api.ExecutionFileInput
type ExecutionFileResponse = api.ExecutionFileResponse
type CreateExecutionFileRequest = api.CreateExecutionFileRequest
type ExecutionFileLimits = api.ExecutionFileLimits
type ExecutionArtifactStorage = api.ExecutionArtifactStorage
type ExecutionResultValidation = api.ExecutionResultValidation

const (
	ExecutionArtifactInline = api.ExecutionArtifactInline
	ExecutionArtifactStored = api.ExecutionArtifactStored
)

type ExecutionClass = api.ExecutionClass

const (
	ExecutionClassStandard = api.ExecutionClassStandard
	ExecutionClassExtended = api.ExecutionClassExtended
)

type CreateExecutionEnvironmentRequest = api.CreateExecutionEnvironmentRequest
type ExecutionEnvironmentResponse = api.ExecutionEnvironmentResponse

type ExecutionClassCapability = api.ExecutionClassCapability

type ExecutionEnvironmentLimits = api.ExecutionEnvironmentLimits

type ExecutionEnvironmentEvent = api.ExecutionEnvironmentEvent
type ExecutionEnvironmentEventsResponse = api.ExecutionEnvironmentEventsResponse
type ExecutionFinishedWebhookPayload = api.ExecutionFinishedWebhookPayload

type PublishExecutionTaskRequest = api.PublishExecutionTaskRequest
type ExecutionTaskRef = api.ExecutionTaskRef
type ExecutionTaskVersionResponse = api.ExecutionTaskVersionResponse
type ExecutionTaskListResponse = api.ExecutionTaskListResponse
type InvokeExecutionTaskRequest = api.InvokeExecutionTaskRequest

// Customer controls and payload-free invocation history.
type RunAttribution = api.RunAttribution
type RunControlCapabilities = api.RunControlCapabilities
type RunBudgetLimits = api.RunBudgetLimits
type SetRunBudgetRequest = api.SetRunBudgetRequest
type RunBudgetUsage = api.RunBudgetUsage
type RunBudgetAlert = api.RunBudgetAlert
type RunBudgetPolicy = api.RunBudgetPolicy
type SetRunAttributionBindingRequest = api.SetRunAttributionBindingRequest
type RunAttributionBinding = api.RunAttributionBinding
type RunHistoryFilter = api.RunHistoryFilter
type RunHistoryEntry = api.RunHistoryEntry
type RunHistoryResponse = api.RunHistoryResponse

type RunBudgetRefusal = api.RunBudgetRefusal

type CreateManagedExecutionWorkflowRequest = api.CreateManagedExecutionWorkflowRequest
type CreateManagedExecutionWorkflowStep = api.CreateManagedExecutionWorkflowStep
type ManagedExecutionWorkflowArtifactInput = api.ManagedExecutionWorkflowArtifactInput
type ManagedExecutionWorkflowStatus = api.ManagedExecutionWorkflowStatus
type ManagedExecutionWorkflowResponse = api.ManagedExecutionWorkflowResponse
type ExecutionPipelineOptions = api.ExecutionPipelineOptions
type ExecutionRetryPolicy = api.ExecutionRetryPolicy
type ExecutionTaskBatch = api.ExecutionTaskBatch
type ExecutionTaskBatchItem = api.ExecutionTaskBatchItem
type ExecutionPipelineResponse = api.ExecutionPipelineResponse
type ExecutionPipelineStepResponse = api.ExecutionPipelineStepResponse
type ExecutionPipelineAttemptResponse = api.ExecutionPipelineAttemptResponse

type ExecutionPipelineCompletion = api.ExecutionPipelineCompletion
type ExecutionPipelineCompletedStep = api.ExecutionPipelineCompletedStep
type ExecutionPipelineCompletedAttempt = api.ExecutionPipelineCompletedAttempt
type ExecutionPipelineArtifactRef = api.ExecutionPipelineArtifactRef
type ExecutionPipelineFinishedWebhookPayload = api.ExecutionPipelineFinishedWebhookPayload
type ExecutionPipelineReceipt = api.ExecutionPipelineReceipt

type ExecutionPipelineProgress = api.ExecutionPipelineProgress

type ExecutionPipelineStepProgress = api.ExecutionPipelineStepProgress

type ExecutionPipelineEvent = api.ExecutionPipelineEvent

type ExecutionPipelineEventsResponse = api.ExecutionPipelineEventsResponse

type RerunExecutionPipelineRequest = api.RerunExecutionPipelineRequest

type ExecutionPipelineRerun = api.ExecutionPipelineRerun

type ExecutionPipelineReusedStep = api.ExecutionPipelineReusedStep
