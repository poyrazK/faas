// adr: 521
package faas

import "github.com/poyrazK/faas/sdk/go/internal/api"

// Operations keep business state, native execution and completion delivery
// separate. The embedded Client methods use these public wire types.
type (
	OperationJobRuntimeProof          = api.OperationJobRuntimeProof
	OperationArtifactUploadResponse   = api.OperationArtifactUploadResponse
	OperationArtifactUploadRequest    = api.OperationArtifactUploadRequest
	OperationJobControlResponse       = api.OperationJobControlResponse
	OperationJobReportRequest         = api.OperationJobReportRequest
	OperationRecoveryDecision         = api.OperationRecoveryDecision
	OperationRecoveryInspection       = api.OperationRecoveryInspection
	OperationRecoveryStep             = api.OperationRecoveryStep
	OperationRecoveryArtifact         = api.OperationRecoveryArtifact
	OperationRecoveryPreviewRequest   = api.OperationRecoveryPreviewRequest
	OperationRecoveryPreview          = api.OperationRecoveryPreview
	OperationWorkflowControlResponse  = api.OperationWorkflowControlResponse
	OperationWorkflowRuntimeProof     = api.OperationWorkflowRuntimeProof
	OperationWorkflowArtifactResponse = api.OperationWorkflowArtifactResponse
	OperationDeliveryInspection       = api.OperationDeliveryInspection
	OperationDeliveryAttempt          = api.OperationDeliveryAttempt
	OperationDeliveryAttemptsResponse = api.OperationDeliveryAttemptsResponse
	OperationDeliveryRetryRequest     = api.OperationDeliveryRetryRequest
	OperationDeliveryRetryResponse    = api.OperationDeliveryRetryResponse
	OperationState                    = api.OperationState
	OperationDefinitionSpec           = api.OperationDefinitionSpec
	OperationDefinitionResponse       = api.OperationDefinitionResponse
	OperationDefinitionsResponse      = api.OperationDefinitionsResponse
	OperationDefinitionSummary        = api.OperationDefinitionSummary
	OperationDoctorResponse           = api.OperationDoctorResponse
	OperationDoctorCheck              = api.OperationDoctorCheck
	OperationTenantIdentity           = api.OperationTenantIdentity
	OperationSubmissionScope          = api.OperationSubmissionScope
	OperationSubmissionLookupRequest  = api.OperationSubmissionLookupRequest
	OperationSubmissionLookupResponse = api.OperationSubmissionLookupResponse
	OperationProgress                 = api.OperationProgress
	OperationReportRequest            = api.OperationReportRequest
	OperationExecutionControlResponse = api.OperationExecutionControlResponse
	OperationResultArtifact           = api.OperationResultArtifact
	OperationArtifactRequest          = api.OperationArtifactRequest
	OperationDeliveryResponse         = api.OperationDeliveryResponse
	OperationResponse                 = api.OperationResponse
	OperationSummary                  = api.OperationSummary
	OperationDeliverySummary          = api.OperationDeliverySummary
	OperationListOptions              = api.OperationListOptions
	OperationListResponse             = api.OperationListResponse
	OperationExecution                = api.OperationExecution
	OperationExecutionsResponse       = api.OperationExecutionsResponse
	OperationAcceptedResponse         = api.OperationAcceptedResponse
	OperationEvent                    = api.OperationEvent
	OperationEventsResponse           = api.OperationEventsResponse
	OperationRecoveryRequest          = api.OperationRecoveryRequest
	OperationStartRequest             = api.OperationStartRequest
	OperationCancellationRequest      = api.OperationCancellationRequest
	OperationRuntimeProof             = api.OperationRuntimeProof
	OperationArtifactTruncatedError   = api.OperationArtifactTruncatedError
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

type OperationJobArtifactResponse = api.OperationJobArtifactResponse
