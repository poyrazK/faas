package faas

import "github.com/poyrazK/faas/sdk/go/internal/api"

type RouteRequirementsConfig = api.RouteRequirementsConfig
type SavedRouteRequirements = api.SavedRouteRequirements
type SaveRouteRequirementsRequest = api.SaveRouteRequirementsRequest
type CheckRouteRequirementsRequest = api.CheckRouteRequirementsRequest
type RouteRequirementsCheck = api.RouteRequirementsCheck
type AutomaticRouteCheck = api.AutomaticRouteCheck
type RouteRequirement = api.RouteRequirement
type RouteChecks = api.RouteChecks
type RouteThrottleRequirement = api.RouteThrottleRequirement
type RouteBudgetRequirement = api.RouteBudgetRequirement
type RouteRequirementsReport = api.RouteRequirementsReport
type RouteRequirementsResult = api.RouteRequirementsResult
type RouteRequirementsFinding = api.RouteRequirementsFinding
type RoutePolicyPlan = api.RoutePolicyPlan
type RoutePolicyChange = api.RoutePolicyChange
type RoutePolicyRuleUsage = api.RoutePolicyRuleUsage
type RoutePolicyImpact = api.RoutePolicyImpact
type RoutePlanUnresolved = api.RoutePlanUnresolved
type RoutePolicyPlanRequest = api.RoutePolicyPlanRequest
type RoutePolicyApplyRequest = api.RoutePolicyApplyRequest
type RoutePolicyAppliedChange = api.RoutePolicyAppliedChange
type RoutePolicyReceipt = api.RoutePolicyReceipt
type RoutePolicyApplyResponse = api.RoutePolicyApplyResponse

type CreateEdgeRuleRequest = api.CreateEdgeRuleRequest
type UpdateEdgeRuleRequest = api.UpdateEdgeRuleRequest

type RouteGroup = api.RouteGroup
type RoutePublicException = api.RoutePublicException
type RouteCapturedOperation = api.RouteCapturedOperation
type RouteCoverageInventory = api.RouteCoverageInventory
type RouteGroupResult = api.RouteGroupResult
type RouteAssignment = api.RouteAssignment
type RouteAssignedCheck = api.RouteAssignedCheck
type RoutePolicyAffectedOperation = api.RoutePolicyAffectedOperation

type CanaryRouteGate = api.CanaryRouteGate
type SetCanaryRouteGateRequest = api.SetCanaryRouteGateRequest
type RouteGateDecision = api.RouteGateDecision

type RouteCheckChangeSummary = api.RouteCheckChangeSummary
type RouteFindingChange = api.RouteFindingChange
type RouteCheckChanges = api.RouteCheckChanges
type RouteCheckHistoryEntry = api.RouteCheckHistoryEntry
type RouteCheckHistoryPage = api.RouteCheckHistoryPage

type RouteCheckHistorySummary = api.RouteCheckHistorySummary

type RouteHealthRoute = api.RouteHealthRoute

type RouteHealthGate = api.RouteHealthGate

type SetRouteHealthGateRequest = api.SetRouteHealthGateRequest

type RouteHealthCounts = api.RouteHealthCounts

type RouteHealthWindowEvidence = api.RouteHealthWindowEvidence

type RouteHealthFinding = api.RouteHealthFinding

type CanaryProfileSignal = api.CanaryProfileSignal
type ProfileCanaryHistoryPage = api.ProfileCanaryHistoryPage

type RouteHealthReport = api.RouteHealthReport

type RouteHealthDecision = api.RouteHealthDecision

type RouteHealthEvaluationPolicy = api.RouteHealthEvaluationPolicy
type RouteHealthHistoryEntry = api.RouteHealthHistoryEntry
type RouteHealthHistoryPage = api.RouteHealthHistoryPage

type RouteHealthTransitionWebhookPayload = api.RouteHealthTransitionWebhookPayload

// Customer comparisons are optional, advisory live route-health evidence.
type RouteHealthReportOptions = api.RouteHealthReportOptions
type RouteCustomerHealthAttribution = api.RouteCustomerHealthAttribution
type RouteCustomerHealthCohort = api.RouteCustomerHealthCohort
type RouteCustomerHealthRoute = api.RouteCustomerHealthRoute
type RouteCustomerHealthReport = api.RouteCustomerHealthReport

type RouteHealthStatusCounts = api.RouteHealthStatusCounts
type RouteHealthClientErrorWindow = api.RouteHealthClientErrorWindow
type RouteHealthClientErrorFinding = api.RouteHealthClientErrorFinding
type RouteHealthClientErrorReport = api.RouteHealthClientErrorReport

type RouteHealthInvestigationOptions = api.RouteHealthInvestigationOptions
type RouteHealthInvestigationSelection = api.RouteHealthInvestigationSelection
type RouteHealthInvestigationExample = api.RouteHealthInvestigationExample
type RouteHealthInvestigationSide = api.RouteHealthInvestigationSide
type RouteHealthInvestigationWindow = api.RouteHealthInvestigationWindow
type RouteHealthInvestigation = api.RouteHealthInvestigation
type RouteHealthLatencyDiagnostics = api.RouteHealthLatencyDiagnostics
type RouteHealthLatencySample = api.RouteHealthLatencySample
type RouteHealthDependencyTiming = api.RouteHealthDependencyTiming
type RouteHealthDependencyComparison = api.RouteHealthDependencyComparison

type RouteMonitorRoute = api.RouteMonitorRoute

type RouteMonitorConfig = api.RouteMonitorConfig

type SetRouteMonitorRequest = api.SetRouteMonitorRequest

type PreviewRouteMonitorRequest = api.PreviewRouteMonitorRequest

type RouteMonitorWindow = api.RouteMonitorWindow

type RouteMonitorFinding = api.RouteMonitorFinding

type RouteMonitorReport = api.RouteMonitorReport

type RouteMonitorPreview = api.RouteMonitorPreview

type RouteMonitorEvidenceWindow = api.RouteMonitorEvidenceWindow

type RouteMonitorEvidence = api.RouteMonitorEvidence

type RouteMonitorIncidentTimelineRoute = api.RouteMonitorIncidentTimelineRoute
type RouteMonitorIncidentTimelineEntry = api.RouteMonitorIncidentTimelineEntry
type RouteMonitorIncidentEscalationSignal = api.RouteMonitorIncidentEscalationSignal
type RouteMonitorIncidentEscalation = api.RouteMonitorIncidentEscalation
type RouteMonitorDeploymentBaseline = api.RouteMonitorDeploymentBaseline

type RouteMonitorIncident = api.RouteMonitorIncident

type RouteMonitorIncidentPage = api.RouteMonitorIncidentPage

type RouteMonitorWebhookPayload = api.RouteMonitorWebhookPayload
type RouteMonitorWebhookEscalation = api.RouteMonitorWebhookEscalation

type RouteMonitorReadOptions = api.RouteMonitorReadOptions

type RouteMonitorCustomerImpact = api.RouteMonitorCustomerImpact

type RouteMonitorCustomerWindow = api.RouteMonitorCustomerWindow

type RouteMonitorCustomerCohort = api.RouteMonitorCustomerCohort

type RouteMonitorCustomerRoute = api.RouteMonitorCustomerRoute

type RouteMonitorCustomerReport = api.RouteMonitorCustomerReport

type RouteLifecycleMapping = api.RouteLifecycleMapping
type ApproveRouteLifecycleRequest = api.ApproveRouteLifecycleRequest
type RouteLifecycleApproval = api.RouteLifecycleApproval

type RouteLifecycleHistoryPage = api.RouteLifecycleHistoryPage
type RouteLifecycleHistoryEntry = api.RouteLifecycleHistoryEntry
type RouteLifecycleHistoryApproval = api.RouteLifecycleHistoryApproval
type RouteLifecycleHistoryCapture = api.RouteLifecycleHistoryCapture
