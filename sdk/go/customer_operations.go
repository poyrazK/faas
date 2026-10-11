// adr: 521
package faas

import "github.com/poyrazK/faas/sdk/go/internal/api"

// Operations keep business state, native execution and completion delivery
// separate. The embedded Client methods use these public wire types.
type (
	OperationJobRuntimeProof                 = api.OperationJobRuntimeProof
	OperationArtifactUploadResponse          = api.OperationArtifactUploadResponse
	OperationArtifactUploadRequest           = api.OperationArtifactUploadRequest
	OperationJobControlResponse              = api.OperationJobControlResponse
	OperationJobReportRequest                = api.OperationJobReportRequest
	OperationRecoveryDecision                = api.OperationRecoveryDecision
	OperationRecoveryInspection              = api.OperationRecoveryInspection
	OperationRecoveryStep                    = api.OperationRecoveryStep
	OperationRecoveryArtifact                = api.OperationRecoveryArtifact
	OperationRecoveryPreviewRequest          = api.OperationRecoveryPreviewRequest
	OperationRecoveryPreview                 = api.OperationRecoveryPreview
	OperationWorkflowControlResponse         = api.OperationWorkflowControlResponse
	OperationWorkflowRuntimeProof            = api.OperationWorkflowRuntimeProof
	OperationWorkflowArtifactResponse        = api.OperationWorkflowArtifactResponse
	OperationDeliveryInspection              = api.OperationDeliveryInspection
	OperationDeliveryAttempt                 = api.OperationDeliveryAttempt
	OperationDeliveryAttemptsResponse        = api.OperationDeliveryAttemptsResponse
	OperationDeliveryRetryRequest            = api.OperationDeliveryRetryRequest
	OperationDeliveryRetryResponse           = api.OperationDeliveryRetryResponse
	OperationState                           = api.OperationState
	OperationDefinitionSpec                  = api.OperationDefinitionSpec
	OperationDefinitionResponse              = api.OperationDefinitionResponse
	OperationDefinitionsResponse             = api.OperationDefinitionsResponse
	OperationDefinitionSummary               = api.OperationDefinitionSummary
	OperationDoctorResponse                  = api.OperationDoctorResponse
	OperationDoctorCheck                     = api.OperationDoctorCheck
	OperationTenantIdentity                  = api.OperationTenantIdentity
	OperationSubmissionScope                 = api.OperationSubmissionScope
	OperationSubmissionLookupRequest         = api.OperationSubmissionLookupRequest
	OperationSubmissionLookupResponse        = api.OperationSubmissionLookupResponse
	OperationProgress                        = api.OperationProgress
	OperationReportRequest                   = api.OperationReportRequest
	OperationExecutionControlResponse        = api.OperationExecutionControlResponse
	OperationResultArtifact                  = api.OperationResultArtifact
	OperationArtifactRequest                 = api.OperationArtifactRequest
	OperationDeliveryResponse                = api.OperationDeliveryResponse
	OperationResponse                        = api.OperationResponse
	OperationSummary                         = api.OperationSummary
	OperationDeliverySummary                 = api.OperationDeliverySummary
	OperationListOptions                     = api.OperationListOptions
	OperationListResponse                    = api.OperationListResponse
	OperationExecution                       = api.OperationExecution
	OperationExecutionsResponse              = api.OperationExecutionsResponse
	OperationAcceptedResponse                = api.OperationAcceptedResponse
	OperationEvent                           = api.OperationEvent
	OperationEventsResponse                  = api.OperationEventsResponse
	OperationRecoveryRequest                 = api.OperationRecoveryRequest
	OperationStartRequest                    = api.OperationStartRequest
	OperationCancellationRequest             = api.OperationCancellationRequest
	OperationRuntimeProof                    = api.OperationRuntimeProof
	OperationArtifactTruncatedError          = api.OperationArtifactTruncatedError
	OperationMilestone                       = api.OperationMilestone
	OperationMilestoneRequest                = api.OperationMilestoneRequest
	OperationMilestoneValidationRequest      = api.OperationMilestoneValidationRequest
	OperationMilestoneValidationResponse     = api.OperationMilestoneValidationResponse
	OperationMilestonesResponse              = api.OperationMilestonesResponse
	OperationMilestoneListOptions            = api.OperationMilestoneListOptions
	OperationWorkflowBlockerEscalationPolicy = api.OperationWorkflowBlockerEscalationPolicy
	OperationWorkflowBlockerEscalation       = api.OperationWorkflowBlockerEscalation
	OperationWorkflowSpec                    = api.OperationWorkflowSpec
	OperationWorkflowTransition              = api.OperationWorkflowTransition
	OperationWorkflowStateReport             = api.OperationWorkflowStateReport
	OperationWorkflowStateValidationRequest  = api.OperationWorkflowStateValidationRequest
	OperationWorkflowStateValidationResponse = api.OperationWorkflowStateValidationResponse
	OperationWorkflowStateReportResponse     = api.OperationWorkflowStateReportResponse
	OperationWorkflowEvidenceMilestone       = api.OperationWorkflowEvidenceMilestone
	OperationWorkflowState                   = api.OperationWorkflowState
	OperationWorkflowStateHistoryEntry       = api.OperationWorkflowStateHistoryEntry
	OperationSubjectSpec                     = api.OperationSubjectSpec
	OperationSubject                         = api.OperationSubject
)

type OperationWorkflowRetainedMilestoneFreshnessRule = api.OperationWorkflowRetainedMilestoneFreshnessRule
type OperationWorkflowRetainedMilestoneFreshness = api.OperationWorkflowRetainedMilestoneFreshness
type OperationWorkflowRetainedMilestoneInvalidationRule = api.OperationWorkflowRetainedMilestoneInvalidationRule
type OperationWorkflowRetainedMilestoneInvalidation = api.OperationWorkflowRetainedMilestoneInvalidation

type (
	OperationWorkflowDependency                     = api.OperationWorkflowDependency
	OperationWorkflowReadinessSnapshot              = api.OperationWorkflowReadinessSnapshot
	OperationWorkflowReadinessSnapshotEntry         = api.OperationWorkflowReadinessSnapshotEntry
	OperationWorkflowReadinessComparisonRequest     = api.OperationWorkflowReadinessComparisonRequest
	OperationWorkflowReadinessSubscriptionRequest   = api.OperationWorkflowReadinessSubscriptionRequest
	OperationWorkflowReadinessSubscription          = api.OperationWorkflowReadinessSubscription
	OperationWorkflowReadinessSubscriptionsResponse = api.OperationWorkflowReadinessSubscriptionsResponse
	OperationWorkflowReadinessNotification          = api.OperationWorkflowReadinessNotification
	OperationWorkflowReadinessInboxResponse         = api.OperationWorkflowReadinessInboxResponse
	OperationWorkflowReadinessEvidence              = api.OperationWorkflowReadinessEvidence
	OperationWorkflowReadinessEvidenceChange        = api.OperationWorkflowReadinessEvidenceChange
	OperationWorkflowReadinessChange                = api.OperationWorkflowReadinessChange
	OperationWorkflowReadinessComparisonResponse    = api.OperationWorkflowReadinessComparisonResponse
	OperationWorkflowReadinessQueueOptions          = api.OperationWorkflowReadinessQueueOptions
	OperationWorkflowReadinessQueueEntry            = api.OperationWorkflowReadinessQueueEntry
	OperationWorkflowReadinessQueueResponse         = api.OperationWorkflowReadinessQueueResponse
	OperationWorkflowReadinessRequest               = api.OperationWorkflowReadinessRequest
	OperationWorkflowRetainedMilestoneRequirement   = api.OperationWorkflowRetainedMilestoneRequirement
	OperationWorkflowRetainedMilestone              = api.OperationWorkflowRetainedMilestone
	OperationWorkflowTransitionReadiness            = api.OperationWorkflowTransitionReadiness
	OperationWorkflowReadinessResponse              = api.OperationWorkflowReadinessResponse
	OperationWorkflowReadinessOverview              = api.OperationWorkflowReadinessOverview
	OperationWorkflowDependencyTrace                = api.OperationWorkflowDependencyTrace
	OperationWorkflowDependencyFinding              = api.OperationWorkflowDependencyFinding
	OperationWorkflowDependencyImpact               = api.OperationWorkflowDependencyImpact
	OperationWorkflowDependentInstance              = api.OperationWorkflowDependentInstance
	OperationWorkflowRelatedInstance                = api.OperationWorkflowRelatedInstance
	OperationWorkflowOutcomeEntry                   = api.OperationWorkflowOutcomeEntry
	OperationWorkflowOutcomesResponse               = api.OperationWorkflowOutcomesResponse
	OperationWorkflowOutcomeOptions                 = api.OperationWorkflowOutcomeOptions
	OperationWorkflowOutcomeGroup                   = api.OperationWorkflowOutcomeGroup
	OperationWorkflowOutcomeSummary                 = api.OperationWorkflowOutcomeSummary
	OperationWorkflowOutcomeSummaryOptions          = api.OperationWorkflowOutcomeSummaryOptions
	OperationWorkflowAttentionEntry                 = api.OperationWorkflowAttentionEntry
	OperationWorkflowAttentionResponse              = api.OperationWorkflowAttentionResponse
	OperationWorkflowAttentionOptions               = api.OperationWorkflowAttentionOptions
	OperationWorkflowAttentionStats                 = api.OperationWorkflowAttentionStats
	OperationWorkflowAttentionGroup                 = api.OperationWorkflowAttentionGroup
	OperationWorkflowAttentionSummary               = api.OperationWorkflowAttentionSummary
	OperationWorkflowAttentionSummaryOptions        = api.OperationWorkflowAttentionSummaryOptions
	OperationWorkflowBlockerResolution              = api.OperationWorkflowBlockerResolution
	OperationWorkflowBlocker                        = api.OperationWorkflowBlocker
	OperationWorkflowDecision                       = api.OperationWorkflowDecision
	OperationWorkflowInstanceSnapshot               = api.OperationWorkflowInstanceSnapshot
	OperationWorkflowInstanceTransition             = api.OperationWorkflowInstanceTransition
	OperationWorkflowInstanceStep                   = api.OperationWorkflowInstanceStep
	OperationWorkflowInstanceMilestoneRef           = api.OperationWorkflowInstanceMilestoneRef
)

const (
	OperationTransactionPostgres      = api.OperationTransactionPostgres
	OperationReceiptVersionHeader     = api.OperationReceiptVersionHeader
	OperationReceiptBindingHeader     = api.OperationReceiptBindingHeader
	OperationExecutionKindHeader      = api.OperationExecutionKindHeader
	OperationWorkflowRunHeader        = api.OperationWorkflowRunHeader
	OperationWorkflowStepHeader       = api.OperationWorkflowStepHeader
	OperationGenerationHeader         = api.OperationGenerationHeader
	OperationWorkflowCapabilityHeader = api.OperationWorkflowCapabilityHeader
	OperationAccepted                 = api.OperationAccepted
	OperationRunning                  = api.OperationRunning
	OperationSucceeded                = api.OperationSucceeded
	OperationFailed                   = api.OperationFailed
	OperationCancelled                = api.OperationCancelled
	OperationRequiresReconciliation   = api.OperationRequiresReconciliation
	OperationRecoveryReconcile        = api.OperationRecoveryReconcile
	OperationRecoverySafeRetry        = api.OperationRecoverySafeRetry
	OperationOwnerPlatformTenant      = api.OperationOwnerPlatformTenant
	OperationIDHeader                 = api.OperationIDHeader
	OperationAttemptHeader            = api.OperationAttemptHeader
	OperationCapabilityHeader         = api.OperationCapabilityHeader
)

type OperationWorkflowActionPreviewRequest = api.OperationWorkflowActionPreviewRequest
type OperationWorkflowActionPreviewResponse = api.OperationWorkflowActionPreviewResponse

type OperationJobArtifactResponse = api.OperationJobArtifactResponse

// OperationWorkflowResolutionVerification is a retained evidence finding.
type OperationWorkflowResolutionVerification = api.OperationWorkflowResolutionVerification

// Retained workflow duration observations.
type OperationWorkflowBottlenecks = api.OperationWorkflowBottlenecks
type OperationWorkflowStateDuration = api.OperationWorkflowStateDuration
type OperationWorkflowBlockerDuration = api.OperationWorkflowBlockerDuration
type OperationWorkflowVerificationDuration = api.OperationWorkflowVerificationDuration

type OperationWorkflowPerformanceOptions = api.OperationWorkflowPerformanceOptions
type OperationWorkflowPerformanceSummary = api.OperationWorkflowPerformanceSummary
type OperationWorkflowDurationDistribution = api.OperationWorkflowDurationDistribution
type OperationWorkflowPerformanceCoverageReason = api.OperationWorkflowPerformanceCoverageReason
type OperationWorkflowPerformanceCohort = api.OperationWorkflowPerformanceCohort
type OperationWorkflowStatePerformance = api.OperationWorkflowStatePerformance
type OperationWorkflowBlockerPerformance = api.OperationWorkflowBlockerPerformance
type OperationWorkflowVerificationPerformance = api.OperationWorkflowVerificationPerformance

type OperationWorkflowPerformanceGroup = api.OperationWorkflowPerformanceGroup
type OperationWorkflowPerformanceInstanceOptions = api.OperationWorkflowPerformanceInstanceOptions
type OperationWorkflowPerformanceInstance = api.OperationWorkflowPerformanceInstance
type OperationWorkflowPerformanceInstancesResponse = api.OperationWorkflowPerformanceInstancesResponse

type OperationWorkflowStateSLA = api.OperationWorkflowStateSLA

type OperationWorkflowPrerequisiteRecovery = api.OperationWorkflowPrerequisiteRecovery

type OperationWorkflowRetainedMilestoneGroup = api.OperationWorkflowRetainedMilestoneGroup
type OperationWorkflowRetainedMilestoneGroupResult = api.OperationWorkflowRetainedMilestoneGroupResult

type OperationWorkflowRetainedMilestonePath = api.OperationWorkflowRetainedMilestonePath
type OperationWorkflowRetainedMilestonePathResult = api.OperationWorkflowRetainedMilestonePathResult

type OperationWorkflowPathSelection = api.OperationWorkflowPathSelection

type OperationWorkflowPathDecisionResult = api.OperationWorkflowPathDecisionResult

type OperationWorkflowConditionComparison = api.OperationWorkflowConditionComparison
type OperationWorkflowConditionChange = api.OperationWorkflowConditionChange
type OperationWorkflowConditionEvidence = api.OperationWorkflowConditionEvidence
type OperationWorkflowRevisionComparison = api.OperationWorkflowRevisionComparison
type OperationWorkflowRevisionChange = api.OperationWorkflowRevisionChange
type OperationWorkflowRevisionEvidence = api.OperationWorkflowRevisionEvidence

type OperationWorkflowOutcomeLink = api.OperationWorkflowOutcomeLink

type OperationWorkflowConditionOutcomeSummary = api.OperationWorkflowConditionOutcomeSummary
type OperationWorkflowConditionOutcomeGroup = api.OperationWorkflowConditionOutcomeGroup
type OperationWorkflowDecisionOutcomeSummary = api.OperationWorkflowDecisionOutcomeSummary
type OperationWorkflowDecisionOutcomeGroup = api.OperationWorkflowDecisionOutcomeGroup

type OperationWorkflowDecisionOutcomeEvidence = api.OperationWorkflowDecisionOutcomeEvidence

type OperationWorkflowRuleComparisonSummary = api.OperationWorkflowRuleComparisonSummary
type OperationWorkflowRuleComparison = api.OperationWorkflowRuleComparison
type OperationWorkflowRuleVersionStatistics = api.OperationWorkflowRuleVersionStatistics
type OperationWorkflowRuleVersionOutcome = api.OperationWorkflowRuleVersionOutcome

type OperationWorkflowRuleVersionCohort = api.OperationWorkflowRuleVersionCohort
type OperationWorkflowRuleVersionOverlap = api.OperationWorkflowRuleVersionOverlap

type OperationWorkflowActiveConditionSummary = api.OperationWorkflowActiveConditionSummary
type OperationWorkflowActiveConditionGroup = api.OperationWorkflowActiveConditionGroup

type OperationWorkflowConditionFreshness = api.OperationWorkflowConditionFreshness

type OperationWorkflowConditionRefreshOwnerGroup = api.OperationWorkflowConditionRefreshOwnerGroup

type OperationWorkflowConditionRefreshPriority = api.OperationWorkflowConditionRefreshPriority

type OperationWorkflowConditionRefreshProgress = api.OperationWorkflowConditionRefreshProgress
type OperationWorkflowConditionRefreshProgressCount = api.OperationWorkflowConditionRefreshProgressCount
type OperationWorkflowConditionRefreshProgressInstance = api.OperationWorkflowConditionRefreshProgressInstance
type OperationWorkflowConditionRefreshProgressChange = api.OperationWorkflowConditionRefreshProgressChange

type OperationWorkflowConditionCoverage = api.OperationWorkflowConditionCoverage
type OperationWorkflowConditionCoverageInstance = api.OperationWorkflowConditionCoverageInstance
type OperationWorkflowConditionCoverageEntry = api.OperationWorkflowConditionCoverageEntry

type OperationWorkflowMissingConditionGroup = api.OperationWorkflowMissingConditionGroup

type OperationWorkflowMissingConditionProgress = api.OperationWorkflowMissingConditionProgress
type OperationWorkflowMissingConditionProgressCount = api.OperationWorkflowMissingConditionProgressCount
type OperationWorkflowMissingConditionProgressInstance = api.OperationWorkflowMissingConditionProgressInstance
type OperationWorkflowMissingConditionProgressChange = api.OperationWorkflowMissingConditionProgressChange

type OperationWorkflowTransitionReporting = api.OperationWorkflowTransitionReporting

type OperationWorkflowGapQueuePriority = api.OperationWorkflowGapQueuePriority
type OperationWorkflowGapQueueGuidance = api.OperationWorkflowGapQueueGuidance

type OperationWorkflowGapProgressGroup = api.OperationWorkflowGapProgressGroup

type OperationWorkflowNewMissingConditionProgress = api.OperationWorkflowNewMissingConditionProgress
type OperationWorkflowNewMissingConditionProgressCount = api.OperationWorkflowNewMissingConditionProgressCount
type OperationWorkflowNewMissingConditionProgressInstance = api.OperationWorkflowNewMissingConditionProgressInstance
type OperationWorkflowNewMissingConditionProgressChange = api.OperationWorkflowNewMissingConditionProgressChange

type OperationWorkflowReadinessCoordination = api.OperationWorkflowReadinessCoordination

type OperationWorkflowReadinessCoordinationRequest = api.OperationWorkflowReadinessCoordinationRequest

type OperationWorkflowReadinessNotificationCheck = api.OperationWorkflowReadinessNotificationCheck

type OperationWorkflowReadinessFollowUpOptions = api.OperationWorkflowReadinessFollowUpOptions

type OperationWorkflowReadinessFollowUp = api.OperationWorkflowReadinessFollowUp

type OperationWorkflowReadinessFollowUpsResponse = api.OperationWorkflowReadinessFollowUpsResponse

type OperationWorkflowReadinessDueReminder = api.OperationWorkflowReadinessDueReminder

// OperationWorkflowReadinessHandoff is an advisory brief for checked notification evidence.
type OperationWorkflowReadinessHandoff = api.OperationWorkflowReadinessHandoff

// Evidence-backed follow-up completion remains shared coordination.
type OperationWorkflowReadinessCompletionRequest = api.OperationWorkflowReadinessCompletionRequest
type OperationWorkflowReadinessCompletion = api.OperationWorkflowReadinessCompletion
type OperationWorkflowReadinessCompletionEvidence = api.OperationWorkflowReadinessCompletionEvidence
