// adr: 521
package preview

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
	OperationDataRepair                      = api.OperationDataRepair
	OperationApprovalSpec                    = api.OperationApprovalSpec
	OperationWaitingState                    = api.OperationWaitingState
	OperationApprovalRequest                 = api.OperationApprovalRequest
	OperationApprovalActor                   = api.OperationApprovalActor
	OperationApprovalPayload                 = api.OperationApprovalPayload
	OperationApprovalDecision                = api.OperationApprovalDecision
	OperationLimits                          = api.OperationLimits
	OperationUsage                           = api.OperationUsage
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
	OperationWaiting                  = api.OperationWaiting
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
type OperationWorkflowReadinessBusinessOutcome = api.OperationWorkflowReadinessBusinessOutcome
type OperationWorkflowReadinessCompletionEvidence = api.OperationWorkflowReadinessCompletionEvidence

// Verification watches observe retained application evidence only.
type OperationWorkflowReadinessVerificationWatch = api.OperationWorkflowReadinessVerificationWatch
type OperationWorkflowReadinessVerificationWatchItem = api.OperationWorkflowReadinessVerificationWatchItem
type OperationWorkflowReadinessVerificationChange = api.OperationWorkflowReadinessVerificationChange
type OperationWorkflowReadinessVerificationEvent = api.OperationWorkflowReadinessVerificationEvent

type OperationWorkflowReadinessBusinessOutcomeWatch = api.OperationWorkflowReadinessBusinessOutcomeWatch
type OperationWorkflowReadinessBusinessOutcomeEvent = api.OperationWorkflowReadinessBusinessOutcomeEvent

type OperationWorkflowReadinessOutcomeEscalationPolicy = api.OperationWorkflowReadinessOutcomeEscalationPolicy
type OperationWorkflowReadinessOutcomeEscalationReference = api.OperationWorkflowReadinessOutcomeEscalationReference
type OperationWorkflowReadinessOutcomeEscalation = api.OperationWorkflowReadinessOutcomeEscalation

type OperationWorkflowReadinessEscalationResponse = api.OperationWorkflowReadinessEscalationResponse
type OperationWorkflowReadinessAssignmentChange = api.OperationWorkflowReadinessAssignmentChange
type OperationWorkflowReadinessEscalationResponseEvent = api.OperationWorkflowReadinessEscalationResponseEvent

type OperationWorkflowCoordinationTimelineOptions = api.OperationWorkflowCoordinationTimelineOptions
type OperationWorkflowCoordinationTimelineEvent = api.OperationWorkflowCoordinationTimelineEvent
type OperationWorkflowCoordinationTimelineSource = api.OperationWorkflowCoordinationTimelineSource
type OperationWorkflowCoordinationTimeline = api.OperationWorkflowCoordinationTimeline

type OperationWorkflowCase = api.OperationWorkflowCase
type OperationWorkflowCaseAction = api.OperationWorkflowCaseAction

type OperationWorkflowPlaybookEvidenceRequirement = api.OperationWorkflowPlaybookEvidenceRequirement
type OperationWorkflowPlaybookEvidenceCheck = api.OperationWorkflowPlaybookEvidenceCheck
type OperationWorkflowPlaybook = api.OperationWorkflowPlaybook
type OperationWorkflowPlaybookGuidance = api.OperationWorkflowPlaybookGuidance

type OperationWorkflowPlaybookProgressStartRequest = api.OperationWorkflowPlaybookProgressStartRequest

type OperationWorkflowPlaybookEvidence = api.OperationWorkflowPlaybookEvidence

type OperationWorkflowPlaybookProgressUpdateRequest = api.OperationWorkflowPlaybookProgressUpdateRequest

type OperationWorkflowPlaybookProgressItem = api.OperationWorkflowPlaybookProgressItem

type OperationWorkflowPlaybookProgressChange = api.OperationWorkflowPlaybookProgressChange

type OperationWorkflowPlaybookProgress = api.OperationWorkflowPlaybookProgress

type OperationWorkflowPlaybookApplicability = api.OperationWorkflowPlaybookApplicability

type OperationWorkflowPlaybookReviewRequest = api.OperationWorkflowPlaybookReviewRequest
type OperationWorkflowPlaybookReview = api.OperationWorkflowPlaybookReview

type OperationWorkflowPlaybookEvidenceWatchRequest = api.OperationWorkflowPlaybookEvidenceWatchRequest
type OperationWorkflowPlaybookEvidenceWatch = api.OperationWorkflowPlaybookEvidenceWatch
type OperationWorkflowPlaybookEvidenceChange = api.OperationWorkflowPlaybookEvidenceChange
type OperationWorkflowPlaybookEvidenceEvent = api.OperationWorkflowPlaybookEvidenceEvent

type OperationWorkflowPlaybookBusinessOutcome = api.OperationWorkflowPlaybookBusinessOutcome
type OperationWorkflowPlaybookOutcomeChange = api.OperationWorkflowPlaybookOutcomeChange

type OperationWorkflowPlaybookOutcomeDeadlinePolicy = api.OperationWorkflowPlaybookOutcomeDeadlinePolicy
type OperationWorkflowPlaybookOutcomeEscalation = api.OperationWorkflowPlaybookOutcomeEscalation

type OperationWorkflowPlaybookCompletionOutcome = api.OperationWorkflowPlaybookCompletionOutcome

type OperationWorkflowPlaybookCompletionReview = api.OperationWorkflowPlaybookCompletionReview
type OperationWorkflowPlaybookCompletionReviewEvent = api.OperationWorkflowPlaybookCompletionReviewEvent

type OperationWorkflowPlaybookOutcomeAcceptancePolicy = api.OperationWorkflowPlaybookOutcomeAcceptancePolicy

type OperationWorkflowPlaybookOutcomeGuidance = api.OperationWorkflowPlaybookOutcomeGuidance

type OperationWorkflowPlaybookOutcomeFollowUp = api.OperationWorkflowPlaybookOutcomeFollowUp

type OperationWorkflowPlaybookStepAssignmentPolicy = api.OperationWorkflowPlaybookStepAssignmentPolicy
type OperationWorkflowPlaybookStepAssignmentReference = api.OperationWorkflowPlaybookStepAssignmentReference
type OperationWorkflowPlaybookStepAssignment = api.OperationWorkflowPlaybookStepAssignment

type OperationWorkflowPlaybookStepDependency = api.OperationWorkflowPlaybookStepDependency
type OperationWorkflowPlaybookStepReadiness = api.OperationWorkflowPlaybookStepReadiness

type OperationWorkflowPlaybookDependencyState = api.OperationWorkflowPlaybookDependencyState
type OperationWorkflowPlaybookDependencyRegression = api.OperationWorkflowPlaybookDependencyRegression

type OperationWorkflowPlaybookDependencyRecovery = api.OperationWorkflowPlaybookDependencyRecovery

type OperationWorkflowPlaybookReviewImpact = api.OperationWorkflowPlaybookReviewImpact

type OperationWorkflowReadinessImpactSummary = api.OperationWorkflowReadinessImpactSummary
type OperationWorkflowReadinessImpactOwner = api.OperationWorkflowReadinessImpactOwner

type OperationWorkflowReadinessImpactWatchRequest = api.OperationWorkflowReadinessImpactWatchRequest
type OperationWorkflowReadinessImpactWatch = api.OperationWorkflowReadinessImpactWatch
type OperationWorkflowReadinessImpactEvent = api.OperationWorkflowReadinessImpactEvent

type OperationWorkflowReviewResponseRequest = api.OperationWorkflowReviewResponseRequest

type OperationWorkflowReviewResponseLinkRequest = api.OperationWorkflowReviewResponseLinkRequest

type OperationWorkflowReviewResponseLink = api.OperationWorkflowReviewResponseLink

type OperationWorkflowReviewResponse = api.OperationWorkflowReviewResponse

type OperationWorkflowReviewResponseChange = api.OperationWorkflowReviewResponseChange

type OperationWorkflowReviewResponseCheckInEvent = api.OperationWorkflowReviewResponseCheckInEvent

type OperationWorkflowReviewResponseBlockageEvent = api.OperationWorkflowReviewResponseBlockageEvent

type OperationWorkflowReviewResponseHealth = api.OperationWorkflowReviewResponseHealth
type OperationWorkflowReviewResponseSourceHealth = api.OperationWorkflowReviewResponseSourceHealth

type OperationWorkflowReviewResponseDecisionRequest = api.OperationWorkflowReviewResponseDecisionRequest
type OperationWorkflowReviewResponseDecision = api.OperationWorkflowReviewResponseDecision

type OperationWorkflowReviewResponseDecisionReview = api.OperationWorkflowReviewResponseDecisionReview
type OperationWorkflowReviewResponseDecisionReviewEvent = api.OperationWorkflowReviewResponseDecisionReviewEvent

type OperationWorkflowReviewResponseHandoffAcceptanceRequest = api.OperationWorkflowReviewResponseHandoffAcceptanceRequest
type OperationWorkflowReviewResponseHandoffAcceptance = api.OperationWorkflowReviewResponseHandoffAcceptance

type OperationWorkflowReviewResponseHandoffDeclineRequest = api.OperationWorkflowReviewResponseHandoffDeclineRequest
type OperationWorkflowReviewResponseHandoffDecline = api.OperationWorkflowReviewResponseHandoffDecline
type OperationWorkflowReviewResponseHandoffDeclineEvent = api.OperationWorkflowReviewResponseHandoffDeclineEvent

type OperationWorkflowReviewResponseHandoffReassignmentRequest = api.OperationWorkflowReviewResponseHandoffReassignmentRequest

type OperationWorkflowReviewResponseHandoffReassignment = api.OperationWorkflowReviewResponseHandoffReassignment

type OperationWorkflowReviewResponseHandoffReassignmentEvent = api.OperationWorkflowReviewResponseHandoffReassignmentEvent

// Escalation coordination is distinct from accepting the handoff.
type OperationWorkflowReviewResponseEscalationAcknowledgmentRequest = api.OperationWorkflowReviewResponseEscalationAcknowledgmentRequest
type OperationWorkflowReviewResponseEscalationAcknowledgment = api.OperationWorkflowReviewResponseEscalationAcknowledgment

// Completion records coordinator work, separate from business resolution.
type OperationWorkflowReviewResponseEscalationCompletionRequest = api.OperationWorkflowReviewResponseEscalationCompletionRequest
type OperationWorkflowReviewResponseEscalationCompletion = api.OperationWorkflowReviewResponseEscalationCompletion

type OperationWorkflowReviewResponseEscalationActionReviewRequest = api.OperationWorkflowReviewResponseEscalationActionReviewRequest
type OperationWorkflowReviewResponseEscalationRevisionRequest = api.OperationWorkflowReviewResponseEscalationRevisionRequest
type OperationWorkflowReviewResponseEscalationActionReview = api.OperationWorkflowReviewResponseEscalationActionReview

type OperationWorkflowReviewResponseScheduledReviewCancellationRequest = api.OperationWorkflowReviewResponseScheduledReviewCancellationRequest
type OperationWorkflowReviewResponseScheduledReviewCancellation = api.OperationWorkflowReviewResponseScheduledReviewCancellation

type OperationWorkflowReviewResponseScheduledReviewRescheduleRequest = api.OperationWorkflowReviewResponseScheduledReviewRescheduleRequest
type OperationWorkflowReviewResponseScheduledReviewReschedule = api.OperationWorkflowReviewResponseScheduledReviewReschedule

type OperationWorkflowCaseNextAction = api.OperationWorkflowCaseNextAction

type OperationWorkflowNextActionQueueItem = api.OperationWorkflowNextActionQueueItem
type OperationWorkflowNextActionCase = api.OperationWorkflowNextActionCase

type OperationWorkflowNextActionDigestPolicy = api.OperationWorkflowNextActionDigestPolicy
type OperationWorkflowNextActionDigest = api.OperationWorkflowNextActionDigest

type OperationWorkflowNextActionDigestExplanation = api.OperationWorkflowNextActionDigestExplanation

type OperationWorkflowNextActionDigestCheck = api.OperationWorkflowNextActionDigestCheck

type OperationWorkflowNextActionDigestCheckItem = api.OperationWorkflowNextActionDigestCheckItem

type OperationWorkflowActionObligation = api.OperationWorkflowActionObligation

type OperationWorkflowObligationEvent = api.OperationWorkflowObligationEvent

type OperationWorkflowCaseObligationHistoryItem = api.OperationWorkflowCaseObligationHistoryItem

type OperationWorkflowObligationReplacement = api.OperationWorkflowObligationReplacement

type OperationWorkflowObligationContext = api.OperationWorkflowObligationContext

type OperationWorkflowObligationAssessment = api.OperationWorkflowObligationAssessment

type OperationWorkflowObligationTransitionTarget = api.OperationWorkflowObligationTransitionTarget

type OperationWorkflowObligationTargetBinding = api.OperationWorkflowObligationTargetBinding

type OperationWorkflowObligationTargetRequest = api.OperationWorkflowObligationTargetRequest

type OperationWorkflowObligationFulfillment = api.OperationWorkflowObligationFulfillment
type OperationWorkflowObligationFulfillmentRequest = api.OperationWorkflowObligationFulfillmentRequest

type OperationWorkflowObligationReopening = api.OperationWorkflowObligationReopening
type OperationWorkflowObligationReopeningRequest = api.OperationWorkflowObligationReopeningRequest

type OperationWorkflowObligationFulfillmentReview = api.OperationWorkflowObligationFulfillmentReview
type OperationWorkflowObligationReaffirmationRequest = api.OperationWorkflowObligationReaffirmationRequest
type OperationWorkflowObligationReaffirmation = api.OperationWorkflowObligationReaffirmation

type OperationWorkflowFulfillmentReviewCoordinationRequest = api.OperationWorkflowFulfillmentReviewCoordinationRequest
type OperationWorkflowFulfillmentReviewCoordinationChange = api.OperationWorkflowFulfillmentReviewCoordinationChange

type OperationWorkflowFulfillmentReviewReminder = api.OperationWorkflowFulfillmentReviewReminder

type OperationWorkflowFulfillmentReviewEscalation = api.OperationWorkflowFulfillmentReviewEscalation
