package faas

import "github.com/poyrazK/faas/sdk/go/internal/api"

// Shared app binding metadata returned by Client.GetAppBindingInventory.
type (
	StageProjectReleaseRequest        = api.StageProjectReleaseRequest
	ProjectReleaseStage               = api.ProjectReleaseStage
	ProjectReleaseStageMember         = api.ProjectReleaseStageMember
	ProjectReleaseDeactivationReceipt = api.ProjectReleaseDeactivationReceipt
	PrepareProjectRollbackRequest     = api.PrepareProjectRollbackRequest
	CheckedProjectReleaseManifest     = api.CheckedProjectReleaseManifest
	CheckedProjectReleaseResponse     = api.CheckedProjectReleaseResponse
	PublishProjectReleaseSetRequest   = api.PublishProjectReleaseSetRequest
	ProjectReleaseSetResponse         = api.ProjectReleaseSetResponse
	ProjectReleaseSetMemberResponse   = api.ProjectReleaseSetMemberResponse

	AlertRollback                               = api.AlertRollback
	AlertRollbackDeploymentEvidence             = api.AlertRollbackDeploymentEvidence
	RollbackOperation                           = api.RollbackOperation
	RollbackRequest                             = api.RollbackRequest
	ServiceRolloutHandoffResponse               = api.ServiceRolloutHandoffResponse
	ServiceRolloutBindingGate                   = api.ServiceRolloutBindingGate
	ServiceRolloutRecoveryReceipt               = api.ServiceRolloutRecoveryReceipt
	RecoverRolloutRequest                       = api.RecoverRolloutRequest
	RolloutTransitionResponse                   = api.RolloutTransitionResponse
	RolloutRecoveryReceipt                      = api.RolloutRecoveryReceipt
	BindingReleasePolicy                        = api.BindingReleasePolicy
	SetBindingReleasePolicyRequest              = api.SetBindingReleasePolicyRequest
	SetObjectStorageBindingReleasePolicyRequest = api.SetObjectStorageBindingReleasePolicyRequest
	SetQueueBindingReleasePolicyRequest         = api.SetQueueBindingReleasePolicyRequest
	AppBindingInventory                         = api.AppBindingInventory
	AppBindingInventoryItem                     = api.AppBindingInventoryItem
	BindingApplicationAdoption                  = api.BindingApplicationAdoption
	BindingApplicationAckTarget                 = api.BindingApplicationAckTarget
	BindingAdoptionCounts                       = api.BindingAdoptionCounts
	BindingInventoryIssue                       = api.BindingInventoryIssue
	BindingVerification                         = api.BindingVerification
	BindingVerificationCheck                    = api.BindingVerificationCheck
	BindingRuntimeFreshness                     = api.BindingRuntimeFreshness
	BindingRuntimeDeployment                    = api.BindingRuntimeDeployment
	BindingRuntimeInstanceCounts                = api.BindingRuntimeInstanceCounts
	BindingRefresh                              = api.BindingRefresh
	BindingCheckFinding                         = api.BindingCheckFinding
	BindingCheckBindingResult                   = api.BindingCheckBindingResult
	BindingCheckReport                          = api.BindingCheckReport
	BindingPromotionRequest                     = api.BindingPromotionRequest
	BindingPromotionResponse                    = api.BindingPromotionResponse
	OutboundBindingProbePolicy                  = api.OutboundBindingProbePolicy
)

const (
	BindingTypeService       = api.BindingTypeService
	BindingTypePostgres      = api.BindingTypePostgres
	BindingTypeObjectStorage = api.BindingTypeObjectStorage
	BindingTypeQueue         = api.BindingTypeQueue
	BindingTypeOutbound      = api.BindingTypeOutbound
)

type CreateQueueBindingCanaryRequest = api.CreateQueueBindingCanaryRequest
type QueueBindingCanary = api.QueueBindingCanary

const QueueEvidenceHandlerCompletion = api.QueueEvidenceHandlerCompletion
const QueueCanaryCapabilityEnv = api.QueueCanaryCapabilityEnv

type BindingVerificationRun = api.BindingVerificationRun
type BindingVerificationRunStep = api.BindingVerificationRunStep
type BindingVerificationPlanStep = api.BindingVerificationPlanStep
type CreateBindingVerificationRunRequest = api.CreateBindingVerificationRunRequest

type CreateProjectBindingVerificationRunRequest = api.CreateProjectBindingVerificationRunRequest
type ProjectBindingVerificationRun = api.ProjectBindingVerificationRun

// ProjectBindingVerificationReviewDigest freezes the manifest, member reviews and consent.
func ProjectBindingVerificationReviewDigest(in CreateProjectBindingVerificationRunRequest) string {
	return api.ProjectBindingVerificationReviewDigest(in)
}

type ReviewBindingVerificationRequest = api.ReviewBindingVerificationRequest
type BindingVerificationReview = api.BindingVerificationReview
type ReviewProjectBindingVerificationRequest = api.ReviewProjectBindingVerificationRequest
type ProjectBindingVerificationReview = api.ProjectBindingVerificationReview

type BindingVerificationDiagnosticBlocker = api.BindingVerificationDiagnosticBlocker
type BindingVerificationDifference = api.BindingVerificationDifference
type BindingVerificationRecoveryAction = api.BindingVerificationRecoveryAction
type BindingVerificationRunDiagnostics = api.BindingVerificationRunDiagnostics
type ProjectBindingVerificationRunDiagnostics = api.ProjectBindingVerificationRunDiagnostics

type BindingVerificationBudget = api.BindingVerificationBudget
