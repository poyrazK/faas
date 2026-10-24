package faas

import "github.com/poyrazK/faas/sdk/go/internal/api"

type ProfilingConfig = api.ProfilingConfig
type ProfileQuery = api.ProfileQuery
type ProfileResponse = api.ProfileResponse
type ProfileCoverage = api.ProfileCoverage
type ProfileSource = api.ProfileSource
type ProfileSourceLocation = api.ProfileSourceLocation
type ProfileFunction = api.ProfileFunction
type ProfileStack = api.ProfileStack
type ProfileCompareRequest = api.ProfileCompareRequest
type ProfileCompareResponse = api.ProfileCompareResponse
type ProfileFunctionDelta = api.ProfileFunctionDelta
type ProfileStackDelta = api.ProfileStackDelta

type ProfileInvestigationInput = api.ProfileInvestigationInput
type ProfileCallPath = api.ProfileCallPath
type ProfileCallPathFrame = api.ProfileCallPathFrame
type SaveProfileInvestigationRequest = api.SaveProfileInvestigationRequest
type ProfileInvestigation = api.ProfileInvestigation
type ProfileInvestigationWindowStatus = api.ProfileInvestigationWindowStatus
type ProfileInvestigationResponse = api.ProfileInvestigationResponse
type ListProfileInvestigationsResponse = api.ListProfileInvestigationsResponse

type ProfileAttributionQuality = api.ProfileAttributionQuality
type ProfileAttributionReason = api.ProfileAttributionReason
type ProfileAttributionComparison = api.ProfileAttributionComparison
type ProfileRouteLabelCoverage = api.ProfileRouteLabelCoverage
type ProfileRouteLabelComparison = api.ProfileRouteLabelComparison
type ProfileRouteRegression = api.ProfileRouteRegression
type ProfileRegressionOptions = api.ProfileRegressionOptions
type CheckProfileRegressionRequest = api.CheckProfileRegressionRequest
type ProfileRegressionMetric = api.ProfileRegressionMetric
type ProfileRegressionCPUPerRequestMetric = api.ProfileRegressionCPUPerRequestMetric
type ProfileRegressionEvidence = api.ProfileRegressionEvidence
type ProfileRegressionAssessment = api.ProfileRegressionAssessment

func DefaultProfileRegressionOptions() ProfileRegressionOptions {
	return api.DefaultProfileRegressionOptions()
}

type ProfileDeploymentPolicyConfig = api.ProfileDeploymentPolicyConfig
type ProfileDeploymentPolicy = api.ProfileDeploymentPolicy
type SaveProfileDeploymentPolicyRequest = api.SaveProfileDeploymentPolicyRequest
type ProfileDeploymentCheck = api.ProfileDeploymentCheck
type ListProfileDeploymentChecksResponse = api.ListProfileDeploymentChecksResponse

// Frozen request-mix metadata recorded with CPU assessments.
type ProfileRequestMixSnapshot = api.ProfileRequestMixSnapshot
type ProfileRequestMixWindow = api.ProfileRequestMixWindow
type ProfileRequestMixGroup = api.ProfileRequestMixGroup

// Sampled CPU attributed to bounded declared route labels.
type ProfileRouteCPU = api.ProfileRouteCPU
type ProfileRouteAdjustment = api.ProfileRouteAdjustment
type ProfileRouteWeight = api.ProfileRouteWeight

const ProfileUnattributedRoute = "[unattributed]"

type ProfileRouteAlertPayload = api.ProfileRouteAlertPayload

type PeriodicProfilePolicy = api.PeriodicProfilePolicy
type ProfilePeriodicMonitor = api.ProfilePeriodicMonitor
type ProfilePeriodicObservation = api.ProfilePeriodicObservation
type ListProfilePeriodicMonitorsResponse = api.ListProfilePeriodicMonitorsResponse

type ProfileCanaryGatePolicy = api.ProfileCanaryGatePolicy
type ProfileGateRouteStreak = api.ProfileGateRouteStreak
type ProfileCanaryGateState = api.ProfileCanaryGateState
type ProfileGateOverride = api.ProfileGateOverride
type ProfileCanaryGateDecision = api.ProfileCanaryGateDecision

type AdvanceCanaryRequest = api.AdvanceCanaryRequest
type CanaryAdvanceResponse = api.CanaryAdvanceResponse
