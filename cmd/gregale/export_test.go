// export_test.go exposes unexported identifiers to the external test package
// main_test (ADR-982). External tests compile as a separate unit from
// the package and its internal tests, so `go test` never holds all of
// main's tests in one compiler process. Package-level vars are exposed
// as pointers ((*main.V)) and struct fields as pointer accessors
// ((*x.F())) so tests can read and write them.
//
// When an external test needs another unexported identifier, add it here.

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingrun"
	"github.com/onebox-faas/faas/pkg/chaos"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

// Package-level types
type AddPostgresResult = addPostgresResult
type AllBindingProbePreview = allBindingProbePreview
type AppBindingInventory = appBindingInventory
type AppBindingInventoryItem = appBindingInventoryItem
type AppNetworkApp = appNetworkApp
type AppNetworkPrivateNetwork = appNetworkPrivateNetwork
type AppNetworkServiceDiscovery = appNetworkServiceDiscovery
type AppNetworkSnapshot = appNetworkSnapshot
type AppNetworkStaticEgress = appNetworkStaticEgress
type AppNetworkUpstreams = appNetworkUpstreams
type AuthCreateOptions = authCreateOptions
type AuthKeyRow = authKeyRow
type AuthPublicSettings = authPublicSettings
type StoredWorkflowCheckReport = storedWorkflowCheckReport
type StoredWorkflowCheckedPublishReport = storedWorkflowCheckedPublishReport
type StoredWorkflowCoverageExclusion = storedWorkflowCoverageExclusion
type StoredWorkflowCoverageGate = storedWorkflowCoverageGate
type StoredWorkflowCoverageHint = storedWorkflowCoverageHint
type StoredWorkflowDefinitionChange = storedWorkflowDefinitionChange
type StoredWorkflowDefinitionDiff = storedWorkflowDefinitionDiff
type StoredWorkflowEventPlan = storedWorkflowEventPlan
type StoredWorkflowEventPlanItem = storedWorkflowEventPlanItem
type StoredWorkflowEventPlanOutcome = storedWorkflowEventPlanOutcome
type StoredWorkflowLiveGateReport = storedWorkflowLiveGateReport
type StoredWorkflowLiveSuiteCaseResult = storedWorkflowLiveSuiteCaseResult
type StoredWorkflowRecoveryJournalEntry = storedWorkflowRecoveryJournalEntry
type StoredWorkflowRecoveryPlan = storedWorkflowRecoveryPlan
type StoredWorkflowRecoveryPreview = storedWorkflowRecoveryPreview
type StoredWorkflowRecoveryReport = storedWorkflowRecoveryReport
type StoredWorkflowRecoveryRetryPreview = storedWorkflowRecoveryRetryPreview
type StoredWorkflowRecoveryRetryRow = storedWorkflowRecoveryRetryRow
type StoredWorkflowRecoverySelection = storedWorkflowRecoverySelection
type StoredWorkflowResponseAction = storedWorkflowResponseAction
type StoredWorkflowResponsePlan = storedWorkflowResponsePlan
type StoredWorkflowRunSummary = storedWorkflowRunSummary
type StoredWorkflowScenario = storedWorkflowScenario
type StoredWorkflowScenarioCoverage = storedWorkflowScenarioCoverage
type StoredWorkflowScenarioResult = storedWorkflowScenarioResult
type StoredWorkflowScenarioSuite = storedWorkflowScenarioSuite
type StoredWorkflowStepExpectation = storedWorkflowStepExpectation
type BindingCreationFailureReport = bindingCreationFailureReport
type BindingPolicyVerificationReport = bindingPolicyVerificationReport
type BindingRecoverySummary = bindingRecoverySummary
type BindingReleasePolicyCheckReport = bindingReleasePolicyCheckReport
type BindingVerificationRunKey = bindingVerificationRunKey
type BindingVerificationRunStepContext = bindingVerificationRunStepContext
type BindingVerificationRunStepKey = bindingVerificationRunStepKey
type BindingVerificationSnapshotKey = bindingVerificationSnapshotKey
type CliCommand = cliCommand
type CliConfig = cliConfig
type CliConfigEntry = cliConfigEntry
type CliFlag = cliFlag
type CliPreviewSummary = cliPreviewSummary
type CliSessionMetadata = cliSessionMetadata
type CliSub = cliSub
type ConnectionContext = connectionContext
type ConnectionProfile = connectionProfile
type CustomerOperationCommand = customerOperationCommand
type CustomerOperationDeliveryCommand = customerOperationDeliveryCommand
type CustomerOperationDeliveryReceipt = customerOperationDeliveryReceipt
type CustomerOperationRecoveryReceipt = customerOperationRecoveryReceipt
type DataAPIAcceptedContracts = dataAPIAcceptedContracts
type DataAPIApprovedChange = dataAPIApprovedChange
type DataAPIChange = dataAPIChange
type DataAPIColumn = dataAPIColumn
type DataAPIComposite = dataAPIComposite
type DataAPICompositeField = dataAPICompositeField
type DataAPIConsumer = dataAPIConsumer
type DataAPICoverageMapping = dataAPICoverageMapping
type DataAPICoveragePolicy = dataAPICoveragePolicy
type DataAPICoverageRule = dataAPICoverageRule
type DataAPIDiagnosticsReport = dataAPIDiagnosticsReport
type DataAPIFailureCounts = dataAPIFailureCounts
type DataAPIFunction = dataAPIFunction
type DataAPIGuideSavedRequest = dataAPIGuideSavedRequest
type DataAPIGuideScenario = dataAPIGuideScenario
type DataAPIHealthPool = dataAPIHealthPool
type DataAPIHealthReport = dataAPIHealthReport
type DataAPIImpact = dataAPIImpact
type DataAPILatency = dataAPILatency
type DataAPIOnboardingStep = dataAPIOnboardingStep
type DataAPIQueryShape = dataAPIQueryShape
type DataAPIRefreshReceipt = dataAPIRefreshReceipt
type DataAPIRelationship = dataAPIRelationship
type DataAPIReleaseConfig = dataAPIReleaseConfig
type DataAPIRequestStats = dataAPIRequestStats
type DataAPIRotationCheck = dataAPIRotationCheck
type DataAPIRotationOutput = dataAPIRotationOutput
type DataAPIRotationOwnerReport = dataAPIRotationOwnerReport
type DataAPIRotationReport = dataAPIRotationReport
type DataAPIServingContract = dataAPIServingContract
type DataAPIShapeFilter = dataAPIShapeFilter
type DataAPIShapeOrder = dataAPIShapeOrder
type DataAPIShapeStats = dataAPIShapeStats
type DataAPISnapshot = dataAPISnapshot
type DataAPIStatus = dataAPIStatus
type DataAPISyncCommand = dataAPISyncCommand
type DataAPISyncConfig = dataAPISyncConfig
type DataAPITable = dataAPITable
type DebugIncidentBundle = debugIncidentBundle
type DebugRequestInspectOutput = debugRequestInspectOutput
type DebugRequestTraceOutput = debugRequestTraceOutput
type DebugWatchEvent = debugWatchEvent
type DeployExecution = deployExecution
type DeployIdempotencyIntent = deployIdempotencyIntent
type DeployIssueSourceMapsBindingError = deployIssueSourceMapsBindingError
type DeployPreflightSummary = deployPreflightSummary
type DeployRecovery = deployRecovery
type DeploySourceMode = deploySourceMode
type DeployStatusJSON = deployStatusJSON
type DevEnvSyncState = devEnvSyncState
type DevPatchExplainer = devPatchExplainer
type DevPhaseTiming = devPhaseTiming
type DevSetupReceipt = devSetupReceipt
type DevSourceSyncState = devSourceSyncState
type DevTarget = devTarget
type DiffCLIOptions = diffCLIOptions
type DoctorCheck = doctorCheck
type DoctorImage = doctorImage
type DoctorReport = doctorReport
type EdgeRuleActionInputs = edgeRuleActionInputs
type EdgeRuleTraceResult = edgeRuleTraceResult
type EdgeRuleTraceRow = edgeRuleTraceRow
type EnvPushReceipt = envPushReceipt
type EnvironmentAppClient = environmentAppClient
type EventRecoveryResult = eventRecoveryResult
type ExecutionWatchEvent = executionWatchEvent
type ExecutionWorkflowArtifactInput = executionWorkflowArtifactInput
type ExecutionWorkflowDependencyInput = executionWorkflowDependencyInput
type ExecutionWorkflowPlan = executionWorkflowPlan
type ExecutionWorkflowRunResult = executionWorkflowRunResult
type ExecutionWorkflowStep = executionWorkflowStep
type GithubSnippetEnv = githubSnippetEnv
type HostingCISetupRecipe = hostingCISetupRecipe
type InitReceipt = initReceipt
type InspectAppSummary = inspectAppSummary
type InspectHealthSummary = inspectHealthSummary
type InspectReleaseSummary = inspectReleaseSummary
type InspectRuntimeSummary = inspectRuntimeSummary
type InspectRuntimeWarning = inspectRuntimeWarning
type InspectSummary = inspectSummary
type InspectSummaryInputs = inspectSummaryInputs
type InspectRecommendation = inspectRecommendation
type InspectWatchEvent = inspectWatchEvent
type IssueTicketSyncResult = issueTicketSyncResult
type LocalAppLog = localAppLog
type LocalProjectContext = localProjectContext
type ManifestObjectStorageClient = manifestObjectStorageClient
type McpAccessAuditOptions = mcpAccessAuditOptions
type McpAccessAuditReport = mcpAccessAuditReport
type McpCatalogFleetHealthReport = mcpCatalogFleetHealthReport
type McpCatalogHealthReport = mcpCatalogHealthReport
type McpCatalogPinReceipt = mcpCatalogPinReceipt
type McpDeploymentReadiness = mcpDeploymentReadiness
type McpNativeRecoveryCheck = mcpNativeRecoveryCheck
type McpNativeReleasePlan = mcpNativeReleasePlan
type McpNativeReleaseState = mcpNativeReleaseState
type McpResourceWatchEvent = mcpResourceWatchEvent
type McpTasksMetricStatus = mcpTasksMetricStatus
type McpTasksObserverReadiness = mcpTasksObserverReadiness
type McpTasksObserverSourceConfig = mcpTasksObserverSourceConfig
type McpTasksStatusResult = mcpTasksStatusResult
type McpWatchEvent = mcpWatchEvent
type ObjectStorageBindingCLIList = objectStorageBindingCLIList
type ObjectStorageBindingCLIRevokeResult = objectStorageBindingCLIRevokeResult
type OpenapiPreviewOutput = openapiPreviewOutput
type PostgresBindingLister = postgresBindingLister
type PreviewCustomerCutoverDiffReport = previewCustomerCutoverDiffReport
type PreviewCustomerCutoverLatestSuccessor = previewCustomerCutoverLatestSuccessor
type PreviewCustomerCutoverOldRouteReuse = previewCustomerCutoverOldRouteReuse
type PreviewCustomerCutoverReviewCustomer = previewCustomerCutoverReviewCustomer
type PreviewCustomerCutoverReviewReport = previewCustomerCutoverReviewReport
type PreviewCustomerCutoverReviewRoute = previewCustomerCutoverReviewRoute
type PreviewCustomerCutoverReviewSuccessor = previewCustomerCutoverReviewSuccessor
type PreviewCustomerMigrationEndpoint = previewCustomerMigrationEndpoint
type PreviewReleaseRouteReview = previewReleaseRouteReview
type PreviewReleaseRouteReviewPreview = previewReleaseRouteReviewPreview
type PreviewReleaseRouteReviewSummary = previewReleaseRouteReviewSummary
type PreviewReportEvidence = previewReportEvidence
type PreviewReportRoute = previewReportRoute
type PreviewReportTest = previewReportTest
type PreviewReportTraffic = previewReportTraffic
type PreviewRouteCustomerImpact = previewRouteCustomerImpact
type PreviewRoutePolicyDrift = previewRoutePolicyDrift
type PreviewRoutePolicyDriftEvidence = previewRoutePolicyDriftEvidence
type PreviewRoutePolicyRuleChange = previewRoutePolicyRuleChange
type PreviewRouteReport = previewRouteReport
type PreviewRouteSource = previewRouteSource
type PreviewSourceBinding = previewSourceBinding
type PreviewSourceImpact = previewSourceImpact
type PreviewUnmatchedSource = previewUnmatchedSource
type ProductAccessReviewFile = productAccessReviewFile
type ProductCommand = productCommand
type ProductOverdueView = productOverdueView
type ProductPilotFixture = productPilotFixture
type ProductPlanPolicyReviewFile = productPlanPolicyReviewFile
type ProductTeamPolicyReviewFile = productTeamPolicyReviewFile
type ProfileCheckReport = profileCheckReport
type ProjectContextReceipt = projectContextReceipt
type ProjectEnvironmentCloneWaitReceipt = projectEnvironmentCloneWaitReceipt
type ProjectEnvironmentInspection = projectEnvironmentInspection
type ProjectEnvironmentPromotionWaitReceipt = projectEnvironmentPromotionWaitReceipt
type QueueCanaryConsentKey = queueCanaryConsentKey
type QueueStatusReport = queueStatusReport
type ReleaseGateFlags = releaseGateFlags
type ResumableUploadState = resumableUploadState
type RolloutHeldNotice = rolloutHeldNotice
type RouteHealthCorrelationReport = routeHealthCorrelationReport
type RouteHealthReleaseReviewReport = routeHealthReleaseReviewReport
type RouteHealthReviewReport = routeHealthReviewReport
type RouteHealthReviewRoute = routeHealthReviewRoute
type RouteHealthSuggestionReport = routeHealthSuggestionReport
type RouteInvestigationSourceRoute = routeInvestigationSourceRoute
type RouteInvestigationWithSource = routeInvestigationWithSource
type RouteMigrationDeploymentEvidence = routeMigrationDeploymentEvidence
type RouteMigrationPairReview = routeMigrationPairReview
type RouteMigrationReviewReport = routeMigrationReviewReport
type RouteMigrationRouteReview = routeMigrationRouteReview
type RouteMonitorIncidentSourceRoute = routeMonitorIncidentSourceRoute
type RouteMonitorIncidentWithSource = routeMonitorIncidentWithSource
type RouteMonitorSourceCorrelation = routeMonitorSourceCorrelation
type RouteSunsetDiff = routeSunsetDiff
type RouteSunsetDiffChange = routeSunsetDiffChange
type RouteSunsetReport = routeSunsetReport
type RouteSunsetRow = routeSunsetRow
type RouteSunsetSuccessor = routeSunsetSuccessor
type SafeBuffer = safeBuffer
type SecretsPair = secretsPair
type ServiceAuthorizationExplanation = serviceAuthorizationExplanation
type ServiceBindingConfigurationChange = serviceBindingConfigurationChange
type ServiceBindingConfigurationReceipt = serviceBindingConfigurationReceipt
type ServiceBindingProbeBatchItem = serviceBindingProbeBatchItem
type ServiceBindingProbeBatchReport = serviceBindingProbeBatchReport
type Shape = shape
type SourceRefAppPolicy = sourceRefAppPolicy
type StartPendingSubmission = startPendingSubmission
type StartPrompt = startPrompt
type StartRunner = startRunner
type TestActionEvidence = testActionEvidence
type TestActionSpec = testActionSpec
type TestAsyncRoute = testAsyncRoute
type TestBehaviorSpec = testBehaviorSpec
type TestBucket = testBucket
type TestBucketRef = testBucketRef
type TestCampaignChoiceCoverage = testCampaignChoiceCoverage
type TestCampaignCoverageEvidence = testCampaignCoverageEvidence
type TestCampaignCoverageRegressionSpec = testCampaignCoverageRegressionSpec
type TestCampaignCoverageSpec = testCampaignCoverageSpec
type TestCampaignGuidanceInput = testCampaignGuidanceInput
type TestCampaignRecoveryRegressionCheckSpec = testCampaignRecoveryRegressionCheckSpec
type TestCampaignRecoveryRegressionSpec = testCampaignRecoveryRegressionSpec
type TestCampaignSeedSearchSpec = testCampaignSeedSearchSpec
type TestCampaignTrendConfig = testCampaignTrendConfig
type TestCampaignTrendInput = testCampaignTrendInput
type TestChaosEvidence = testChaosEvidence
type TestChaosRule = testChaosRule
type TestChaosRuleEvidence = testChaosRuleEvidence
type TestChaosSpec = testChaosSpec
type TestCircuitBreakerSpec = testCircuitBreakerSpec
type TestCommandExecutionSpec = testCommandExecutionSpec
type TestComparisonGate = testComparisonGate
type TestComparisonGateCheck = testComparisonGateCheck
type TestConnectionLeaksSpec = testConnectionLeaksSpec
type TestConnectionSample = testConnectionSample
type TestConsumer = testConsumer
type TestDataCase = testDataCase
type TestDeliveryOutput = testDeliveryOutput
type TestFaultLatencyOverride = testFaultLatencyOverride
type TestFaultMatrixCaseSpec = testFaultMatrixCaseSpec
type TestFaultMatrixEvidence = testFaultMatrixEvidence
type TestFaultMinimizationOptions = testFaultMinimizationOptions
type TestFaultPercentOverride = testFaultPercentOverride
type TestFaultReplayOverrides = testFaultReplayOverrides
type TestFaultRuleRef = testFaultRuleRef
type TestHTTPExpect = testHTTPExpect
type TestAssertionProfile = testAssertionProfile
type TestHTTPRequest = testHTTPRequest
type TestHTTPRequestEvidence = testHTTPRequestEvidence
type TestRequestDeadlineSpec = testRequestDeadlineSpec
type TestInvocationOutput = testInvocationOutput
type TestJUnitSuite = testJUnitSuite
type TestLoadChaosPhase = testLoadChaosPhase
type TestLoadChaosPhaseEvidence = testLoadChaosPhaseEvidence
type TestLoadChaosPhaseSpec = testLoadChaosPhaseSpec
type TestLoadConfig = testLoadConfig
type TestLoadEvidence = testLoadEvidence
type TestLoadOverrides = testLoadOverrides
type TestLoadPhaseClock = testLoadPhaseClock
type TestLoadPhaseWindow = testLoadPhaseWindow
type TestLoadProgress = testLoadProgress
type TestLoadRecoveryConfig = testLoadRecoveryConfig
type TestLoadRecoveryEvidence = testLoadRecoveryEvidence
type TestLoadRecoverySample = testLoadRecoverySample
type TestLoadRecoverySpec = testLoadRecoverySpec
type TestLoadRecoveryStabilityEvidence = testLoadRecoveryStabilityEvidence
type TestLoadSpec = testLoadSpec
type TestLoadStreamSpec = testLoadStreamSpec
type TestLoadStageSpec = testLoadStageSpec
type TestLoadStepThresholdConfig = testLoadStepThresholdConfig
type TestLoadStepThresholdSpec = testLoadStepThresholdSpec
type TestLoadThresholdEvidence = testLoadThresholdEvidence
type TestLoadThresholds = testLoadThresholds
type TestLocalAppSpec = testLocalAppSpec
type TestLocalReadinessSpec = testLocalReadinessSpec
type TestManifest = testManifest
type TestMetricDelta = testMetricDelta
type TestMinimizationTarget = testMinimizationTarget
type TestObjectOutput = testObjectOutput
type TestObjectSpec = testObjectSpec
type TestPoolExhaustionSpec = testPoolExhaustionSpec
type TestPoolSample = testPoolSample
type TestPoolSnapshot = testPoolSnapshot
type TestPostgresSpec = testPostgresSpec
type TestPreparedScenario = testPreparedScenario
type TestRecoveryDeadlineOverride = testRecoveryDeadlineOverride
type TestRecoveryRegressionSpec = testRecoveryRegressionSpec
type TestRecoveryTrendConfig = testRecoveryTrendConfig
type TestReplayManifest = testReplayManifest
type TestReportComparison = testReportComparison
type TestReportComparisonStats = testReportComparisonStats
type TestResilienceEvidence = testResilienceEvidence
type TestResilienceSpec = testResilienceSpec
type TestRestartEvidence = testRestartEvidence
type TestRestartSpec = testRestartSpec
type TestRestartInFlightSpec = testRestartInFlightSpec
type TestRestartCheckpointSpec = testRestartCheckpointSpec
type TestRestartInFlightEvidence = testRestartInFlightEvidence
type TestRetryAmplificationEvidence = testRetryAmplificationEvidence
type TestRetryPolicy = testRetryPolicy
type TestRunComparison = testRunComparison
type TestRunPlan = testRunPlan
type TestRunReceipt = testRunReceipt
type TestScenario = testScenario
type TestScenarioStep = testScenarioStep
type TestScenarioStepEvidence = testScenarioStepEvidence
type TestService = testService
type TestServiceWakeEvidence = testServiceWakeEvidence
type TestSuite = testSuite
type TestSuiteMember = testSuiteMember
type TestSuiteOptions = testSuiteOptions
type TestSuspendResumeSpec = testSuspendResumeSpec
type TestSweepRegressionEvidence = testSweepRegressionEvidence
type TestSweepRegressionSpec = testSweepRegressionSpec
type TestWaitFor = testWaitFor
type TestWakeEvidence = testWakeEvidence
type UploadCacheCleanupResult = uploadCacheCleanupResult
type UploadCachePolicy = uploadCachePolicy
type ZeroConfigProvenance = zeroConfigProvenance
type ZeroConfigStubServer = zeroConfigStubServer

// Package-level consts
const AuthBanForever = authBanForever
const AuthEventsKeyName = authEventsKeyName
const AuthJWTSecretName = authJWTSecretName
const AuthSignerPrefix = authSignerPrefix
const AuthSigningKeyPrefix = authSigningKeyPrefix
const StoredWorkflowDefinitionYAML = storedWorkflowDefinitionYAML
const BindingProbeTaskTimeoutSeconds = bindingProbeTaskTimeoutSeconds
const BindingSmokeTaskTimeoutSeconds = bindingSmokeTaskTimeoutSeconds
const BindingTypeObjectStorage = bindingTypeObjectStorage
const BindingTypePostgres = bindingTypePostgres
const BindingTypeQueue = bindingTypeQueue
const BindingTypeService = bindingTypeService
const BuildStatusCancelled = buildStatusCancelled
const BuildStatusFailed = buildStatusFailed
const BuildStatusSucceeded = buildStatusSucceeded
const CliAudienceCustomer = cliAudienceCustomer
const CliDocsURL = cliDocsURL
const CliTestHomePrefix = cliTestHomePrefix
const ConfigKeyAPIBase = configKeyAPIBase
const ConfigKeyJSON = configKeyJSON
const ConstSlug = constSlug
const CustomerBaseline = customerBaseline
const CustomerID = customerID
const CustomerTenant = customerTenant
const CutoverBaselineDeployment = cutoverBaselineDeployment
const CutoverSuccessorDeployment = cutoverSuccessorDeployment
const DebugIncidentBundleSchema = debugIncidentBundleSchema
const DefaultDeployWaitTimeout = defaultDeployWaitTimeout
const DefaultDeployWaitTimeoutSeconds = defaultDeployWaitTimeoutSeconds
const DefaultQualificationProbeTimeout = defaultQualificationProbeTimeout
const DefaultQueueWorkloadConcurrency = defaultQueueWorkloadConcurrency
const DefaultQueueWorkloadQueueName = defaultQueueWorkloadQueueName
const DefaultQueueWorkloadTargetDepth = defaultQueueWorkloadTargetDepth
const DefaultTemplateHandler = defaultTemplateHandler
const DefaultTestCIBudget = defaultTestCIBudget
const DefaultTestCIWorkflow = defaultTestCIWorkflow
const DefaultZeroConfigSourceCapMB = defaultZeroConfigSourceCapMB
const DeployFromSourceDocsURL = deployFromSourceDocsURL
const DeploySourceAuto = deploySourceAuto
const DeploySourceHEAD = deploySourceHEAD
const DeploySourceWorktree = deploySourceWorktree
const DeploymentRowFmtWide = deploymentRowFmtWide
const DeploymentStatusCancelled = deploymentStatusCancelled
const DeploymentStatusFailed = deploymentStatusFailed
const DeploymentStatusSuperseded = deploymentStatusSuperseded
const DevDiagBindFailure = devDiagBindFailure
const DevDiagImportFailure = devDiagImportFailure
const DevDiagStartupCrash = devDiagStartupCrash
const DevDiagUpstreamFailure = devDiagUpstreamFailure
const DevEditToLiveTarget = devEditToLiveTarget
const DevGroupDependsEnv = devGroupDependsEnv
const DevGroupServiceEnv = devGroupServiceEnv
const DevGroupWorkspaceEnv = devGroupWorkspaceEnv
const DevPhaseBuild = devPhaseBuild
const DevPhaseReady = devPhaseReady
const DevPhaseSync = devPhaseSync
const DeveloperIDEnvironment = developerIDEnvironment
const DispatchSignup = dispatchSignup
const DocsSiteURL = docsSiteURL
const DoctorCheckFail = doctorCheckFail
const DoctorCheckOK = doctorCheckOK
const DoctorCheckPend = doctorCheckPend
const EventDrivenDocsURL = eventDrivenDocsURL
const ExecutionWorkflowFailurePolicyContinueIndependent = executionWorkflowFailurePolicyContinueIndependent
const ExecutionWorkflowMaxSteps = executionWorkflowMaxSteps
const FireNowStatusSucceeded = fireNowStatusSucceeded
const FunctionsDocsURL = functionsDocsURL
const InspectSummarySchemaVersion = inspectSummarySchemaVersion
const KeyValueLineMaxBytes = keyValueLineMaxBytes
const KeyringAccount = keyringAccount
const KeyringService = keyringService
const LiveCLIDeployment = liveCLIDeployment
const LiveCLIRun = liveCLIRun
const LocalAppLogLimit = localAppLogLimit
const LogsSourceHTTP = logsSourceHTTP
const LogsSourceRuntime = logsSourceRuntime
const ManagedPostgresBindingsWarning = managedPostgresBindingsWarning
const MaxCLIListPages = maxCLIListPages
const MaxDeployIdempotencyKeyLength = maxDeployIdempotencyKeyLength
const McpJournalOwnerEnv = mcpJournalOwnerEnv
const McpTasksObserverDatabaseEnvDefault = mcpTasksObserverDatabaseEnvDefault
const McpTasksObserverHeartbeatMetric = mcpTasksObserverHeartbeatMetric
const McpTasksObserverMetricsSlugEnv = mcpTasksObserverMetricsSlugEnv
const McpTasksObserverMetricsTokenEnv = mcpTasksObserverMetricsTokenEnv
const McpTasksObserverNamespaceEnv = mcpTasksObserverNamespaceEnv
const McpTasksObserverOwnerKeyEnvDefault = mcpTasksObserverOwnerKeyEnvDefault
const McpTasksOldestAgeMetric = mcpTasksOldestAgeMetric
const McpTasksOutstandingMetric = mcpTasksOutstandingMetric
const McpTasksWithoutCompatibleWorkerMetric = mcpTasksWithoutCompatibleWorkerMetric
const ModeSourceTree = modeSourceTree
const OverviewHeldDeployment = overviewHeldDeployment
const OverviewWebDeployment = overviewWebDeployment
const ParkedWindowInstanceLimit = parkedWindowInstanceLimit
const PinnedBindingDeployment = pinnedBindingDeployment
const PreviewCustomerCutoverReviewVersion = previewCustomerCutoverReviewVersion
const PreviewReportAfter = previewReportAfter
const PreviewReportBefore = previewReportBefore
const ProjectContextDirName = projectContextDirName
const ProjectContextFileName = projectContextFileName
const ProjectContextVersion = projectContextVersion
const ResumableUploadStateVersion = resumableUploadStateVersion
const RolloutStateAborted = rolloutStateAborted
const RolloutStateComplete = rolloutStateComplete
const RouteInvestigationSourceEvidenceLimit = routeInvestigationSourceEvidenceLimit
const RouteMigrationFromDeployment = routeMigrationFromDeployment
const RouteMigrationToDeployment = routeMigrationToDeployment
const RuntimeBinary = runtimeBinary
const RuntimeGo124 = runtimeGo124
const SecretsDocsURL = secretsDocsURL
const ShapeApp = shapeApp
const ShapeFunction = shapeFunction
const ShapeUnknown = shapeUnknown
const StageStatusCompleted = stageStatusCompleted
const StageStatusFailed = stageStatusFailed
const StageStatusInProgress = stageStatusInProgress
const StandaloneContextVersion = standaloneContextVersion
const StartResponseAppID = startResponseAppID
const StartResponseDeploymentID = startResponseDeploymentID
const StartResponseInstanceID = startResponseInstanceID
const StatusLive = statusLive
const StorageDocsURL = storageDocsURL
const SvcRepo = svcRepo
const TestCIVMWorkflowName = testCIVMWorkflowName
const TestCIWorkflowName = testCIWorkflowName
const TestConnectionPoll = testConnectionPoll
const TestDataCaseLimit = testDataCaseLimit
const TestDeliverySinkFixture = testDeliverySinkFixture
const TestFailureDiagnosticMaxBytes = testFailureDiagnosticMaxBytes
const TestFailureDiagnosticMaxWorkloads = testFailureDiagnosticMaxWorkloads
const TestHTTPBodyLimit = testHTTPBodyLimit
const TestTCPEchoFixture = testTCPEchoFixture
const TestTCPRelayFixture = testTCPRelayFixture
const TraceWatchRetryMax = traceWatchRetryMax
const UploadCacheMaxAge = uploadCacheMaxAge
const UploadCacheMaxEntries = uploadCacheMaxEntries
const UploadSessionAlreadyCommittedCode = uploadSessionAlreadyCommittedCode
const UploadSessionOffsetConflictCode = uploadSessionOffsetConflictCode

// Package-level vars
var TerminalSafe = terminalSafe
var ParsePostgresSuspendTimeout = parsePostgresSuspendTimeout
var RenderPostgresSuspendTimeout = renderPostgresSuspendTimeout
var RenderPostgresActivity = renderPostgresActivity
var RenderPostgresDatabaseUsage = renderPostgresDatabaseUsage
var AgentKeyScopes = agentKeyScopes
var TerminalSafeLines = terminalSafeLines
var AuthAdminHTTPClient = &authAdminHTTPClient
var AuthAdminKeyID = &authAdminKeyID
var AuthAdminKeyName = &authAdminKeyName
var AuthEmailTypes = &authEmailTypes
var AuthKeyChangePoll = &authKeyChangePoll
var AuthRestartRetryPause = &authRestartRetryPause
var AuthRestartRetryWindow = &authRestartRetryWindow
var AuthSigningKeyName = &authSigningKeyName
var BillingRunPollInterval = &billingRunPollInterval
var SandboxTemplatePoll = &sandboxTemplatePoll
var CampaignTrendTemplate = &campaignTrendTemplate
var CliCommands = &cliCommands
var CorsDefaultMethods = &corsDefaultMethods
var DataAPIRotationActions = &dataAPIRotationActions
var DataAPIDiagnosticCodes = &dataAPIDiagnosticCodes
var DataAPIDiagnosticRoutes = &dataAPIDiagnosticRoutes
var DocsPageSlugs = &docsPageSlugs
var DocsURLPrefix = &docsURLPrefix
var EdgeRuleKindVocab = &edgeRuleKindVocab
var EnvVarRefRegex = &envVarRefRegex
var ErrAppFlagConflict = &errAppFlagConflict
var ErrBoom = &errBoom
var ErrMCPJournalBusy = &errMCPJournalBusy
var ErrProjectContextNotFound = &errProjectContextNotFound
var ErrRollbackDeclined = &errRollbackDeclined
var FakeStripeLiveKey = &fakeStripeLiveKey
var GithubActionDefaultSHA = &githubActionDefaultSHA
var JsonOutput = &jsonOutput
var LoopbackBindRegex = &loopbackBindRegex
var McpNativeCheckWorkers = &mcpNativeCheckWorkers
var NonInteractive = &nonInteractive
var OsStderr = &osStderr
var GitMetadataTimeout = &gitMetadataTimeout
var GitSlowNoticeAfter = &gitSlowNoticeAfter
var GitPipeWaitDelay = &gitPipeWaitDelay
var WithProjectWaitProgress = withProjectWaitProgress
var ProjectBuildStepInterval = &projectBuildStepInterval
var WaitForProjectApplyTimeouts = waitForProjectApplyTimeouts
var OsStdin = &osStdin
var OsStdout = &osStdout
var ProfileOverride = &profileOverride
var ProjectEnvironmentClonePollInterval = &projectEnvironmentClonePollInterval
var ProjectEnvironmentPromotionPollInterval = &projectEnvironmentPromotionPollInterval
var RealtimeDrainPollInterval = &realtimeDrainPollInterval
var RolloutFlagConflicts = &rolloutFlagConflicts
var TemplateNames13 = &templateNames13
var TestOnlyProjectContextRoot = &testOnlyProjectContextRoot
var TestOnlyTTY = &testOnlyTTY
var UpdateAppRequestCLISurfaces = &updateAppRequestCLISurfaces

// Package-level funcs
var ValidCLISlug = validCLISlug
var CollectPreviewRouteReport = collectPreviewRouteReport
var ErrPreviewNoProductionParent = errPreviewNoProductionParent
var PreviewEnvironmentReviewNote = previewEnvironmentReviewNote
var AddRouteMonitorSourceOwners = addRouteMonitorSourceOwners
var AddSafeReleasePreview = addSafeReleasePreview
var AddValidateParamFlags = addValidateParamFlags
var AdmitOrReadStoredWorkflowLiveSuiteCase = admitOrReadStoredWorkflowLiveSuiteCase
var AdvancedCliCommands = advancedCliCommands
var AiRouteCallPath = aiRouteCallPath
var AlertRollbackWindowSeconds = alertRollbackWindowSeconds
var AnnounceBindingRun = announceBindingRun
var ApiBase = apiBase
var AppLabel = appLabel
var AppNetworkProbeState = appNetworkProbeState
var AppendDeployPreviewAnnotationIntent = appendDeployPreviewAnnotationIntent
var ApplyAIGuardrailFlags = applyAIGuardrailFlags
var ApplyStoredWorkflowCoverageGate = applyStoredWorkflowCoverageGate
var ApplyDeployLifecycleToCreateRequest = applyDeployLifecycleToCreateRequest
var ApplyDevManifestDefaults = applyDevManifestDefaults
var ApplyJSONFlag = applyJSONFlag
var ApplyMCPGatewayPolicy = applyMCPGatewayPolicy
var SyncExternalOAuthGatewayPolicy = syncExternalOAuthGatewayPolicy

type MCPAccessAuditOptions = mcpAccessAuditOptions

var ApplyMCPTasksStatusChecks = applyMCPTasksStatusChecks
var ApplyRolloutFlag = applyRolloutFlag
var SafeFlagName = safeFlagName
var ApplySimpleAppPlanToCreateRequest = applySimpleAppPlanToCreateRequest
var ApplyTestConnectionLeaks = applyTestConnectionLeaks
var ApplyTestFaultMatrixCase = applyTestFaultMatrixCase
var ApplyTestFaultReplayEdits = applyTestFaultReplayEdits
var ApplyTestFaultReplayOverrides = applyTestFaultReplayOverrides
var ApplyTestOutageCycles = applyTestOutageCycles
var ApplyTestPoolExhaustion = applyTestPoolExhaustion
var ApplyTestRecoveryDeadlineOverrides = applyTestRecoveryDeadlineOverrides
var ApplyTestSweepBaseline = applyTestSweepBaseline
var ApprovalPolicyRequest = approvalPolicyRequest
var AssertOneProblem = assertOneProblem
var AssessTestPreflight = assessTestPreflight
var AttachDataAPIConnectionBudget = attachDataAPIConnectionBudget
var AttachPreviewRoutePolicyDrift = attachPreviewRoutePolicyDrift
var AttachPreviewRouteRequirements = attachPreviewRouteRequirements
var AuthBanFor = authBanFor
var AuthEmailSettings = authEmailSettings
var AuthKeyKID = authKeyKID
var AuthMFASummary = authMFASummary
var AuthOAuthClientBody = authOAuthClientBody
var AuthRedirectURL = authRedirectURL
var AuthSSOBody = authSSOBody
var AuthUserRefValid = authUserRefValid
var AuthedClient = authedClient
var AuthedFakeAPI = authedFakeAPI
var AutoPackSource = autoPackSource
var StoredWorkflowCommandArgument = storedWorkflowCommandArgument
var StoredWorkflowEventJobRequest = storedWorkflowEventJobRequest
var StoredWorkflowLiveUUID = storedWorkflowLiveUUID
var StoredWorkflowPublicationEvidence = storedWorkflowPublicationEvidence
var StoredWorkflowRecoveryEventReason = storedWorkflowRecoveryEventReason
var StoredWorkflowRecoveryRetryPlan = storedWorkflowRecoveryRetryPlan
var StoredWorkflowRecoveryRunKey = storedWorkflowRecoveryRunKey
var StoredWorkflowRunJobRequest = storedWorkflowRunJobRequest
var AwakeDuration = awakeDuration
var BindingCheckCLICodes = bindingCheckCLICodes
var BindingCheckCLIInventory = bindingCheckCLIInventory
var BindingCheckCanProgress = bindingCheckCanProgress
var BindingPolicyVerificationPlan = bindingPolicyVerificationPlan
var BindingProbeOmission = bindingProbeOmission
var BoolPtr = boolPtr
var BridgeInspectionHandler = bridgeInspectionHandler
var BridgeLocalProxy = bridgeLocalProxy
var BridgeLoopbackOnly = bridgeLoopbackOnly
var BucketUploadCheckpointPath = bucketUploadCheckpointPath
var BuildAppNetworkDoctorReport = buildAppNetworkDoctorReport
var BuildAppScalePlanChanges = buildAppScalePlanChanges

// AppScalePlanWarnings returns the checks `app scale --plan` prints.
var AppScalePlanWarnings = appScalePlanWarnings
var BuildBaseline = buildBaseline
var BuildCanarySpec = buildCanarySpec
var BuildCreateRequest = buildCreateRequest
var BuildDataAPIMigrationGuide = buildDataAPIMigrationGuide
var BuildDataAPITransaction = buildDataAPITransaction
var BuildDataAPITransactionContract = buildDataAPITransactionContract
var BuildDevSetupReceipt = buildDevSetupReceipt
var BuildDiffOptions = buildDiffOptions
var BuildEdgeRuleAction = buildEdgeRuleAction
var BuildEdgeRuleTraceProposal = buildEdgeRuleTraceProposal
var BuildErrorExcerpt = buildErrorExcerpt
var BuildInspectSummary = buildInspectSummary
var BuildPending = buildPending
var BuildPreviewBuildPlan = buildPreviewBuildPlan
var BuildPreviewCustomerCutoverDiff = buildPreviewCustomerCutoverDiff
var BuildProjectEnvironmentInspection = buildProjectEnvironmentInspection
var BuildReleaseRouteHealthReviewReport = buildReleaseRouteHealthReviewReport
var BuildReleaseRouteHealthSuggestionReport = buildReleaseRouteHealthSuggestionReport
var BuildRouteHealthSuggestionReport = buildRouteHealthSuggestionReport
var BuildRouteSunsetDiff = buildRouteSunsetDiff
var BuildRouteSunsetReport = buildRouteSunsetReport
var BuildTestCampaignTrend = buildTestCampaignTrend
var BuildVerificationPackage = buildVerificationPackage
var CacheDurationSeconds = cacheDurationSeconds
var CacheRoutePattern = cacheRoutePattern
var CampaignCoverageReceipt = campaignCoverageReceipt
var CampaignCoverageScenario = campaignCoverageScenario
var CanUseResumableUpload = canUseResumableUpload
var CancelSmokeTask = cancelSmokeTask
var CanonicalServicePolicy = canonicalServicePolicy
var CanonicalStartScope = canonicalStartScope
var CaptureStoredWorkflowStdout = captureStoredWorkflowStdout
var CaptureBindingCLI = captureBindingCLI
var CapturePreviewRun = capturePreviewRun
var CaptureStderr = captureStderr
var CaptureStderrSwap = captureStderrSwap
var CaptureStdout = captureStdout
var CaptureStdoutSwap = captureStdoutSwap
var CaptureSuiteTestOutput = captureSuiteTestOutput
var CheckCapacityForCreate = checkCapacityForCreate
var CheckDestEmpty = checkDestEmpty
var CheckMCPConditionalParking = checkMCPConditionalParking
var CheckMCPNativeRetirement = checkMCPNativeRetirement
var CheckProfileConnection = checkProfileConnection
var CheckRetainedHostingPlan = checkRetainedHostingPlan
var CheckTestConnectionsAfterDrain = checkTestConnectionsAfterDrain
var CheckTestPoolAfterDrain = checkTestPoolAfterDrain
var ChooseMenuAction = chooseMenuAction
var ClassifyDevRuntimeLog = classifyDevRuntimeLog
var CleanupUploadCacheExclusive = cleanupUploadCacheExclusive
var CliConfigPath = cliConfigPath
var CliTreePlaces = cliTreePlaces
var CliTreeManifest = cliTreeManifest
var CliTreeSpelling = cliTreeSpelling
var CliTreeCanonicalFlag = cliTreeCanonicalFlag
var CliTreeFlagsForDisplay = cliTreeFlagsForDisplay
var CliTreeFlagSynonyms = &cliTreeFlagSynonyms
var CliTreeFolds = &cliTreeFolds
var CliTreeFlagFolds = &cliTreeFlagFolds
var RewriteFlagFold = rewriteFlagFold
var CliTreeFlagFoldSpelling = func(fold cliTreeFlagFold) string { return fold.spelling() }
var CliTreeFold = cliTreeFold
var ManifestLeafFlags = manifestLeafFlags
var CliTreeEarlierName = cliTreeEarlierName
var CliTreeNounsHolding = cliTreeNounsHolding
var CliTreeHintTerminal = &cliTreeHintTerminal

// CustomerManifest is every customer command, earlier names included.
func CustomerManifest() []cliCommand { return customerAudience(discoverableCliCommands()) }

var CliRelatedCommands = &cliRelatedCommands
var CliSeeAlso = cliSeeAlso
var CliPreAuthPatch = cliPreAuthPatch
var CliRevisionPinTTLSeconds = cliRevisionPinTTLSeconds
var CliSessionMetadataPath = cliSessionMetadataPath
var CmdAI = cmdAI
var CmdReleaseGates = cmdReleaseGates
var CmdRedis = cmdRedis
var CmdApprovals = cmdApprovals
var CmdAITokens = cmdAITokens
var CmdAccount = cmdAccount
var CmdAccountDelete = cmdAccountDelete
var CmdAccountExport = cmdAccountExport
var CmdAccountReleaseWebhooks = cmdAccountReleaseWebhooks
var CmdAccountRestore = cmdAccountRestore
var CmdAccountStatus = cmdAccountStatus
var CmdAdd = cmdAdd
var CmdAddBucket = cmdAddBucket
var CmdAddPostgres = cmdAddPostgres
var CmdAppCopy = cmdAppCopy
var CmdAlertActions = cmdAlertActions
var CmdAlertAdd = cmdAlertAdd
var CmdAlertInfo = cmdAlertInfo
var CmdAlertList = cmdAlertList
var CmdAlertPresetEnable = cmdAlertPresetEnable
var CmdAlertPresetList = cmdAlertPresetList
var CmdAlertRm = cmdAlertRm
var CmdAlertRotateSecret = cmdAlertRotateSecret
var CmdAlertUpdate = cmdAlertUpdate
var CmdAlerts = cmdAlerts
var CmdAlertsPreset = cmdAlertsPreset
var CmdAnalytics = cmdAnalytics
var CmdApp = cmdApp
var CmdAppChanges = cmdAppChanges
var CmdAppDispatch = cmdAppDispatch
var CmdAppExec = cmdAppExec
var CmdAppHealth = cmdAppHealth
var CmdAppScale = cmdAppScale
var CmdAppScalePlan = cmdAppScalePlan
var CmdAppSecurity = cmdAppSecurity
var CmdAppStaticEgressIP = cmdAppStaticEgressIP
var CmdAppStaticEgressIPClear = cmdAppStaticEgressIPClear
var CmdApps = cmdApps
var CmdAppsRm = cmdAppsRm
var CmdAppsRoutes = cmdAppsRoutes
var CmdAppsTCP = cmdAppsTCP
var CmdAppsUDP = cmdAppsUDP
var CmdAuditEvents = cmdAuditEvents
var CmdAuth = cmdAuth
var CmdStoredWorkflows = cmdStoredWorkflows
var CmdStoredWorkflowsCheck = cmdStoredWorkflowsCheck
var CmdStoredWorkflowsFailurePolicy = cmdStoredWorkflowsFailurePolicy
var CmdStoredWorkflowsFailureResume = cmdStoredWorkflowsFailureResume
var CmdStoredWorkflowsHealth = cmdStoredWorkflowsHealth
var CmdStoredWorkflowsHistory = cmdStoredWorkflowsHistory
var CmdStoredWorkflowsInit = cmdStoredWorkflowsInit
var CmdStoredWorkflowsPublish = cmdStoredWorkflowsPublish
var CmdStoredWorkflowsRecover = cmdStoredWorkflowsRecover
var CmdStoredWorkflowsRecoveryPolicy = cmdStoredWorkflowsRecoveryPolicy
var CmdStoredWorkflowsRecoveryPolicyEvaluate = cmdStoredWorkflowsRecoveryPolicyEvaluate
var CmdStoredWorkflowsRecoveryPolicyReviews = cmdStoredWorkflowsRecoveryPolicyReviews
var CmdStoredWorkflowsRecoveryPreview = cmdStoredWorkflowsRecoveryPreview
var CmdStoredWorkflowsRecoverySuppress = cmdStoredWorkflowsRecoverySuppress
var CmdStoredWorkflowsReport = cmdStoredWorkflowsReport
var CmdStoredWorkflowsResponseExecute = cmdStoredWorkflowsResponseExecute
var CmdStoredWorkflowsResponsePlan = cmdStoredWorkflowsResponsePlan
var CmdStoredWorkflowsSimulate = cmdStoredWorkflowsSimulate
var CmdBilling = cmdBilling
var CmdBillingBudgetPreview = cmdBillingBudgetPreview
var CmdBillingBudgets = cmdBillingBudgets
var CmdBillingCancel = cmdBillingCancel
var CmdBillingExport = cmdBillingExport
var CmdBillingFinancial = cmdBillingFinancial
var CmdBillingPaymentMethod = cmdBillingPaymentMethod
var CmdBillingPortal = cmdBillingPortal
var CmdBillingRefreshInvoice = cmdBillingRefreshInvoice
var CmdBillingRetry = cmdBillingRetry
var CmdBillingStatus = cmdBillingStatus
var CmdBindingsObjectStorageRotate = cmdBindingsObjectStorageRotate
var CmdBindingsVerify = cmdBindingsVerify
var CmdBucket = cmdBucket
var CmdBucketDefaultEncryption = cmdBucketDefaultEncryption
var CmdBucketDelete = cmdBucketDelete
var CmdBucketDeletions = cmdBucketDeletions
var CmdBucketNotifications = cmdBucketNotifications
var CmdBucketTags = cmdBucketTags
var CmdBucketUploads = cmdBucketUploads
var CmdBuild = cmdBuild
var CmdBuildList = cmdBuildList
var CmdBuildProvenance = cmdBuildProvenance
var CmdBuildSbom = cmdBuildSbom
var CmdBuildStatus = cmdBuildStatus
var CmdCache = cmdCache
var CmdCanary = cmdCanary
var CmdCanarySimulate = cmdCanarySimulate
var CmdCapabilities = cmdCapabilities
var CmdChannels = cmdChannels
var CmdChaos = cmdChaos
var CmdCommit = cmdCommit
var CmdCompletionBash = cmdCompletionBash
var CmdCompletionFish = cmdCompletionFish
var CmdCompletionPowershell = cmdCompletionPowershell
var PSLiteral = psLiteral
var CmdCompletionZsh = cmdCompletionZsh
var CmdConfig = cmdConfig
var CmdConnect = cmdConnect
var CmdConnectRepo = cmdConnectRepo
var CmdConsumers = cmdConsumers
var CmdContext = cmdContext
var CmdContextSet = cmdContextSet
var CmdCorsAllow = cmdCorsAllow
var CmdCorsLs = cmdCorsLs
var CmdCorsRm = cmdCorsRm
var CmdCorsShow = cmdCorsShow
var CmdCrons = cmdCrons
var CmdCronsCancel = cmdCronsCancel
var CmdCronsFireNowGet = cmdCronsFireNowGet
var CmdCronsInfo = cmdCronsInfo
var CmdCronsRm = cmdCronsRm
var CmdCronsRun = cmdCronsRun
var CmdCronsRuns = cmdCronsRuns
var CmdCronsUpdate = cmdCronsUpdate
var CmdDLQPurge = cmdDLQPurge
var CmdDashboard = cmdDashboard
var CmdDashboards = cmdDashboards
var CmdIncidents = cmdIncidents
var CmdDataAPI = cmdDataAPI
var CmdDataAPIConfigure = cmdDataAPIConfigure
var CmdDataAPICreate = cmdDataAPICreate
var CmdDataAPIDev = cmdDataAPIDev
var CmdDataAPIExplain = cmdDataAPIExplain
var CmdDataAPIFixture = cmdDataAPIFixture
var CmdDataAPIInstall = cmdDataAPIInstall
var CmdDataAPILive = cmdDataAPILive
var CmdDataAPIStorage = cmdDataAPIStorage
var CmdDataAPIHooks = cmdDataAPIHooks
var DataAPIHooksSQL = dataAPIHooksSQL
var DataAPIHooksUpgradeSQL = dataAPIHooksUpgradeSQL
var DataAPIHooksBacklogFull = dataAPIHooksBacklogFull
var EnableDataAPIHooks = enableDataAPIHooks
var DisableDataAPIHooks = disableDataAPIHooks
var DataAPIHooksStatus = dataAPIHooksStatus
var DataAPIHooksDelivery = dataAPIHooksDelivery
var ApplyDataAPIDrainSchedule = applyDataAPIDrainSchedule

type DataAPIHooksReceipt = dataAPIHooksReceipt

var DataAPIDrainSchedule = dataAPIDrainSchedule
var DataAPIDrainEvery = dataAPIDrainEvery
var CheckDataAPIReportSettings = checkDataAPIReportSettings
var CmdDataAPIMigration = cmdDataAPIMigration
var CmdDataAPIOnboard = cmdDataAPIOnboard
var CmdDataAPIIntegrate = cmdDataAPIIntegrate
var DataAPIIntegrationSteps = dataAPIIntegrationSteps
var CmdDataAPIRefresh = cmdDataAPIRefresh
var CmdDataAPITransaction = cmdDataAPITransaction
var CmdDataAPIVerify = cmdDataAPIVerify
var CmdDataAPIVerifyCoverage = cmdDataAPIVerifyCoverage
var CmdDataRepair = cmdDataRepair
var CmdDebug = cmdDebug
var CmdDebugBundle = cmdDebugBundle
var CmdDebugCoverage = cmdDebugCoverage
var CmdDebugDependencies = cmdDebugDependencies
var CmdDebugRegressionsWatch = cmdDebugRegressionsWatch
var CmdDebugRequests = cmdDebugRequests
var CmdDebugRequestsEvidence = cmdDebugRequestsEvidence
var CmdDebugRequestsList = cmdDebugRequestsList
var CmdDebugRequestsReplay = cmdDebugRequestsReplay
var CmdDebugRequestsWatch = cmdDebugRequestsWatch
var CmdDebugRunning = cmdDebugRunning
var CmdDelayedTaskAdd = cmdDelayedTaskAdd
var CmdDelayedTaskCancel = cmdDelayedTaskCancel
var CmdDelayedTaskGet = cmdDelayedTaskGet
var CmdDelayedTaskList = cmdDelayedTaskList
var CmdDeliver = cmdDeliver
var CmdDeployGithubSnippet = cmdDeployGithubSnippet
var CmdDeployRepoSourceRef = cmdDeployRepoSourceRef
var CmdDeployTarball = cmdDeployTarball
var CmdDeployCheck = cmdDeployCheck
var CredentialFileName = credentialFileName
var CmdDeployTarballToExisting = cmdDeployTarballToExisting
var CmdDeployment = cmdDeployment
var CmdDeploymentAliasDelete = cmdDeploymentAliasDelete
var CmdDeploymentAliasSet = cmdDeploymentAliasSet
var CmdDeploymentGet = cmdDeploymentGet
var CmdDeploymentSetMinInstances = cmdDeploymentSetMinInstances
var CmdDeploymentSummary = cmdDeploymentSummary
var CmdDeployments = cmdDeployments
var CmdDeploymentsExcludeClear = cmdDeploymentsExcludeClear
var CmdDeploys = cmdDeploys
var CmdDeploysClearObsolete = cmdDeploysClearObsolete
var CmdDeploysRetry = cmdDeploysRetry
var CmdDeploysShow = cmdDeploysShow
var CmdDeploysStatus = cmdDeploysStatus
var CmdDev = cmdDev
var CmdDevBridge = cmdDevBridge
var CmdDevSetup = cmdDevSetup
var CmdDoctorWithImageInspector = cmdDoctorWithImageInspector
var CmdDomains = cmdDomains
var CmdDomainsShow = cmdDomainsShow
var CmdDomainsVerify = cmdDomainsVerify
var CmdEdgeRuleLists = cmdEdgeRuleLists
var CmdEdgeRules = cmdEdgeRules
var EdgeRuleTrend = edgeRuleTrend
var CmdEdgeRulesCreate = cmdEdgeRulesCreate
var CmdEdgeRulesGet = cmdEdgeRulesGet
var CmdEdgeRulesList = cmdEdgeRulesList
var CmdEdgeRulesRm = cmdEdgeRulesRm
var CmdEdgeRulesTrace = cmdEdgeRulesTrace
var CmdEdgeRulesUpdate = cmdEdgeRulesUpdate
var CmdEnv = cmdEnv
var CmdEvents = cmdEvents
var CmdEventsAttempts = cmdEventsAttempts
var CmdEventsBacklog = cmdEventsBacklog
var CmdEventsDeliveries = cmdEventsDeliveries
var CmdEventsFanoutHistory = cmdEventsFanoutHistory
var CmdEventsInspect = cmdEventsInspect
var CmdEventsPreview = cmdEventsPreview
var CmdEventsPublish = cmdEventsPublish
var CmdEventsRecover = cmdEventsRecover
var CmdEventsReplayPreview = cmdEventsReplayPreview
var CmdEventsReplayRetryableFanoutFailures = cmdEventsReplayRetryableFanoutFailures
var CmdEventsSubscriptions = cmdEventsSubscriptions
var CmdEventsWorkflowBackfill = cmdEventsWorkflowBackfill
var CmdEventsWorkflowReplayPreview = cmdEventsWorkflowReplayPreview
var CmdFlags = cmdFlags
var CmdInit = cmdInit
var CmdInspect = cmdInspect
var CmdInvitations = cmdInvitations
var CmdInvocations = cmdInvocations
var CmdInvocationsGet = cmdInvocationsGet
var CmdInvocationsList = cmdInvocationsList
var CmdInvocationsWait = cmdInvocationsWait
var CmdInvoices = cmdInvoices
var CmdInvoke = cmdInvoke
var CmdIssueAutoSync = cmdIssueAutoSync
var CmdIssueCompoundAlerts = cmdIssueCompoundAlerts
var CmdIssueLinearConnection = cmdIssueLinearConnection
var CmdIssueTickets = cmdIssueTickets
var CmdIssues = cmdIssues
var CmdJobsAdd = cmdJobsAdd
var CmdJobsRun = cmdJobsRun
var CmdJobsUpdate = cmdJobsUpdate
var CmdKeys = cmdKeys
var CmdLink = cmdLink
var CmdLogDrains = cmdLogDrains
var CmdLogout = cmdLogout
var CmdLogs = cmdLogs
var CmdMCP = cmdMCP
var CmdMCPAccess = cmdMCPAccess
var CmdMCPCatalogMonitor = cmdMCPCatalogMonitor
var CmdMCPDeploy = cmdMCPDeploy
var CmdMCPInit = cmdMCPInit
var CmdMCPImportOpenAPI = cmdMCPImportOpenAPI
var CmdMCPReadiness = cmdMCPReadiness
var CmdMCPReadinessAttention = cmdMCPReadinessAttention
var CmdMCPReadinessFallback = cmdMCPReadinessFallback
var CmdMCPReadinessMaintenance = cmdMCPReadinessMaintenance
var CmdMCPReadinessNotifications = cmdMCPReadinessNotifications
var CmdMCPReadinessPagerDuty = cmdMCPReadinessPagerDuty
var CmdMCPReadinessPagerDutyAcknowledgements = cmdMCPReadinessPagerDutyAcknowledgements
var CmdMCPReadinessReminderDefaults = cmdMCPReadinessReminderDefaults
var CmdMCPRemote = cmdMCPRemote
var CmdMCPServe = cmdMCPServe
var CmdMCPTasksConfigure = cmdMCPTasksConfigure
var CmdMCPTasksReport = cmdMCPTasksReport
var CmdMail = cmdMail
var CmdMan = cmdMan
var CmdMetrics = cmdMetrics

var ParseJobCommand = parseJobCommand

var SplitJobArgv = splitJobArgv
var CmdMfa = cmdMfa
var CmdMfaConfirm = cmdMfaConfirm
var CmdMfaDisable = cmdMfaDisable
var CmdMfaRecover = cmdMfaRecover
var CmdMfaVerify = cmdMfaVerify
var CmdMirror = cmdMirror
var CmdMirrorCreate = cmdMirrorCreate
var CmdMirrorInfo = cmdMirrorInfo
var CmdMirrorList = cmdMirrorList
var CmdMirrorReplay = cmdMirrorReplay
var CmdMirrorRm = cmdMirrorRm
var CmdMirrorSummary = cmdMirrorSummary
var CmdMirrorUpdate = cmdMirrorUpdate
var CmdOpen = cmdOpen
var CmdOpenapi = cmdOpenapi
var CmdOpenapiDiff = cmdOpenapiDiff
var CmdOpenapiDryRun = cmdOpenapiDryRun
var CmdOpenapiGet = cmdOpenapiGet
var CmdOpenapiImport = cmdOpenapiImport
var CmdOpenapiPreview = cmdOpenapiPreview
var CmdOpenapiRemove = cmdOpenapiRemove
var CmdOrgs = cmdOrgs
var CmdOrgsCreate = cmdOrgsCreate
var CmdOrgsInvitationsListAll = cmdOrgsInvitationsListAll
var CmdOrgsInvitationsLs = cmdOrgsInvitationsLs
var CmdOrgsInvitationsRevoke = cmdOrgsInvitationsRevoke
var CmdOrgsKeys = cmdOrgsKeys
var CmdOrgsKeysAdd = cmdOrgsKeysAdd
var CmdOrgsKeysInfo = cmdOrgsKeysInfo
var CmdOrgsKeysList = cmdOrgsKeysList
var CmdOrgsKeysRm = cmdOrgsKeysRm
var CmdOrgsMembersChangeRole = cmdOrgsMembersChangeRole
var CmdOrgsMembersInvite = cmdOrgsMembersInvite
var CmdOrgsMembersRm = cmdOrgsMembersRm
var CmdOrgsTransferOwnership = cmdOrgsTransferOwnership
var CmdOrgsUpdate = cmdOrgsUpdate
var CmdPS = cmdPS
var CmdPark = cmdPark
var CmdPipeline = cmdPipeline
var CmdPlatformTenants = cmdPlatformTenants
var CmdPostgres = cmdPostgres
var CmdPostgresAttach = cmdPostgresAttach
var CmdPostgresBindingsCreate = cmdPostgresBindingsCreate
var CmdPostgresBindingsRotate = cmdPostgresBindingsRotate
var CmdPostgresCreate = cmdPostgresCreate
var CmdPostgresCutover = cmdPostgresCutover
var CmdPostgresDelete = cmdPostgresDelete
var CmdPostgresGet = cmdPostgresGet
var CmdPostgresList = cmdPostgresList
var CmdPostgresUndelete = cmdPostgresUndelete
var CmdPostgresResize = cmdPostgresResize
var CmdPostgresUsage = cmdPostgresUsage
var CmdPostgresOverage = cmdPostgresOverage
var CmdPreview = cmdPreview
var CmdPreviewCustomers = cmdPreviewCustomers
var CmdPreviewDestroy = cmdPreviewDestroy
var CmdPreviewReport = cmdPreviewReport
var CmdProfile = cmdProfile
var CmdProjects = cmdProjects
var CmdProjectsEnvironmentConfig = cmdProjectsEnvironmentConfig
var CmdProjectsEnvironmentConfigDiff = cmdProjectsEnvironmentConfigDiff
var CmdProjectsEnvironmentCreate = cmdProjectsEnvironmentCreate
var CmdProjectsEnvironmentGitOps = cmdProjectsEnvironmentGitOps
var CmdProjectsEnvironmentHistory = cmdProjectsEnvironmentHistory
var CmdProjectsEnvironmentInspect = cmdProjectsEnvironmentInspect
var CmdProjectsEnvironmentPolicies = cmdProjectsEnvironmentPolicies
var CmdProjectsEnvironmentPromote = cmdProjectsEnvironmentPromote
var CmdProjectsEnvironmentPromotionPreview = cmdProjectsEnvironmentPromotionPreview
var CmdProjectsEnvironmentQueues = cmdProjectsEnvironmentQueues
var CmdProjectsEnvironmentReleaseSets = cmdProjectsEnvironmentReleaseSets
var CmdProjectsEnvironmentReleases = cmdProjectsEnvironmentReleases
var CmdProjectsEnvironments = cmdProjectsEnvironments
var CmdProjectsList = cmdProjectsList
var CmdProjectsRemove = cmdProjectsRemove
var CmdProjectsUpdate = cmdProjectsUpdate
var CmdQueueAck = cmdQueueAck
var CmdQueueBindingCreate = cmdQueueBindingCreate
var CmdQueueBindingList = cmdQueueBindingList
var CmdQueueDeadLetter = cmdQueueDeadLetter
var CmdQueueDispatch = cmdQueueDispatch
var CmdQueuePeek = cmdQueuePeek
var CmdQueueReceive = cmdQueueReceive
var CmdQueueSend = cmdQueueSend
var CmdQueueState = cmdQueueState
var CmdRealtimeAuthFinalize = cmdRealtimeAuthFinalize
var CmdRealtimeAuthRotate = cmdRealtimeAuthRotate
var CmdRealtimeClose = cmdRealtimeClose
var CmdRealtimeConnections = cmdRealtimeConnections
var CmdRealtimeCreate = cmdRealtimeCreate
var CmdRealtimeDelete = cmdRealtimeDelete
var CmdRealtimeDrain = cmdRealtimeDrain
var CmdRealtimeDrainStatus = cmdRealtimeDrainStatus
var CmdRealtimeGet = cmdRealtimeGet
var CmdRealtimeOccupancy = cmdRealtimeOccupancy
var CmdRealtimePublish = cmdRealtimePublish
var CmdRealtimeSend = cmdRealtimeSend
var CmdRealtimeSubscribe = cmdRealtimeSubscribe
var CmdRealtimeUnsubscribe = cmdRealtimeUnsubscribe
var CmdRealtimeUpdate = cmdRealtimeUpdate
var CmdRegistry = cmdRegistry
var CmdRegistryList = cmdRegistryList
var CmdRegistryRm = cmdRegistryRm
var CmdRegistrySet = cmdRegistrySet
var CmdRollback = cmdRollback
var CmdRollouts = cmdRollouts
var CmdRolloutsRecover = cmdRolloutsRecover
var CmdRoutes = cmdRoutes
var CmdRoutesHealth = cmdRoutesHealth
var CmdRoutesImpact = cmdRoutesImpact
var CmdRoutesLifecycle = cmdRoutesLifecycle
var CmdRoutesLifecycleHistory = cmdRoutesLifecycleHistory
var CmdRoutesMonitor = cmdRoutesMonitor
var CmdRoutesPlan = cmdRoutesPlan
var CmdRoutesSunsets = cmdRoutesSunsets
var CmdRoutesSunsetsDiff = cmdRoutesSunsetsDiff
var CmdRun = cmdRun
var CmdRunImage = cmdRunImage
var CmdRuns = cmdRuns
var CmdSLO = cmdSLO
var CmdSLOs = cmdSLOs
var CmdScan = cmdScan
var CmdScheduledReleases = cmdScheduledReleases
var CmdSecretReferences = cmdSecretReferences
var CmdSecrets = cmdSecrets
var CmdSend = cmdSend
var CmdSignup = cmdSignup
var CmdSites = cmdSites
var CmdSitesCI = cmdSitesCI
var CmdSitesDiagnose = cmdSitesDiagnose
var CmdStart = cmdStart
var CmdSynthetics = cmdSynthetics
var CmdTail = cmdTail
var CmdTenantSurfacesAdd = cmdTenantSurfacesAdd
var CmdTenantSurfacesHostnameAdd = cmdTenantSurfacesHostnameAdd
var CmdTest = cmdTest
var CmdTestCompare = cmdTestCompare
var CmdTestInit = cmdTestInit
var CmdTestMinimize = cmdTestMinimize
var CmdTestReplay = cmdTestReplay
var CmdThrottleSuggestions = cmdThrottleSuggestions
var CmdTrace = cmdTrace
var CmdTraffic = cmdTraffic
var CmdTrafficPromote = cmdTrafficPromote
var CmdTrafficSet = cmdTrafficSet
var CmdTrustedPublishersAdd = cmdTrustedPublishersAdd
var CmdTrustedPublishersList = cmdTrustedPublishersList
var CmdTrustedPublishersRemove = cmdTrustedPublishersRemove
var CmdUnlink = cmdUnlink
var CmdUploadCacheCleanup = cmdUploadCacheCleanup
var CmdUsage = cmdUsage
var CmdUsageDaily = cmdUsageDaily
var CmdUsageList = cmdUsageList
var CmdUsageStorage = cmdUsageStorage
var CmdUsageSummary = cmdUsageSummary
var CmdWake = cmdWake
var CmdWakeAhead = cmdWakeAhead
var CmdWakeTimeline = cmdWakeTimeline
var CmdWebhookDeliveries = cmdWebhookDeliveries
var CmdWebhookInfo = cmdWebhookInfo
var CmdWebhookRetry = cmdWebhookRetry
var CmdWebhookRotateSecret = cmdWebhookRotateSecret
var CmdWebhooks = cmdWebhooks
var CmdWebhooksAdd = cmdWebhooksAdd
var CmdWebhooksList = cmdWebhooksList
var CmdWebhooksRm = cmdWebhooksRm
var CmdWebhooksUpdate = cmdWebhooksUpdate
var CmdWorkers = cmdWorkers
var CmdWorkersList = cmdWorkersList
var CmdWorkersLogs = cmdWorkersLogs
var CmdWorkersScale = cmdWorkersScale
var CmdWorkersStatus = cmdWorkersStatus
var CmdWorkflows = cmdWorkflows
var CmdWorkflowsCancel = cmdWorkflowsCancel
var CmdWorkflowsDiagnose = cmdWorkflowsDiagnose
var CmdWorkflowsEvents = cmdWorkflowsEvents
var CmdWorkflowsList = cmdWorkflowsList
var CmdWorkflowsResume = cmdWorkflowsResume
var CmdWorkflowsRetry = cmdWorkflowsRetry
var CmdWorkflowsRun = cmdWorkflowsRun
var CmdWorkflowsStatus = cmdWorkflowsStatus
var CmdWorkflowsSteps = cmdWorkflowsSteps
var CollectEventDeliveryPages = collectEventDeliveryPages

// ReleaseGateRequest builds a release gate add request from its flags.
var ReleaseGateRequest = releaseGateRequest

func CollectListPages[T any](p0 context.Context, p1 string, p2 bool, p3 func(context.Context, string) ([]T, string, error)) ([]T, string, error) {
	return collectListPages[T](p0, p1, p2, p3)
}

var CollectMCPAccessAudit = collectMCPAccessAudit
var CollectMCPAdmissionExplanation = collectMCPAdmissionExplanation
var CollectMCPConvergence = collectMCPConvergence

func CollectOffsetPages[T any](p0 context.Context, p1 int, p2 bool, p3 func(context.Context, int) ([]T, int, error)) ([]T, int, error) {
	return collectOffsetPages[T](p0, p1, p2, p3)
}

var CollectQueueStatus = collectQueueStatus
var CollectRouteHealthReleaseReview = collectRouteHealthReleaseReview
var CollectTestDiagnostics = collectTestDiagnostics
var CollectTestRecoveryFailureDiagnostics = collectTestRecoveryFailureDiagnostics
var CollectTestValidation = collectTestValidation
var CollectTestValidationForEngine = collectTestValidationForEngine
var CommittedDeploymentID = committedDeploymentID
var CompareDiagnosticManifest = compareDiagnosticManifest
var ComparePreviewContractsWithRequests = comparePreviewContractsWithRequests
var CompareTestActions = compareTestActions
var CompareTestBehavior = compareTestBehavior
var CompareTestReports = compareTestReports
var ComparisonBudgetReceipt = comparisonBudgetReceipt
var CompileExecutionWorkflowResultSchemas = compileExecutionWorkflowResultSchemas
var CompleteSmokeReport = completeSmokeReport
var ComposeProjectName = composeProjectName
var ConfigureDataAPI = configureDataAPI
var ConfigureScenarioTCP = configureScenarioTCP
var ConfigureTestWorkloadAccess = configureTestWorkloadAccess
var ConfigureTestWorkloadVisibility = configureTestWorkloadVisibility
var ScenarioInternalServices = scenarioInternalServices
var TestTerminationSignals = testTerminationSignals
var ConfiguredJSONPreference = configuredJSONPreference
var ConsumeLogStream = consumeLogStream
var ContainsString = containsString
var CopyBindingReleasePolicy = copyBindingReleasePolicy
var CorrelateRouteMonitorAffectedRoutes = correlateRouteMonitorAffectedRoutes
var CorrelateRouteMonitorIncidentSourceAuto = correlateRouteMonitorIncidentSourceAuto
var CoveragePolicy = coveragePolicy
var CreateDevSetupManifest = createDevSetupManifest
var CreateDurableProjectVerification = createDurableProjectVerification
var CreateOrFetchApp = createOrFetchApp

// DeployAppWithAlternatives runs the deploy's name resolution: slug, or the
// caller's suffixed alternative when another owner holds slug.
func DeployAppWithAlternatives(ctx context.Context, client *Client, slug, sourceDir string) (string, error) {
	req := api.CreateAppRequest{Slug: slug}
	got, _, err := createOrFetchAppWithAlternatives(ctx, client, req, req, sourceDir, nil, nil, nil, nil, nil)
	return got, err
}

var CreateTestAsyncRoutes = createTestAsyncRoutes
var CurrentUploadOffset = currentUploadOffset
var CustomerCliCommands = customerCliCommands
var CustomerUsageFixture = customerUsageFixture
var DashboardAccountURL = dashboardAccountURL
var DashboardAppURL = dashboardAppURL
var DashboardAppsNewURL = dashboardAppsNewURL
var DashboardStatelessURL = dashboardStatelessURL
var DataAPIAcceptedContractPolicy = dataAPIAcceptedContractPolicy
var DataAPIConsumerImpacts = dataAPIConsumerImpacts
var DataAPIContractFixture = dataAPIContractFixture
var DataAPIDiagnosticAllowed = dataAPIDiagnosticAllowed
var DataAPIDiagnosticFixture = dataAPIDiagnosticFixture
var DataAPIDiffFixture = dataAPIDiffFixture
var DataAPIGuideActions = dataAPIGuideActions
var DataAPIGuideShape = dataAPIGuideShape
var DataAPIHTTPS = dataAPIHTTPS
var DataAPIOnboardingQuote = dataAPIOnboardingQuote
var DataAPIOnboardingSteps = dataAPIOnboardingSteps
var DataAPIOrigin = dataAPIOrigin
var DataAPIReadinessURL = dataAPIReadinessURL
var DataAPIStorageName = dataAPIStorageName
var DataAPIStorageSQL = dataAPIStorageSQL
var DataRepairCheckPolicyFile = dataRepairCheckPolicyFile
var DataRepairRetentionScope = dataRepairRetentionScope
var DeadlineTestReceipt = deadlineTestReceipt
var DebugRequestRefArgs = debugRequestRefArgs
var DebugRequestTraceID = debugRequestTraceID
var DecodeBindingCheckCLI = decodeBindingCheckCLI
var DecodeBucketLifecycleFile = decodeBucketLifecycleFile
var DecodeBucketNotificationsFile = decodeBucketNotificationsFile
var DecodeCustomerOperationReceipt = decodeCustomerOperationReceipt
var DecodeServiceBindingConfiguration = decodeServiceBindingConfiguration
var DefaultProjectSlug = defaultProjectSlug
var DeployContextDirectory = deployContextDirectory
var DeployDeveloperSource = deployDeveloperSource
var DeployIdempotencyKey = deployIdempotencyKey

var MissingRequiredSecretNames = missingRequiredSecretNames
var DeployManifestObjectStorageBindings = deployManifestObjectStorageBindings
var DeployManifestPostgresBindings = deployManifestPostgresBindings
var DeployManifestQueueBindings = deployManifestQueueBindings

var DeployManifestLabels = deployManifestLabels
var DeployManifestTriggersWithRollback = deployManifestTriggersWithRollback
var DeployOperationIdempotencyKey = deployOperationIdempotencyKey
var DeployOptionsFingerprint = deployOptionsFingerprint
var DeployPreflightListener = deployPreflightListener
var DeployPreflightListenerWith = deployPreflightListenerWith
var DeployPreflightRepoSource = deployPreflightRepoSource
var DeployPreflightScheduled = deployPreflightScheduled
var DeployPreflightSource = deployPreflightSource
var DeployPreviewRequested = deployPreviewRequested
var DeploymentAliasRevisionLabel = deploymentAliasRevisionLabel
var DeploymentLabel = deploymentLabel
var DeploymentPromotionCommand = deploymentPromotionCommand
var DeploymentQueueCanaryBindings = deploymentQueueCanaryBindings
var DeploymentWaitResumeCommand = deploymentWaitResumeCommand
var DeriveDevWorkspaceID = deriveDevWorkspaceID
var DescribeDataAPIPool = describeDataAPIPool
var DescribeDataAPIRotationRollout = describeDataAPIRotationRollout
var DescribeDataAPIStatus = describeDataAPIStatus
var DescribeDoctorImage = describeDoctorImage
var DescribeRolloutDecision = describeRolloutDecision
var DetectGithubSnippetEnv = detectGithubSnippetEnv
var DevDiagnosticFromDeployment = devDiagnosticFromDeployment
var DevDiagnosticFromError = devDiagnosticFromError
var DevGroupDepends = devGroupDepends
var DevGroupService = devGroupService
var DevGroupServiceConfig = devGroupServiceConfig
var DevGroupWorkspace = devGroupWorkspace
var DevHistoryPatchNote = devHistoryPatchNote
var DevPatchExplanation = devPatchExplanation
var DevSetupManifestEdit = devSetupManifestEdit
var DevSetupShellCommand = devSetupShellCommand
var DeveloperIDPath = developerIDPath
var DiagnoseCommitSource = diagnoseCommitSource
var DiagnosticProblem = diagnosticProblem
var DialAppTaskAttach = dialAppTaskAttach
var DiffStoredWorkflowDefinition = diffStoredWorkflowDefinition
var DiffDataAPISnapshots = diffDataAPISnapshots
var DiffPreviewRoutePolicy = diffPreviewRoutePolicy
var DiffRequestFromCLI = diffRequestFromCLI
var DiscoverProjectSources = discoverProjectSources
var DocsURLForCode = docsURLForCode
var DocsURLForTemplate = docsURLForTemplate
var DocsURLForTopic = docsURLForTopic
var DoctorCheckDurableEntities = doctorCheckDurableEntities
var DoctorImageAccessError = doctorImageAccessError
var DoctorImageContractCheck = doctorImageContractCheck
var DoctorImageHealthcheck = doctorImageHealthcheck
var DoctorImageListenerCheck = doctorImageListenerCheck
var DrainMCPNativeRelease = drainMCPNativeRelease
var EdgePolicySummary = edgePolicySummary
var EdgeRuleValidateFlagsVisited = edgeRuleValidateFlagsVisited
var EditDistance = editDistance
var EffectiveKeyring = effectiveKeyring
var EmitInspectWatchEvent = emitInspectWatchEvent
var EmptyPilotTextSHA256 = emptyPilotTextSHA256
var EncodeCustomerOperationReceipt = encodeCustomerOperationReceipt
var EnableDataAPILive = enableDataAPILive
var EncodeCustomerOperationRecoveryReceipt = encodeCustomerOperationRecoveryReceipt
var EndpointRecoveryReport = endpointRecoveryReport
var EndpointRecoverySpec = endpointRecoverySpec
var EnsurePostgresEgress = ensurePostgresEgress
var EnsureSourceRefApp = ensureSourceRefApp
var DeployPreviewSource = deployPreviewSource
var ErrAuth = errAuth
var EnvCreate = envCreate
var EnvPull = envPull
var EnvPush = envPush
var EnvironmentDiff = environmentDiff
var EnvironmentProjectSlug = environmentProjectSlug
var EstimateCrawlerWakeCost = estimateCrawlerWakeCost
var EstimateTestWorkloadMinutes = estimateTestWorkloadMinutes
var EvaluateAllBindingCoverage = evaluateAllBindingCoverage
var EvaluateAllBindingCoverageGate = evaluateAllBindingCoverageGate
var EvaluateStoredWorkflowScenario = evaluateStoredWorkflowScenario
var EvaluateDataAPICoverage = evaluateDataAPICoverage
var EvaluateTestCampaignCoverageRegression = evaluateTestCampaignCoverageRegression
var EvaluateTestCampaignRecoveryRegression = evaluateTestCampaignRecoveryRegression
var EvaluateTestConnectionWindow = evaluateTestConnectionWindow
var EvaluateTestLoadRecovery = evaluateTestLoadRecovery
var EvaluateTestPoolPhase = evaluateTestPoolPhase
var ExecuteTestRunPlan = executeTestRunPlan
var ExecutableHeaderMismatch = executableHeaderMismatch
var ExecuteStoredWorkflowLiveSuite = executeStoredWorkflowLiveSuite
var ExecuteStoredWorkflowRecoveryPlan = executeStoredWorkflowRecoveryPlan
var ExecuteBindingPolicyVerification = executeBindingPolicyVerification
var ExecuteDurableBindingVerification = executeDurableBindingVerification
var ExecuteDurableProjectVerification = executeDurableProjectVerification
var ExecuteQueueBindingCanary = executeQueueBindingCanary
var ExecutionBundle = executionBundle
var ExecutionSource = executionSource
var ExecutionWorkflowPlanID = executionWorkflowPlanID
var ExitCodeForStatus = exitCodeForStatus
var ExpandShortExecFlags = expandShortExecFlags
var ExpandTestFaultSweep = expandTestFaultSweep
var ExpandTestOutageCycles = expandTestOutageCycles
var ExpandTestSecretValue = expandTestSecretValue
var ExplainServiceAuthorization = explainServiceAuthorization
var ExtractAppTaskTar = extractAppTaskTar
var FailedDeploymentRecovery = failedDeploymentRecovery
var FaultMatrixTestScenario = faultMatrixTestScenario
var FilterAccountSecretListByAge = filterAccountSecretListByAge
var FilterAppSecretListByAge = filterAppSecretListByAge
var FilterAppSecretListByClass = filterAppSecretListByClass
var FinalizeLogin = finalizeLogin
var FinancialMoney = financialMoney
var FindAddPostgresDatabase = findAddPostgresDatabase
var FindLatestFailedDeployment = findLatestFailedDeployment
var FindPreviewReportRoute = findPreviewReportRoute
var FindProjectContext = findProjectContext
var FindSecretStatus = findSecretStatus
var FinishMCPDeploy = finishMCPDeploy
var FinishMCPDeployWithSmoke = finishMCPDeployWithSmoke

func CheckMCPPublicationBaselineForTest(ctx context.Context, c *Client, slug, candidate, appID string, serving *string) error {
	return checkMCPPublicationBaseline(ctx, c, slug, candidate, &mcpCatalogPinReceipt{expectedAppID: appID, expectedServing: serving})
}

func ValidateMCPReviewedToolCatalogForTest(cfg mcphosting.Config, report mcphosting.Report) error {
	return validateMCPReviewedToolCatalog(cfg, report)
}

var RunMCPVerificationJobs = runMCPVerificationJobs
var FinishPreviewRequestComparison = finishPreviewRequestComparison
var FinishPreviewSecurityComparison = finishPreviewSecurityComparison
var FinishTestRunReports = finishTestRunReports
var FirstNewTestServiceRequest = firstNewTestServiceRequest
var FlagSetCommandName = flagSetCommandName
var FollowRolloutByDefault = followRolloutByDefault
var FormatArtifactCacheSummary = formatArtifactCacheSummary
var FormatCronDuration = formatCronDuration
var FormatDevPhaseDuration = formatDevPhaseDuration
var FormatDevRuntimeLog = formatDevRuntimeLog
var FormatGBHours = formatGBHours
var FormatMillicents = formatMillicents
var FormatRuntimeLogLine = formatRuntimeLogLine
var FormatSuspectedDependency = formatSuspectedDependency
var FreezeRequestFromFlags = freezeRequestFromFlags
var FunctionConcurrencyDeployHint = functionConcurrencyDeployHint
var GeneratedTestCICompareCommand = generatedTestCICompareCommand
var GeneratedTestCIInstallCommand = generatedTestCIInstallCommand
var GeneratedTestCIRunCommand = generatedTestCIRunCommand
var GeneratedTestCIVMConfigCommand = generatedTestCIVMConfigCommand
var GeneratedTestCIVMRunCommand = generatedTestCIVMRunCommand
var GitArchiveHEAD = gitArchiveHEAD
var GithubSetupCheckWaitFor = githubSetupCheckWaitFor
var GithubTarballURL = githubTarballURL
var GroupDigits = groupDigits
var HostingLocalConfig = hostingLocalConfig
var HostingLocalRead = hostingLocalRead
var HostingLocalSnapshot = hostingLocalSnapshot
var HumanBindingRuntimeFreshness = humanBindingRuntimeFreshness
var ImageDigestRequiredProblem = imageDigestRequiredProblem
var ImageRunExitCode = imageRunExitCode
var IndexByte = indexByte
var InitTestRepo = initTestRepo
var InitZeroConfigRepo = initZeroConfigRepo
var InspectArtifactOutputDirectory = inspectArtifactOutputDirectory
var InspectCommitEvent = inspectCommitEvent
var InspectData = inspectData
var InspectOpenAPI = inspectOpenAPI
var InspectProductPilotHelp = inspectProductPilotHelp
var InspectRecommendations = inspectRecommendations
var AppParkedLabel = appParkedLabel
var WriteParkedNotice = writeParkedNotice
var StartupCrashCauseHint = startupCrashCauseHint
var WaitAppReadiness = waitAppReadiness
var PrintSecretDeliveryOverview = printSecretDeliveryOverview
var SecretDeliveryRowLabel = secretDeliveryRowLabel
var InspectRuntime = inspectRuntime
var InspectResources = inspectResources
var InspectUnavailable = inspectUnavailable
var InspectWatchChanges = inspectWatchChanges
var InspectWatchExitCode = inspectWatchExitCode
var InspectWatchPermanentError = inspectWatchPermanentError
var InvokeStatusOK = invokeStatusOK
var IsCompletedDeployment = isCompletedDeployment
var IsTerminalBuildStatus = isTerminalBuildStatus
var IsTerminalDeploymentStatus = isTerminalDeploymentStatus
var IsValidDeploymentAnnotationTag = isValidDeploymentAnnotationTag
var JoinDevGroup = joinDevGroup
var JsonOut = jsonOut
var LinkedAppSlug = linkedAppSlug
var LinkedProjectContext = linkedProjectContext
var LiveCLIView = liveCLIView
var LiveServicePolicy = liveServicePolicy
var LiveSuiteCaseKey = liveSuiteCaseKey
var LiveSuiteHash = liveSuiteHash
var LoadStoredWorkflowScenarios = loadStoredWorkflowScenarios
var LoadBucketUploadCheckpoint = loadBucketUploadCheckpoint
var LoadDataAPIRotationCommand = loadDataAPIRotationCommand
var LoadDevManifest = loadDevManifest
var LoadMCPNativeState = loadMCPNativeState
var LoadMCPReleasePolicy = loadMCPReleasePolicy
var LoadMCPReleasePolicyWithTokens = loadMCPReleasePolicyWithTokens
var LoadOrCreateDeveloperID = loadOrCreateDeveloperID
var LoadTestStep = loadTestStep
var LoadToken = loadToken
var LoadWorkflowManifestForDeploy = loadWorkflowManifestForDeploy
var LargestSourceTarballMB = largestSourceTarballMB
var LocalTestConsumerEnv = localTestConsumerEnv
var LockStoredWorkflowLiveSuite = lockStoredWorkflowLiveSuite
var LockBucketUploadCheckpoint = lockBucketUploadCheckpoint
var LockResumableUploadState = lockResumableUploadState
var LogDrainAuthHeaderPair = logDrainAuthHeaderPair
var LooksLikeFlag = looksLikeFlag
var LookupCliCommand = lookupCliCommand
var ManRoffFlagName = manRoffFlagName
var ManagedExecutionWorkflowRequest = managedExecutionWorkflowRequest
var ManagedMonitorAction = managedMonitorAction
var ManifestDeploymentScope = manifestDeploymentScope
var ManifestPostgresDeploymentScope = manifestPostgresDeploymentScope
var MapFailureProblem = mapFailureProblem
var MarkdownComparisonText = markdownComparisonText
var MaterializeDeployArchive = materializeDeployArchive
var MaterializeScenarioFixture = materializeScenarioFixture
var McpAccessSourceChecks = mcpAccessSourceChecks
var McpArguments = mcpArguments
var McpAuthorizationChecksPassed = mcpAuthorizationChecksPassed
var McpCatalogDigestForPin = mcpCatalogDigestForPin
var McpExpectedGatewayAction = mcpExpectedGatewayAction
var McpHealthURL = mcpHealthURL
var McpInputIsTerminal = mcpInputIsTerminal
var McpLocalCLIDir = mcpLocalCLIDir
var McpNativeDeploy = mcpNativeDeploy
var McpNativeFingerprint = mcpNativeFingerprint
var McpNativeWorkerLogs = mcpNativeWorkerLogs
var McpTasksObserverConfigEnvNames = mcpTasksObserverConfigEnvNames
var McpTasksObserverCredential = mcpTasksObserverCredential
var McpTasksRuntimeApp = mcpTasksRuntimeApp
var McpTasksObserverProcessDetected = mcpTasksObserverProcessDetected
var PrintReleaseTaskOutput = printReleaseTaskOutput
var McpTasksObserverEntrypointDetected = mcpTasksObserverEntrypointDetected
var McpTasksObserverHeartbeatIsFresh = mcpTasksObserverHeartbeatIsFresh
var McpTasksObserverMetricsTargetMatches = mcpTasksObserverMetricsTargetMatches
var McpTasksObserverNamespacesMatch = mcpTasksObserverNamespacesMatch
var McpToken = mcpToken
var MdFlagLabel = mdFlagLabel
var MenuActions = menuActions
var MergeAppFlag = mergeAppFlag
var MergeDevSetupYAML = mergeDevSetupYAML
var MigrationEndpoint = migrationEndpoint
var MinimizationTestOptions = minimizationTestOptions
var MinimizationTestReceipt = minimizationTestReceipt
var MinimizationTestScenario = minimizationTestScenario
var MinimizedTestReplay = minimizedTestReplay
var MonitorTestConnectionLeaks = monitorTestConnectionLeaks
var MonitorTestPoolExhaustion = monitorTestPoolExhaustion
var MonitorTokenEnv = monitorTokenEnv
var MustGit = mustGit
var NewAuthSigningKey = newAuthSigningKey
var NewBindingProgress = newBindingProgress
var NewBucketResumeFixture = newBucketResumeFixture
var NewCLIPreview = newCLIPreview
var NewDataAPIDiagnosticsCollector = newDataAPIDiagnosticsCollector
var NewDeployReceipt = newDeployReceipt
var NewDeployRecovery = newDeployRecovery
var NewDevPhaseTracker = newDevPhaseTracker
var NewExplainCollector = newExplainCollector
var NewFakeAPI = newFakeAPI
var NewMCPAccessAuditOptions = newMCPAccessAuditOptions
var NewPreviewReportRoute = newPreviewReportRoute
var NewProductPilotFixture = newProductPilotFixture
var NewSafeReleasePreview = newSafeReleasePreview
var NewServiceExplanation = newServiceExplanation
var NewTestActionCollector = newTestActionCollector
var NewTestLoadPhaseClock = newTestLoadPhaseClock
var NewTestProxy = newTestProxy
var NewTestReplayManifest = newTestReplayManifest
var NewZeroConfigStubServer = newZeroConfigStubServer
var NextStepsFor = nextStepsFor
var NormalizeAnalyticsArgs = normalizeAnalyticsArgs
var NormalizeDeployPreviewFlags = normalizeDeployPreviewFlags
var NormalizeDocsURL = normalizeDocsURL
var NormalizeFlagDiagnostic = normalizeFlagDiagnostic
var NormalizeGithubActionSHA = normalizeGithubActionSHA
var NormalizeLogsSince = normalizeLogsSince
var OneLine = oneLine
var OpenAppTaskStreamSession = openAppTaskStreamSession
var OpenCustomerDirectoryForSync = openCustomerDirectoryForSync
var OpenCustomerFile = openCustomerFile
var OpenPublicAuthAfterTokenRemoval = openPublicAuthAfterTokenRemoval
var OutageCyclesConfig = outageCyclesConfig
var OutageCyclesReceipt = outageCyclesReceipt
var OutageCyclesScenario = outageCyclesScenario
var OutputAlertRollback = outputAlertRollback
var OwnMCPJournal = ownMCPJournal
var PackDirToTarGz = packDirToTarGz
var PackGitArchive = packGitArchive

// NormalizeGitArchive normalizes a `git archive HEAD` of repoRoot taken at
// prefix ("" for the whole tree).
func NormalizeGitArchive(repoRoot, prefix, archivePath string) ([]string, error) {
	return normalizeGitArchive(gitArchiveSource{repoRoot: repoRoot, prefix: prefix, commit: "HEAD"}, archivePath)
}

var ParkedArchivePendingNote = parkedArchivePendingNote
var ParkedLogsSelection = parkedLogsSelection
var ParkedWindowInstances = parkedWindowInstances
var RetainedLineLevelMatches = retainedLineLevelMatches
var ParseAIPriceSpec = parseAIPriceSpec

var FormatPriceTiers = formatPriceTiers
var ParseAIRouteTargets = parseAIRouteTargets
var ParseStoredWorkflowLiveBaseline = parseStoredWorkflowLiveBaseline
var ParseStoredWorkflowLiveGate = parseStoredWorkflowLiveGate
var ParseStoredWorkflowLiveSuite = parseStoredWorkflowLiveSuite
var ParseBillingStatusFlags = parseBillingStatusFlags
var ParseCanaryStages = parseCanaryStages
var ParseCustomerMilestoneCommand = parseCustomerMilestoneCommand
var ParseCustomerOperationBindings = parseCustomerOperationBindings
var ParseCustomerOperationCommand = parseCustomerOperationCommand
var ParseCustomerOperationDelivery = parseCustomerOperationDelivery
var ParseCustomerOperationSignal = parseCustomerOperationSignal

// NewTenantCustomerOperationSignalClient builds the --self signal client
// (ADR-20261017100853), whose fields are unexported.
func NewTenantCustomerOperationSignalClient(client *api.Client, definition string) tenantCustomerOperationSignalClient {
	return tenantCustomerOperationSignalClient{client: client, definition: definition}
}

// DialDevDebugTunnel dials one debugger tunnel (ADR-20261111150000).
func DialDevDebugTunnel(ctx context.Context, tunnelURL, token string) error {
	conn, err := newDevDebugDialer(tunnelURL, token)(ctx)
	if conn != nil {
		_ = conn.Close()
	}
	return err
}

// NewEnvironmentAppClient builds a stage app client whose revision pin starts unset.
func NewEnvironmentAppClient(client *api.Client, environment string, revision *int64) EnvironmentAppClient {
	return environmentAppClient{Client: client, environment: environment, revision: revision}
}

var ParseDataAPIContract = parseDataAPIContract
var ParseDataAPIHealthPool = parseDataAPIHealthPool
var ParseDataAPIRequestRecord = parseDataAPIRequestRecord
var ParseUSDMillicents = parseUSDMillicents
var ParseGithubSetupDeployBranches = parseGithubSetupDeployBranches
var ParseImageRunArgs = parseImageRunArgs
var ParseInterspersed = parseInterspersed
var ParseMCPServeOptions = parseMCPServeOptions
var ParsePortForwardSpec = parsePortForwardSpec
var ParseProductCommand = parseProductCommand
var ParseReleaseAtFlag = parseReleaseAtFlag
var ParseRevisionRef = parseRevisionRef
var ParseRouteMonitorCLI = parseRouteMonitorCLI
var ParseRouteMonitorCodeowners = parseRouteMonitorCodeowners
var ParseSecretAge = parseSecretAge
var ParseSecretsPair = parseSecretsPair
var ParseStandardApplicationCLI = parseStandardApplicationCLI
var ParseStandardCLI = parseStandardCLI
var ParseStandardInspectionCLI = parseStandardInspectionCLI
var ParseStandardMutationCLI = parseStandardMutationCLI
var ParseStandardRolloutCLI = parseStandardRolloutCLI
var ParseTestCIProfiles = parseTestCIProfiles
var PassedObjectStorageCLIReport = passedObjectStorageCLIReport
var PercentileMS = percentileMS
var PinnedBindingProbeOutput = pinnedBindingProbeOutput
var PipeStdin = pipeStdin
var PlanBlockedError = planBlockedError
var PlanProblem = planProblem
var PollBindingCheck = pollBindingCheck
var PollBindingCheckReleasePolicy = pollBindingCheckReleasePolicy
var PollRollbackOperation = pollRollbackOperation
var PollRolloutStatus = pollRolloutStatus
var PoolSnapshotError = poolSnapshotError
var PortableDevFile = portableDevFile
var PostgresBindingIsNew = postgresBindingIsNew
var PostgresUsageObservedLabel = postgresUsageObservedLabel
var PreAuthSummary = preAuthSummary
var PrepareStoredWorkflowExpectation = prepareStoredWorkflowExpectation
var PrepareStoredWorkflowLiveSuite = prepareStoredWorkflowLiveSuite
var PrepareBindingPolicyVerification = prepareBindingPolicyVerification
var PrepareDeployIssueSourceMaps = prepareDeployIssueSourceMaps
var PrepareTestBehavior = prepareTestBehavior
var PrepareTestCommandFiles = prepareTestCommandFiles
var PrepareTestObjects = prepareTestObjects
var PrepareTestPostgres = prepareTestPostgres
var PrepareTestRecoveryFailureDiagnostics = prepareTestRecoveryFailureDiagnostics
var PrepareTestRehearsal = prepareTestRehearsal
var PrepareTestReplay = prepareTestReplay
var PrepareTestRunPlans = prepareTestRunPlans
var PrepareTestSuite = prepareTestSuite
var PrepareTestSuspendResume = prepareTestSuspendResume
var PrepareTestSweepBaselines = prepareTestSweepBaselines
var PreviewCronsFromManifest = previewCronsFromManifest
var PreviewEdgeRules = previewEdgeRules
var PreviewExecutionWorkflow = previewExecutionWorkflow
var PreviewReportAnalytics = previewReportAnalytics
var PreviewReportHasBreaks = previewReportHasBreaks
var PreviewReportHasRequestBreaks = previewReportHasRequestBreaks
var PreviewReportHasSecurityRegressions = previewReportHasSecurityRegressions
var PreviewReportSpec = previewReportSpec
var PreviewRequirementsHost = previewRequirementsHost
var PrimaryDomainOrFallback = primaryDomainOrFallback
var PrintAppSettingsReceipt = printAppSettingsReceipt
var PrintStoredWorkflowCheckReport = printStoredWorkflowCheckReport
var PrintStoredWorkflowDefinitionDiff = printStoredWorkflowDefinitionDiff
var PrintStoredWorkflowRevisionDetails = printStoredWorkflowRevisionDetails
var PrintBuildStatus = printBuildStatus
var PrintDeployColdWakeSentence = printDeployColdWakeSentence
var PrintDoctorReport = printDoctorReport
var PrintDomainDNSRecords = printDomainDNSRecords
var PrintErr = printErr
var PrintLocalCommandHelp = printLocalCommandHelp
var PrintLocalCommandHelpDetail = printLocalCommandHelpDetail
var PrintLocalHelpFlags = printLocalHelpFlags
var PrintLocalSubcommandHelp = printLocalSubcommandHelp
var PrintMCPHostingDoctor = printMCPHostingDoctor
var PrintParkedWindowLogs = printParkedWindowLogs
var PrintPlanText = printPlanText
var PrintPlanTextWithExplain = printPlanTextWithExplain
var PrintPostgresBindingDelivery = printPostgresBindingDelivery
var PrintProvenance = printProvenance
var PrintRealtimeOccupancy = printRealtimeOccupancy
var PrintRealtimeUsage = printRealtimeUsage
var PrintRecoveryPollingHealth = printRecoveryPollingHealth
var PrintResidentCostEcho = printResidentCostEcho
var PrintTestCampaignCoverage = printTestCampaignCoverage
var PrintTestLoadProgress = printTestLoadProgress
var PrintTestLoadSummary = printTestLoadSummary
var PrintTestReportComparison = printTestReportComparison
var PrioritizePreviewRouteReview = prioritizePreviewRouteReview
var ProbeDataAPIHealth = probeDataAPIHealth
var ProbeMCPCatalogDeployment = probeMCPCatalogDeployment
var ProbeMCPCatalogExactFleet = probeMCPCatalogExactFleet
var ProbeMCPCatalogHealth = probeMCPCatalogHealth
var ProbeWakeState = probeWakeState
var ProductReportExit = productReportExit
var ProjectContextPath = projectContextPath
var ProjectContextRoot = projectContextRoot
var ProjectContextRootAllowed = projectContextRootAllowed
var ProjectEnvironmentPreflightExitCode = projectEnvironmentPreflightExitCode
var ProjectInspectionTarget = projectInspectionTarget
var PromptMCPInput = promptMCPInput
var ProvisionTestConsumers = provisionTestConsumers
var PublishStoredWorkflowWithScenarios = publishStoredWorkflowWithScenarios
var PublishDeployIssueSourceMaps = publishDeployIssueSourceMaps
var PumpAppTaskAttach = pumpAppTaskAttach
var QualifyProjectEnvironmentWithProfile = qualifyProjectEnvironmentWithProfile
var QualifyProjectEnvironmentWithProfileAndHTTPClient = qualifyProjectEnvironmentWithProfileAndHTTPClient
var QueueScalingBounds = queueScalingBounds
var QueueStatusHealth = queueStatusHealth
var RandomAuthSecret = randomAuthSecret
var RankDataAPIQueryShapes = rankDataAPIQueryShapes
var ReadAuthzFile = readAuthzFile
var ReadStoredWorkflowDefinition = readStoredWorkflowDefinition
var ReadStoredWorkflowEventPlan = readStoredWorkflowEventPlan
var ReadStoredWorkflowItemMockAttempts = readStoredWorkflowItemMockAttempts
var ReadStoredWorkflowLiveSuite = readStoredWorkflowLiveSuite
var ReadStoredWorkflowRecoveryPlan = readStoredWorkflowRecoveryPlan
var ReadStoredWorkflowResponsePlan = readStoredWorkflowResponsePlan
var ReadCapturedDeployArchive = readCapturedDeployArchive
var ReadCheckedReleaseFile = readCheckedReleaseFile
var ReadCoverageEvidence = readCoverageEvidence
var ReadCredentialLine = readCredentialLine
var ReadCustomerOperationPrivateReceiptBound = readCustomerOperationPrivateReceiptBound
var ReadDataAPIConnectionBudget = readDataAPIConnectionBudget
var ReadDataAPIConsumers = readDataAPIConsumers
var ReadDataAPIDiagnostics = readDataAPIDiagnostics
var ReadDataAPIGuideRequests = readDataAPIGuideRequests
var ReadDataAPILive = readDataAPILive
var ReadDataAPIReleaseConfig = readDataAPIReleaseConfig
var ReadDevEnvFile = readDevEnvFile
var ReadDevServiceOverrideFile = readDevServiceOverrideFile
var ReadEdgeRuleTraceConfig = readEdgeRuleTraceConfig
var ReadInteractivePassword = readInteractivePassword
var ReadMCPContract = readMCPContract
var ReadMCPNativePlan = readMCPNativePlan
var ReadMCPTasksObserverSourceConfig = readMCPTasksObserverSourceConfig
var ReadOpenapiDocument = readOpenapiDocument
var ReadPreviewCoverageRequirements = readPreviewCoverageRequirements
var ReadPreviewReleaseRouteReview = readPreviewReleaseRouteReview
var ReadPreviewRouteRequirements = readPreviewRouteRequirements
var ReadProductOnboardingPreview = readProductOnboardingPreview
var ReadProductOnboardingToken = readProductOnboardingToken
var ReadProjectEnvironmentQualificationProfile = readProjectEnvironmentQualificationProfile
var ReadRouteMonitorRoutes = readRouteMonitorRoutes
var ReadRoutePolicyPlan = readRoutePolicyPlan
var ReadSavedToken = readSavedToken
var ReadSecretsFile = readSecretsFile
var ProjectDeployInitialSecrets = projectDeployInitialSecrets
var ProjectSecretOwnersFor = projectSecretOwnersFor
var ReadStandardReviewCLI = readStandardReviewCLI
var ReadTestBaselineReport = readTestBaselineReport
var ReadScaffoldOpenAPIDocument = readScaffoldOpenAPIDocument
var ReadTestCircuitCounter = readTestCircuitCounter
var ReadTestComparisonBudget = readTestComparisonBudget
var ReadTestConnections = readTestConnections
var ReadTestData = readTestData
var ReadTestManifest = readTestManifest
var ReadTestManifestDocument = readTestManifestDocument
var ReadTestManifestForScenario = readTestManifestForScenario
var ReadTestPoolSnapshot = readTestPoolSnapshot
var ReadTestTriggerOutput = readTestTriggerOutput
var RealtimeMessageRequest = realtimeMessageRequest
var ReconcileMCPNativeSubmission = reconcileMCPNativeSubmission
var RecordMCPResourceWatchUpdate = recordMCPResourceWatchUpdate
var RecordMCPWatchUpdate = recordMCPWatchUpdate
var RecoverCustomerOperationWithReceipt = recoverCustomerOperationWithReceipt
var RecoverMCPNativeRelease = recoverMCPNativeRelease
var RecoveryReportDuration = recoveryReportDuration
var RecoveryTrendReceipt = recoveryTrendReceipt
var RedactErrorText = redactErrorText
var RedactTestDiagnosticText = redactTestDiagnosticText
var RehearsalTestGit = rehearsalTestGit
var RehearsalTestRepo = rehearsalTestRepo
var RejectDevTriggerAppSelection = rejectDevTriggerAppSelection
var ReleaseDiffLines = releaseDiffLines
var ReleaseSummaryLine = releaseSummaryLine
var ReleaseTimelineLines = releaseTimelineLines
var ReleasedUsage = releasedUsage
var RemoveTestFaultRules = removeTestFaultRules
var RenderAccountSLO = renderAccountSLO
var RenderAppBindingInventory = renderAppBindingInventory
var RenderAppMetrics = renderAppMetrics

// RenderServiceMap exposes the service-map text output.
var RenderServiceMap = renderServiceMap
var RenderAppSLO = renderAppSLO
var RenderApplyRescue = renderApplyRescue
var RenderStoredWorkflowLiveGate = renderStoredWorkflowLiveGate
var RenderBashCommand = renderBashCommand
var RenderBindingCreationFailure = renderBindingCreationFailure
var RenderBuildListRow = renderBuildListRow
var RenderColdBootBreakdown = renderColdBootBreakdown
var RenderContextSuffix = renderContextSuffix
var RenderCronRecentRuns = renderCronRecentRuns
var RenderCronState = renderCronState
var RenderCustomerOperation = renderCustomerOperation
var RenderDebugRegressionsTable = renderDebugRegressionsTable
var RenderDebugReplayComparison = renderDebugReplayComparison
var RenderDebugRequestMetadata = renderDebugRequestMetadata
var RenderDebugRequestsTable = renderDebugRequestsTable
var RenderDebugRunning = renderDebugRunning
var RenderDeployFailureIfPresent = renderDeployFailureIfPresent
var RenderDeployPreflight = renderDeployPreflight
var RenderDeploySummary = renderDeploySummary
var RenderDeploymentAccess = renderDeploymentAccess
var RenderDeploymentProgress = renderDeploymentProgress
var RenderDeploymentReleaseSummary = renderDeploymentReleaseSummary
var RenderDeploymentRow = renderDeploymentRow
var RenderDeploymentRowWide = renderDeploymentRowWide
var RenderDeploymentHeader = renderDeploymentHeader
var RenderDiffPreviewJSON = renderDiffPreviewJSON
var RenderDoctorHuman = renderDoctorHuman
var RenderDoctorImage = renderDoctorImage
var RenderEdgeAttackMode = renderEdgeAttackMode
var RenderEdgeProtection = renderEdgeProtection
var RenderEdgeRuleTrace = renderEdgeRuleTrace
var RenderEnvDiffCell = renderEnvDiffCell
var RenderEnvDiffTable = renderEnvDiffTable
var RenderFireNowStatus = renderFireNowStatus
var RenderFishCommand = renderFishCommand
var RenderGithubSetupWorkflow = renderGithubSetupWorkflow
var RenderGithubSnippet = renderGithubSnippet
var RenderHTTPLogQueryPageWarnings = renderHTTPLogQueryPageWarnings
var RenderInspectErrorsHuman = renderInspectErrorsHuman
var RenderInspectResources = renderInspectResources
var RenderInspectSummaryHuman = renderInspectSummaryHuman
var RenderInspectWorkerRecovery = renderInspectWorkerRecovery
var RenderInvoices = renderInvoices
var RenderMCPStarterCapabilities = renderMCPStarterCapabilities
var RenderManCommand = renderManCommand
var RenderMarkdownReference = renderMarkdownReference
var RenderMirrorRule = renderMirrorRule
var RenderPowershellCommand = renderPowershellCommand
var RenderPreviewRequirements = renderPreviewRequirements
var RenderPreviewRouteReport = renderPreviewRouteReport
var RenderProjectApplyResult = renderProjectApplyResult
var RenderQueueStatus = renderQueueStatus
var RenderRestoreBreakdown = renderRestoreBreakdown
var RenderRevision = renderRevision
var RenderRolloutDecisions = renderRolloutDecisions
var RenderRouteContract = renderRouteContract
var RenderRouteImpact = renderRouteImpact
var RenderRouteMonitorIncident = renderRouteMonitorIncident
var RenderRoutePolicyPlan = renderRoutePolicyPlan
var RenderRouteRemovalServerCheck = renderRouteRemovalServerCheck
var RenderSecretsByScope = renderSecretsByScope
var RenderSimpleAppPlan = renderSimpleAppPlan
var RenderSuccessfulDeployment = renderSuccessfulDeployment
var RenderSuccessfulDeploymentWithOptions = renderSuccessfulDeploymentWithOptions
var RenderSummaryHeader = renderSummaryHeader
var RenderTestComparisonMarkdown = renderTestComparisonMarkdown
var RenderTriggerClassSummary = renderTriggerClassSummary
var RenderUsageSummary = renderUsageSummary
var RenderWakeTimelinePage = renderWakeTimelinePage
var RenderWakeTimelinePageWithOptions = renderWakeTimelinePageWithOptions
var RenderWorkflowRunDiagnostics = renderWorkflowRunDiagnostics
var RenderZshCommand = renderZshCommand
var ReorderSecretsSetArgs = reorderSecretsSetArgs
var RepairEndpoint = repairEndpoint
var RequestLine = requestLine
var RequireAuthnStillOnHint = requireAuthnStillOnHint
var RequireNoAuth = requireNoAuth
var RequireTemplateSecretsBeforeFirstStart = requireTemplateSecretsBeforeFirstStart
var RequireTyped = requireTyped
var ResetJSONOut = resetJSONOut
var ResetJSONOutput = resetJSONOutput
var ResetNOColorCache = resetNOColorCache
var ResetCapabilityView = resetCapabilityView
var ResetStdoutTTYCache = resetStdoutTTYCache
var ResolveDeployReleaseAt = resolveDeployReleaseAt
var ResolveDeployRollbackOn5xx = resolveDeployRollbackOn5xx
var ResolveDeployShape = resolveDeployShape
var ResolveDeploySourceDir = resolveDeploySourceDir
var ResolveDeploySourceMode = resolveDeploySourceMode
var ResolveDeployedBy = resolveDeployedBy
var ResolveDeploymentArg = resolveDeploymentArg
var ResolveDeploymentRef = resolveDeploymentRef
var ResolveDevSetupSource = resolveDevSetupSource
var ResolveDevSourceConfig = resolveDevSourceConfig
var ResolveEnvironmentFlagOrContext = resolveEnvironmentFlagOrContext
var ResolveEnvironmentFromContext = resolveEnvironmentFromContext
var ResolveExecutionInput = resolveExecutionInput
var ResolveJSONFlag = resolveJSONFlag
var ResolveManagedPostgresDatabase = resolveManagedPostgresDatabase
var ResolvePayload = resolvePayload
var ResolveScanSource = resolveScanSource
var ResolveSetupEnvFile = resolveSetupEnvFile
var ResolveSimpleAppPlan = resolveSimpleAppPlan
var ResolveTestLoadConfig = resolveTestLoadConfig
var ResolveTestLoadRecovery = resolveTestLoadRecovery
var ResolveWorkspaceContext = resolveWorkspaceContext

// ResolveWorkspaceContextReason decides from the tree a deploy would ship:
// the working tree with worktree, committed HEAD with head.
func ResolveWorkspaceContextReason(repoRoot, sourceDir string, worktree, head bool) (string, string, bool, string, error) {
	return resolveWorkspaceContextReason(repoRoot, sourceDir, deployContextTree(repoRoot, sourceDir, worktree, head))
}

var ResolvedTempDir = resolvedTempDir
var ResponseActionID = responseActionID
var RestartAuthApp = restartAuthApp
var RestoreMCPNativeWeb = restoreMCPNativeWeb
var RetireMCPNativeWorker = retireMCPNativeWorker
var RetryCustomerOperationDelivery = retryCustomerOperationDelivery
var RetryStartingStage = retryStartingStage
var RouteAdviceContentLine = routeAdviceContentLine
var RouteHealthHistoryTransition = routeHealthHistoryTransition
var RouteHealthTargetError = routeHealthTargetError
var RouteMonitorCodeownersPatternMatches = routeMonitorCodeownersPatternMatches
var RouteMonitorMatchCodeowners = routeMonitorMatchCodeowners
var Run = run
var RunStoredWorkflowLiveGate = runStoredWorkflowLiveGate
var RunStoredWorkflowScenarios = runStoredWorkflowScenarios
var RunBindingCheckCLI = runBindingCheckCLI
var RunBindingVerificationJobs = runBindingVerificationJobs
var RunBucketCapacity = runBucketCapacity
var RunBucketCopySources = runBucketCopySources
var RunBucketDefaultEncryption = runBucketDefaultEncryption
var BucketStatusAlias = bucketStatusAlias
var RunBucketDelete = runBucketDelete
var RunBucketLifecycle = runBucketLifecycle
var RunBucketList = runBucketList
var RunBucketNotifications = runBucketNotifications
var RunBucketObjectLock = runBucketObjectLock
var RunBucketTransfer = runBucketTransfer
var RunBucketVersioning = runBucketVersioning
var RunCmdInit = runCmdInit
var RunCmdInitList = runCmdInitList
var RunCustomerOperationBindings = runCustomerOperationBindings
var RunCustomerOperationCommand = runCustomerOperationCommand
var RunCustomerOperationDelivery = runCustomerOperationDelivery
var RunCustomerOperationSignal = runCustomerOperationSignal
var RunDataAPIOnboarding = runDataAPIOnboarding
var RunDataAPIRelease = runDataAPIRelease
var RunDataAPIRotationOwnerCheck = runDataAPIRotationOwnerCheck
var RunDiff = runDiff
var RunDoctorChecks = runDoctorChecks
var RunDoctorImageChecks = runDoctorImageChecks
var RunDurableBindingVerification = runDurableBindingVerification
var RunExecutionWorkflow = runExecutionWorkflow
var RunGitCmd = runGitCmd
var RunHostingCI = runHostingCI
var RunImageRequested = runImageRequested
var RunInspectWatch = runInspectWatch
var RunIssueTicketSync = runIssueTicketSync
var RunIssueTickets = runIssueTickets
var RunLocalTest = runLocalTest
var RunLocalTestWithLoad = runLocalTestWithLoad
var RunLogs = runLogs
var RunMCPBrowserConnection = runMCPBrowserConnection

var RunToolBrowserConnectionForTest = runToolBrowserConnection
var ToolAuthorizationCallbackForTest = toolAuthorizationCallback
var RunToolConnectionBrowserCommandForTest = runMCPToolConnectionBrowser
var RunMCPCatalogMonitor = runMCPCatalogMonitor
var RunMCPNativeRelease = runMCPNativeRelease
var RunMCPNativeReleaseContext = runMCPNativeReleaseContext
var RunMCPNativeRetirement = runMCPNativeRetirement
var RunMCPResourceWatch = runMCPResourceWatch
var RunMCPWatch = runMCPWatch
var RunProductCommand = runProductCommand
var RunProjectEnvironmentPreflight = runProjectEnvironmentPreflight
var RunProjectEnvironmentQualificationProbe = runProjectEnvironmentQualificationProbe
var RunSimulatedTest = runSimulatedTest
var RunSweepTest = runSweepTest
var RunTestChaosSchedule = runTestChaosSchedule
var RunTestCommand = runTestCommand
var RunTestFaultMatrix = runTestFaultMatrix
var RunTestFaultMinimization = runTestFaultMinimization
var RunTestHTTPRequests = runTestHTTPRequests
var RunTestProfile = runTestProfile
var RunTestRehearsal = runTestRehearsal
var RunTestResilience = runTestResilience
var RunTestRestart = runTestRestart
var RunTestScenarioStepLoad = runTestScenarioStepLoad
var RunTestScenarioSteps = runTestScenarioSteps
var RunTestSuspendResume = runTestSuspendResume
var RunVersionProtection = runVersionProtection
var RunWithStderr = runWithStderr
var SafeMCPConnectionError = safeMCPConnectionError
var SafeReleaseHealthGate = safeReleaseHealthGate
var SameBindingDeployment = sameBindingDeployment
var SameSunsetContracts = sameSunsetContracts
var SaveStoredWorkflowLiveSuite = saveStoredWorkflowLiveSuite
var SaveStoredWorkflowResponsePlan = saveStoredWorkflowResponsePlan
var SaveBucketUploadCheckpoint = saveBucketUploadCheckpoint
var SaveCLIConfig = saveCLIConfig
var SaveDeployIssueSourceMapsRecovery = saveDeployIssueSourceMapsRecovery
var SaveExecutionArtifacts = saveExecutionArtifacts
var SaveMCPNativeState = saveMCPNativeState
var SaveProductPilot = saveProductPilot
var SaveProjectContext = saveProjectContext
var SaveResumableUploadState = saveResumableUploadState
var SaveStartSession = saveStartSession
var ScaffoldTestHTTPRequests = scaffoldTestHTTPRequests
var ScanEnvRefs = scanEnvRefs
var ScanSource = scanSource
var ScenarioInternalServiceURLs = scenarioInternalServiceURLs
var ScheduledReleaseLines = scheduledReleaseLines
var SecondsOrDurationFlag = secondsOrDurationFlag
var SecretAckProgress = secretAckProgress
var SecretAckProgressWithRestart = secretAckProgressWithRestart
var SecretCellSummary = secretCellSummary
var SecretRuntimeReloadLabel = secretRuntimeReloadLabel
var SecretUpdatedAtSuffix = secretUpdatedAtSuffix
var SelectDevCronPath = selectDevCronPath
var SelectProductOnboardingWorkspace = selectProductOnboardingWorkspace
var SelectTestReplay = selectTestReplay
var SelectedTestProfiles = selectedTestProfiles
var SendRollbackOverReleases = sendRollbackOverReleases
var ServeHostingLocal = serveHostingLocal
var ServeMCPHTTP = serveMCPHTTP
var ServiceExplainRequest = serviceExplainRequest
var SetDeploySecretsWithScope = setDeploySecretsWithScope
var SetFakeKeyring = setFakeKeyring
var SetPreviewTestAuth = setPreviewTestAuth
var SetProjectDeploySecrets = setProjectDeploySecrets
var SetupCLIRegression = setupCLIRegression
var SetupConnectionProfiles = setupConnectionProfiles
var SetupHermeticTokensEnv = setupHermeticTokensEnv
var SetupQueueWorkload = setupQueueWorkload
var SetupQueueWorkloadWithRetry = setupQueueWorkloadWithRetry
var SharedStoredWorkflowLiveSuiteID = sharedStoredWorkflowLiveSuiteID
var SimulateEdgeRuleRequest = simulateEdgeRuleRequest
var SortedKeys = sortedKeys
var SplitArgsForFlags = splitArgsForFlags
var SplitCSV = splitCSV
var SplitJobLogFollow = splitJobLogFollow
var StartContextSessionPath = startContextSessionPath
var StartMCPNativeRelease = startMCPNativeRelease
var StartRequiredConfiguration = startRequiredConfiguration
var StartSessionPath = startSessionPath
var StartTestAPI = startTestAPI
var StartTestEnvironment = startTestEnvironment
var StartTestLink = startTestLink
var StartTestRunner = startTestRunner
var StartTestSource = startTestSource
var StaticHostingManifest = staticHostingManifest
var StreamDevRuntimeLogs = streamDevRuntimeLogs
var SuggestCommand = suggestCommand
var SuggestSubcommand = suggestSubcommand
var SuiteTestScenario = suiteTestScenario
var SummarizeMCPDiagnostics = summarizeMCPDiagnostics
var SummarizeProjectApply = summarizeProjectApply
var SummarizeTestResilienceRecovery = summarizeTestResilienceRecovery
var SunsetUsage = sunsetUsage
var SwapIO = swapIO
var SwapStdout = swapStdout
var SweepRegressionGroups = sweepRegressionGroups
var SweepTestScenario = sweepTestScenario
var TarEntries = tarEntries
var TarballSHA256 = tarballSHA256
var TemplateAvailable = templateAvailable
var TerminalExitForDeploymentWithFailureContext = terminalExitForDeploymentWithFailureContext
var TestAPIKey = testAPIKey
var TestColdStartSpec = testColdStartSpec
var TestCommandBaseEnv = testCommandBaseEnv
var TestDiagnosticRedactions = testDiagnosticRedactions
var TestExecutionCompleteForEvidence = testExecutionCompleteForEvidence
var TestFaultCampaignDraw = testFaultCampaignDraw
var TestFaultReplayVersion = testFaultReplayVersion
var TestHTTPConsumerKeys = testHTTPConsumerKeys
var TestLatencySelection = testLatencySelection
var TestManagedWorkDigest = testManagedWorkDigest
var TestPhaseRecoveryEvidence = testPhaseRecoveryEvidence
var TestRecoveryDeadlineSpec = testRecoveryDeadlineSpec
var TestRehearsalLocalExecution = testRehearsalLocalExecution
var TestStabilityMinimizationOutcome = testStabilityMinimizationOutcome
var TestStabilitySelection = testStabilitySelection
var TokenFingerprint = tokenFingerprint
var TokenPath = tokenPath
var TopLevelUsage = topLevelUsage
var TopPatterns = topPatterns
var TraceHostMatches = traceHostMatches
var TraceLookupErrorLine = traceLookupErrorLine
var TraceMatchBreakdown = traceMatchBreakdown
var TraceMethodMatches = traceMethodMatches
var TraceWatchComplete = traceWatchComplete
var TraceWatchRetryDelay = traceWatchRetryDelay
var TraceWatchRetryableError = traceWatchRetryableError
var TransportProblem = transportProblem
var TriggerEnabledValue = triggerEnabledValue
var TriggerRecordLine = triggerRecordLine
var Trunc = trunc
var TruncateReason = truncateReason
var UnconsumedQueueWarning = unconsumedQueueWarning
var UnexcludedStoredWorkflowCoverageHints = unexcludedStoredWorkflowCoverageHints
var UpdateMCPWatchCapabilityNames = updateMCPWatchCapabilityNames
var UploadHostingFile = uploadHostingFile
var UploadStateDir = uploadStateDir
var UploadStatePath = uploadStatePath
var UpstreamsNotOffered = upstreamsNotOffered
var ValidAppWebhookEvent = validAppWebhookEvent
var ValidBindingPolicyServiceTargets = validBindingPolicyServiceTargets
var ValidBucketLifecycleArgs = validBucketLifecycleArgs
var ValidDataAPIConnectionBudget = validDataAPIConnectionBudget
var ValidDeploymentRef = validDeploymentRef
var ValidDeveloperID = validDeveloperID
var ValidGithubSetupBranch = validGithubSetupBranch
var ValidGithubSetupWorkflowName = validGithubSetupWorkflowName
var ValidLiveSuiteHash = validLiveSuiteHash
var ValidSecretClass = validSecretClass
var ValidTestCIEnvironment = validTestCIEnvironment
var ValidateStoredWorkflowCoverageExclusions = validateStoredWorkflowCoverageExclusions
var ValidateStoredWorkflowEventPlan = validateStoredWorkflowEventPlan
var ValidateStoredWorkflowRecoveryPlan = validateStoredWorkflowRecoveryPlan
var ValidateStoredWorkflowResponsePlan = validateStoredWorkflowResponsePlan
var ValidateCIUploadGrants = validateCIUploadGrants
var ValidateCLIInvestigation = validateCLIInvestigation
var ValidateCLILimit = validateCLILimit
var ValidateCLIOffset = validateCLIOffset
var ValidateCustomerOperationRecoveryDecision = validateCustomerOperationRecoveryDecision
var ValidateDataAPIConsumers = validateDataAPIConsumers
var ValidateDeployDiffManifest = validateDeployDiffManifest
var ValidateDeployIdempotencyKey = validateDeployIdempotencyKey
var ValidateDeploySourceSelection = validateDeploySourceSelection
var ValidateDeploymentReason = validateDeploymentReason
var ValidateExecutionWorkflowResult = validateExecutionWorkflowResult
var ValidateExplicitDockerfile = validateExplicitDockerfile
var ValidateExplicitDockerfileArchive = validateExplicitDockerfileArchive
var ValidateHTTPLogRoute = validateHTTPLogRoute
var ValidateLocalTestScenario = validateLocalTestScenario
var ValidateLocalTestURL = validateLocalTestURL
var ValidateMCPDeploySecretsFile = validateMCPDeploySecretsFile
var ValidateMCPDeploymentCatalogFleetPin = validateMCPDeploymentCatalogFleetPin
var ValidateMCPDeploymentCatalogPin = validateMCPDeploymentCatalogPin

func PinMCPCandidateWithSubmit(ctx context.Context, c *Client, key string, cfg mcphosting.Config, candidate api.DeploymentResponse, submit func([]string, map[string]string) (api.DeploymentResponse, error)) (api.DeploymentResponse, string, error) {
	return pinMCPCandidateRevisionWithSubmit(ctx, c, mcpDeployOptions{slug: "demo", timeout: 60, idempotencyKey: key}, "/src", cfg, candidate, submit)
}

var ValidatePreviewArchivePlanLimit = validatePreviewArchivePlanLimit
var ValidateProductCustomerActivity = validateProductCustomerActivity
var ValidateProductOnboardingPreview = validateProductOnboardingPreview
var ValidateProjectEnvironmentFlag = validateProjectEnvironmentFlag
var ValidateProjectManifestConfig = validateProjectManifestConfig
var ValidateQueueRetryPolicy = validateQueueRetryPolicy
var ValidateRepoDeployFlags = validateRepoDeployFlags
var ValidateRollbackFlags = validateRollbackFlags
var ValidateRouteHealthReleasePreviewInput = validateRouteHealthReleasePreviewInput
var ValidateRouteHealthReport = validateRouteHealthReport
var ValidateScenarioChaos = validateScenarioChaos
var ValidateScenarioTCPService = validateScenarioTCPService
var ValidateSingleAppManifestTargets = validateSingleAppManifestTargets
var ValidateTemplateSecrets = validateTemplateSecrets
var ValidateTestActions = validateTestActions
var ValidateTestCampaignCoverageBatch = validateTestCampaignCoverageBatch
var ValidateTestCampaignGuidanceOptions = validateTestCampaignGuidanceOptions
var ValidateTestCommandExecution = validateTestCommandExecution
var ValidateTestHTTPRequests = validateTestHTTPRequests
var ValidateTestAssertionProfiles = validateTestAssertionProfiles
var PrepareTestRequestDeadline = prepareTestRequestDeadline
var ExpandTestAssertionProfiles = expandTestAssertionProfiles
var TestHTTPDataFields = testHTTPDataFields
var ValidateTestLoadRecovery = validateTestLoadRecovery
var ValidateTestLoadSpec = validateTestLoadSpec
var ValidateTestManagedWork = validateTestManagedWork
var ValidateTestMinimization = validateTestMinimization
var ValidateTestResilience = validateTestResilience
var ValidateTestScenarioSteps = validateTestScenarioSteps
var ValidateTestSecrets = validateTestSecrets
var ValidateTestSuiteOptions = validateTestSuiteOptions
var VerificationForeignKeys = verificationForeignKeys
var VerifyDataAPIServingContract = verifyDataAPIServingContract
var VerifyDeployIssueBundle = verifyDeployIssueBundle
var VerifyProductPilot = verifyProductPilot
var VerifyTestProfile = verifyTestProfile
var VerifyTestServiceHot = verifyTestServiceHot
var VerifyTestServiceTCPHot = verifyTestServiceTCPHot
var VerifyTestServiceWake = verifyTestServiceWake
var VisibleCliCommands = visibleCliCommands
var PublicReferenceCommands = publicReferenceCommands
var WaitAlertRollback = waitAlertRollback
var WaitCommitEvent = waitCommitEvent
var WaitDataAPIRefresh = waitDataAPIRefresh
var WaitDataAPIRestart = waitDataAPIRestart
var WaitDataPipeline = waitDataPipeline
var WaitPipelineConnectionCheck = waitConnectionCheck
var WaitForAppWake = waitForAppWake
var WaitForDeploymentReceiptUntil = waitForDeploymentReceiptUntil
var WaitForDeploymentRollout = waitForDeploymentRollout
var WaitForProjectApply = waitForProjectApply
var WaitForProjectEnvironmentClone = waitForProjectEnvironmentClone
var WaitForProjectEnvironmentPromotion = waitForProjectEnvironmentPromotion
var WaitForSecretApplicationAck = waitForSecretApplicationAck
var WaitForSecretApplicationAckAfterRestart = waitForSecretApplicationAckAfterRestart
var WaitForSecretRevocationAck = waitForSecretRevocationAck
var WaitForTestDelivery = waitForTestDelivery
var WaitForTestInvocation = waitForTestInvocation
var WaitForTestOutputs = waitForTestOutputs
var WaitMCPAccessConvergence = waitMCPAccessConvergence
var WakeRecommendationTier = wakeRecommendationTier
var WarnClientScopedProtectiveRule = warnClientScopedProtectiveRule
var WarnRootOnlyProtectiveRule = warnRootOnlyProtectiveRule
var DefaultEdgeRuleMatchPath = defaultEdgeRuleMatchPath
var WarnSingleHostProtectiveRule = warnSingleHostProtectiveRule
var WarnTemplatedEdgeRulePaths = warnTemplatedEdgeRulePaths
var WatchDevPatch = watchDevPatch
var FinishDeferredDevSync = finishDeferredDevSync
var WithBindingProgress = withBindingProgress
var WithBindingVerificationBudget = withBindingVerificationBudget
var WithBindingVerificationSlot = withBindingVerificationSlot
var WithCwd = withCwd
var WithFixtureCronStaging = withFixtureCronStaging
var WithFixturePolicyStaging = withFixturePolicyStaging
var WithGetErr = withGetErr
var WithRecorder = withRecorder
var WithResetJSONOutput = withResetJSONOutput
var WithTTYForTest = withTTYForTest
var WithoutConfirmationFlags = withoutConfirmationFlags
var WorkerReplicaBounds = workerReplicaBounds
var WriteStoredWorkflowFixture = writeStoredWorkflowFixture
var WriteStoredWorkflowHealthRunHints = writeStoredWorkflowHealthRunHints
var WriteStoredWorkflowRecoveryAttention = writeStoredWorkflowRecoveryAttention
var WriteStoredWorkflowRecoveryPlanFile = writeStoredWorkflowRecoveryPlanFile
var WriteStoredWorkflowSuggestions = writeStoredWorkflowSuggestions
var WriteCustomerOperationBindings = writeCustomerOperationBindings
var WriteDataAPIApprovalFixture = writeDataAPIApprovalFixture
var WriteDataAPIConnectionBudget = writeDataAPIConnectionBudget
var WriteDataAPIDatabaseTimings = writeDataAPIDatabaseTimings
var WriteDataAPIDiagnostics = writeDataAPIDiagnostics
var WriteDataAPIPool = writeDataAPIPool
var WriteDataAPITimings = writeDataAPITimings
var WriteDataAPITypes = writeDataAPITypes
var WriteDeploySourceArchive = writeDeploySourceArchive
var WriteFile = writeFile
var WriteGitOpsPlanMarkdown = writeGitOpsPlanMarkdown
var WriteJSON = writeJSON
var WriteJSONProblem = writeJSONProblem
var WriteJSONTest = writeJSONTest
var WriteJSONTestStatus = writeJSONTestStatus
var WriteLegacyToken = writeLegacyToken
var WriteMCPAdmissionExplanation = writeMCPAdmissionExplanation
var WriteMCPCatalogMonitor = writeMCPCatalogMonitor
var WriteMCPConnection = writeMCPConnection
var WriteMCPConnectionHealth = writeMCPConnectionHealth
var WriteMCPContract = writeMCPContract
var WriteMCPOAuthRetirement = writeMCPOAuthRetirement
var WriteMailDryRun = writeMailDryRun
var WriteManifest = writeManifest

func WriteNDJSON[T any](p0 []T) error { return writeNDJSON[T](p0) }

var WritePreviewPolicySnapshotTest = writePreviewPolicySnapshotTest
var WritePreviewReportDoc = writePreviewReportDoc
var WriteProductOutput = writeProductOutput
var WriteRoutePolicyPlan = writeRoutePolicyPlan
var WriteSuiteTestFile = writeSuiteTestFile
var WriteSuiteTestManifest = writeSuiteTestManifest
var WriteTestComparisonHTML = writeTestComparisonHTML
var WriteTestComparisonMarkdown = writeTestComparisonMarkdown
var WriteTestHTMLReport = writeTestHTMLReport
var WriteTestJUnit = writeTestJUnit
var WriteTestReport = writeTestReport
var WriteWaitedDeploymentReceiptUntil = writeWaitedDeploymentReceiptUntil
var WriteWaitedDeploymentReceiptUntilWithOptions = writeWaitedDeploymentReceiptUntilWithOptions
var ZeroConfigDeployServer = zeroConfigDeployServer

// Data API runtime served by the control plane (ADR-20261031095954).
var ServedDataAPIRuntime = servedDataAPIRuntime
var UploadedSourceFiles = uploadedSourceFiles
var UploadedServerHas = uploadedServerHas
var ReadDataAPIRuntimeArchive = readDataAPIRuntimeArchive
var AttachDataAPIRuntime = attachDataAPIRuntime
var CurrentDataAPIRuntimeVersion = currentDataAPIRuntimeVersion

type DataAPIHealthProbe = dataAPIHealthProbe

const ServedRuntimeVersion = servedRuntimeVersion

// Members of appTaskStreamSession
func (r *appTaskStreamSession) Stdout() **io.PipeReader { return &r.stdout }

// Members of artifactOutputDirectory
func (d *artifactOutputDirectory) Planned() []string                { return d.planned }
func (d *artifactOutputDirectory) Root() *os.Root                   { return d.root }
func (d *artifactOutputDirectory) Create(ctx context.Context) error { return d.create(ctx) }

// OpenArtifactTaskDirectory opens a server-named task directory under parent.
var OpenArtifactTaskDirectory = openArtifactTaskDirectory

// CLIUserAgent is the User-Agent the CLI's API client sends.
var CLIUserAgent = cliUserAgent

// ResetServerNotices forgets the notices already printed this run.
func ResetServerNotices() {
	serverNotices.Lock()
	defer serverNotices.Unlock()
	serverNotices.seen = nil
}

// Members of authCreateOptions
func (r *authCreateOptions) EmailAPIKey() *string   { return &r.emailAPIKey }
func (r *authCreateOptions) EmailFrom() *string     { return &r.emailFrom }
func (r *authCreateOptions) EmailProvider() *string { return &r.emailProvider }

// Members of storedWorkflowLiveSuiteOptions
func (r *storedWorkflowLiveSuiteOptions) Interval() *time.Duration           { return &r.interval }
func (r *storedWorkflowLiveSuiteOptions) Path() *string                      { return &r.path }
func (r *storedWorkflowLiveSuiteOptions) Plan() *storedWorkflowLiveSuitePlan { return &r.plan }

// Members of storedWorkflowScenarioCoverage
func (r storedWorkflowScenarioCoverage) XHints(p0 api.WorkflowSpec) []storedWorkflowCoverageHint {
	return r.hints(p0)
}

// Members of bindingPolicySnapshotClient
func (r *bindingPolicySnapshotClient) Inventory() *api.AppBindingInventory { return &r.inventory }

// Members of bindingProgressView
func (r *bindingProgressView) Observe(p0 bindingrun.Progress) error            { return r.observe(p0) }
func (r *bindingProgressView) Recovery() *bindingRecoverySummary               { return r.recovery() }
func (r *bindingProgressView) RenderReport(p0 bindingPolicyVerificationReport) { r.renderReport(p0) }

// Members of bucketResumeFixture
func (r *bucketResumeFixture) ApiBase() *string                    { return &r.apiBase }
func (r *bucketResumeFixture) Creates() *int                       { return &r.creates }
func (r *bucketResumeFixture) FailSign() *int                      { return &r.failSign }
func (r *bucketResumeFixture) Reads() *int                         { return &r.reads }
func (r *bucketResumeFixture) Session() *api.ObjectMultipartUpload { return &r.session }
func (r *bucketResumeFixture) Url() *string                        { return &r.url }

// Members of bucketTransferOptions
func (r *bucketTransferOptions) Path() *string     { return &r.path }
func (r *bucketTransferOptions) ResumeID() *string { return &r.resumeID }

// Members of cliSessionMetadata
func (r cliSessionMetadata) Matches(p0 string) bool { return r.matches(p0) }

// Members of customerOperationBindingsCommand
func (r *customerOperationBindingsCommand) Check() *bool    { return &r.check }
func (r *customerOperationBindingsCommand) Output() *string { return &r.output }

// Members of customerOperationCommand
func (r *customerOperationCommand) App() *string                            { return &r.app }
func (r *customerOperationCommand) Id() *string                             { return &r.id }
func (r *customerOperationCommand) Receipt() *string                        { return &r.receipt }
func (r *customerOperationCommand) Recovery() *api.OperationRecoveryRequest { return &r.recovery }

// Members of customerOperationDeliveryCommand
func (r *customerOperationDeliveryCommand) App() *string     { return &r.app }
func (r *customerOperationDeliveryCommand) Id() *string      { return &r.id }
func (r *customerOperationDeliveryCommand) Limit() *int      { return &r.limit }
func (r *customerOperationDeliveryCommand) Receipt() *string { return &r.receipt }
func (r *customerOperationDeliveryCommand) Request() *api.OperationDeliveryRetryRequest {
	return &r.request
}
func (r *customerOperationDeliveryCommand) Verb() *string { return &r.verb }

// Members of customerOperationSignalCommand
func (r *customerOperationSignalCommand) Subject() *string { return &r.subject }

// Members of dataAPIDiagnosticsCollector
func (r *dataAPIDiagnosticsCollector) Report() *dataAPIDiagnosticsReport { return &r.report }
func (r *dataAPIDiagnosticsCollector) Finish()                           { r.finish() }
func (r *dataAPIDiagnosticsCollector) Observe(p0 string) bool            { return r.observe(p0) }

// Members of dataAPIRotationOutput
func (r *dataAPIRotationOutput) Buffer() *bytes.Buffer { return &r.buffer }
func (r *dataAPIRotationOutput) Exceeded() *bool       { return &r.exceeded }

// Members of deployExecution
func (r *deployExecution) OnTerminal() *func(api.DeploymentResponse) int { return &r.onTerminal }
func (r *deployExecution) ResolveApp() *func(context.Context, *Client, api.CreateAppRequest) (api.AppResponse, error) {
	return &r.resolveApp
}
func (r deployExecution) NotifyQueued(p0 api.DeploymentResponse) { r.notifyQueued(p0) }

// Members of devEnvSyncState
func (r *devEnvSyncState) Synced() *bool { return &r.synced }
func (r *devEnvSyncState) Sync(p0 context.Context, p1 devSecretClient, p2 string, p3 string) (devEnvSyncReport, error) {
	return r.sync(p0, p1, p2, p3)
}
func (r *devEnvSyncState) SyncServiceOverrides(p0 context.Context, p1 devSecretClient, p2 string, p3 string) (devEnvSyncReport, error) {
	return r.syncServiceOverrides(p0, p1, p2, p3)
}

// Members of devPatchExplainer
func (r *devPatchExplainer) Explain(p0 *api.DevPatchPreview) string { return r.explain(p0) }

// Members of devPhaseTracker
func (r *devPhaseTracker) Mu() *sync.Mutex       { return &r.mu }
func (r *devPhaseTracker) StartedAt() *time.Time { return &r.startedAt }
func (r *devPhaseTracker) FinishRouteSwitch()    { r.finishRouteSwitch() }
func (r *devPhaseTracker) ObserveStage(p0 string, p1 string, p2 int64, p3 string) {
	r.observeStage(p0, p1, p2, p3)
}
func (r *devPhaseTracker) ObserveStageState(p0 json.RawMessage) { r.observeStageState(p0) }
func (r *devPhaseTracker) PatchDelivered(p0 api.DevPatchStatusResponse, p1 time.Time) time.Duration {
	return r.patchDelivered(p0, p1)
}
func (r *devPhaseTracker) Receipt(p0 string) devSyncReceipt { return r.receipt(p0) }
func (r *devPhaseTracker) ReconcileFinalStages(p0 context.Context, p1 *api.Client) {
	r.reconcileFinalStages(p0, p1)
}
func (r *devPhaseTracker) Render(p0 io.Writer)                   { r.render(p0) }
func (r *devPhaseTracker) SetDeploymentID(p0 string)             { r.setDeploymentID(p0) }
func (r *devPhaseTracker) SetDevPatch(p0 *api.DevPatchPreview)   { r.setDevPatch(p0) }
func (r *devPhaseTracker) SourceSync(p0 time.Duration, p1 error) { r.sourceSync(p0, p1) }

// Members of devSourceConfig
func (r *devSourceConfig) Shape() *shape { return &r.shape }

// Members of explainCollector
func (r *explainCollector) ErrorCount() *int          { return &r.errorCount }
func (r *explainCollector) InfoCount() *int           { return &r.infoCount }
func (r *explainCollector) LastError() *string        { return &r.lastError }
func (r *explainCollector) Patterns() *map[string]int { return &r.patterns }
func (r *explainCollector) WarnCount() *int           { return &r.warnCount }
func (r *explainCollector) Flush(p0 io.Writer)        { r.flush(p0) }
func (r *explainCollector) Observe(p0 string)         { r.observe(p0) }

// Members of fakeAPI
func (r *fakeAPI) SawBody() *[]byte        { return &r.sawBody }
func (r *fakeAPI) SawHeader() *http.Header { return &r.sawHeader }
func (r *fakeAPI) SawMethod() *string      { return &r.sawMethod }
func (r *fakeAPI) SawPath() *string        { return &r.sawPath }
func (r *fakeAPI) SawQuery() *string       { return &r.sawQuery }
func (r *fakeAPI) Srv() **httptest.Server  { return &r.srv }

// Members of imageRunOptions
func (r *imageRunOptions) Request() *api.CreateImageRunRequest { return &r.request }

// Members of loadedStoredWorkflowScenario
func (r *loadedStoredWorkflowScenario) CoverageExclusions() *[]storedWorkflowCoverageExclusion {
	return &r.coverageExclusions
}
func (r *loadedStoredWorkflowScenario) Outputs() *map[string]json.RawMessage { return &r.outputs }
func (r *loadedStoredWorkflowScenario) Request() *api.SimulateStoredWorkflowRequest {
	return &r.request
}
func (r *loadedStoredWorkflowScenario) Scenario() *storedWorkflowScenario { return &r.scenario }

// Members of manifestCronTransaction
func (r *manifestCronTransaction) Steps() *[]manifestCronRollbackStep { return &r.steps }

// Members of mcpReleaseAuthorization
func (r *mcpReleaseAuthorization) InsufficientToken() *string { return &r.insufficientToken }

// Members of mcpReleaseRole
func (r *mcpReleaseRole) Token() *string { return &r.token }

// Members of mcpServeOptions
func (r *mcpServeOptions) AllowToolInvocation() *bool { return &r.allowToolInvocation }
func (r *mcpServeOptions) AllowWrites() *bool         { return &r.allowWrites }
func (r *mcpServeOptions) Onboarding() *string        { return &r.onboarding }
func (r *mcpServeOptions) Redirects() *[]string       { return &r.redirects }
func (r *mcpServeOptions) StateFile() *string         { return &r.stateFile }

// Members of mcpTasksStatusResult
func (r *mcpTasksStatusResult) Diagnose() { r.diagnose() }

// Members of menuAction
func (r *menuAction) Args() *[]string { return &r.args }
func (r *menuAction) Label() *string  { return &r.label }

// Members of productCommand
func (r *productCommand) App() *string         { return &r.app }
func (r *productCommand) EmailStatus() *string { return &r.emailStatus }
func (r *productCommand) File() *string        { return &r.file }
func (r *productCommand) HelpID() *string      { return &r.helpID }
func (r *productCommand) Output() *string      { return &r.output }
func (r *productCommand) Tenant() *string      { return &r.tenant }
func (r *productCommand) Verb() *string        { return &r.verb }

// Members of productPilotFixture
func (r *productPilotFixture) Catalog() *api.ProductCatalog             { return &r.catalog }
func (r *productPilotFixture) Doctor() *api.ProductDoctorReport         { return &r.doctor }
func (r *productPilotFixture) Help() *map[string]api.ProductHelpRequest { return &r.help }
func (r *productPilotFixture) Operations() *map[string]api.ProductOperationExplanation {
	return &r.operations
}
func (r *productPilotFixture) Reads() *int                                   { return &r.reads }
func (r *productPilotFixture) Reviews() *map[string]api.ProductBillingReview { return &r.reviews }

// Members of projectApplyStatus
func (r *projectApplyStatus) AppsReconciled() *int { return &r.appsReconciled }
func (r *projectApplyStatus) BuildsFailed() *int   { return &r.buildsFailed }
func (r *projectApplyStatus) BuildsQueued() *int   { return &r.buildsQueued }
func (r *projectApplyStatus) ImagesQueued() *int   { return &r.imagesQueued }
func (r *projectApplyStatus) MissingBuilds() *int  { return &r.missingBuilds }
func (r *projectApplyStatus) Unchanged() *int      { return &r.unchanged }

// Members of recordedLauncher
func (r *recordedLauncher) Err() *error     { return &r.err }
func (r *recordedLauncher) Urls() *[]string { return &r.urls }

// Members of rolloutHeldNotice
func (r *rolloutHeldNotice) MaybeWarn(p0 io.Writer, p1 api.DeploymentResponse, p2 time.Time) {
	r.maybeWarn(p0, p1, p2)
}

// Members of routeMonitorCLIOptions
func (r *routeMonitorCLIOptions) Action() *string          { return &r.action }
func (r *routeMonitorCLIOptions) CustomerDetails() *bool   { return &r.customerDetails }
func (r *routeMonitorCLIOptions) CustomerGroupBy() *string { return &r.customerGroupBy }
func (r *routeMonitorCLIOptions) Fail() *bool              { return &r.fail }
func (r *routeMonitorCLIOptions) SourceImpact() *string    { return &r.sourceImpact }

// Members of routeMonitorCodeownersRule
func (r *routeMonitorCodeownersRule) Line() *int        { return &r.line }
func (r *routeMonitorCodeownersRule) Owners() *[]string { return &r.owners }

// Members of startFileEdit
func (r *startFileEdit) Path() *string { return &r.path }

// Members of startPrompt
func (r *startPrompt) Reader() **bufio.Reader { return &r.reader }
func (r *startPrompt) Writer() *io.Writer     { return &r.writer }
func (r *startPrompt) Confirm(ctx context.Context, label string) (bool, error) {
	return r.confirm(ctx, label)
}
func (r *startPrompt) ConfirmYes(ctx context.Context, label string) (bool, error) {
	return r.confirmYes(ctx, label)
}

// Members of startRequestResult
func (r *startRequestResult) Path() *string             { return &r.path }
func (r *startRequestResult) ServedDeployment() *string { return &r.servedDeployment }

// Members of startRunner
func (r *startRunner) Account() *api.AccountResponse { return &r.account }
func (r *startRunner) Client() **Client              { return &r.client }
func (r *startRunner) Ctx() *context.Context         { return &r.ctx }
func (r *startRunner) Deploy() *func(context.Context, []string, bool, ...deployExecution) int {
	return &r.deploy
}
func (r *startRunner) LastRequest() **startRequestResult     { return &r.lastRequest }
func (r *startRunner) Prompt() *startPrompt                  { return &r.prompt }
func (r *startRunner) RequestFailure() **startRequestFailure { return &r.requestFailure }
func (r *startRunner) SecretsFile() *string                  { return &r.secretsFile }
func (r *startRunner) Session() *startSession                { return &r.session }
func (r *startRunner) StatePath() *string                    { return &r.statePath }
func (r *startRunner) Authenticate() int                     { return r.authenticate() }
func (r *startRunner) CheckFirstResponse(p0 string, p1 string) int {
	return r.checkFirstResponse(p0, p1)
}
func (r *startRunner) ChooseRecovery() (bool, bool, error) { return r.chooseRecovery() }
func (r *startRunner) Finish()                             { r.finish() }
func (r *startRunner) PrintRecovery()                      { r.printRecovery() }
func (r *startRunner) PublicRequest(p0 api.AppResponse, p1 string) int {
	return r.publicRequest(p0, p1)
}
func (r *startRunner) RememberApp() error { return r.rememberApp() }
func (r *startRunner) RenderLaunchDefaults(p0 doctorReport, p1 api.AppResponse, p2 bool) {
	r.renderLaunchDefaults(p0, p1, p2)
}
func (r *startRunner) ResolveDeployTarget(p0 context.Context, p1 *Client, p2 api.CreateAppRequest) (api.AppResponse, error) {
	return r.resolveDeployTarget(p0, p1, p2)
}
func (r *startRunner) ResolveLaunchTarget() error                   { return r.resolveLaunchTarget() }
func (r *startRunner) ReviewAndDeploy() (int, bool)                 { return r.reviewAndDeploy() }
func (r *startRunner) SelectSource() int                            { return r.selectSource() }
func (r *startRunner) SendTestRequest(p0 string, p1 string) int     { return r.sendTestRequest(p0, p1) }
func (r *startRunner) ShowRequestLogs(p0 int)                       { r.showRequestLogs(p0) }
func (r *startRunner) SourcePreflight() (doctorReport, bool, error) { return r.sourcePreflight() }
func (r *startRunner) StarterWalkthrough() int                      { return r.starterWalkthrough() }

// Members of testBehaviorSession
func (r *testBehaviorSession) Dir() *string              { return &r.dir }
func (r *testBehaviorSession) OutputPath() *string       { return &r.outputPath }
func (r *testBehaviorSession) Finish(p0 *testRunReceipt) { r.finish(p0) }

// Members of testChaosSpec
func (r *testChaosSpec) Plan() (chaos.Plan, error) { return r.plan() }

// Members of testLoadPhaseClock
func (r *testLoadPhaseClock) Windows() *[]testLoadPhaseWindow   { return &r.windows }
func (r *testLoadPhaseClock) PhaseAt(p0 time.Time) int          { return r.phaseAt(p0) }
func (r *testLoadPhaseClock) Window(p0 int) testLoadPhaseWindow { return r.window(p0) }

// Members of testPostgresSession
func (r *testPostgresSession) Cleanup(p0 context.Context, p1 testPostgresClient, p2 *testRunReceipt) {
	r.cleanup(p0, p1, p2)
}
func (r *testPostgresSession) Create(p0 context.Context, p1 testPostgresClient, p2 *testRunReceipt, p3 string) error {
	return r.create(p0, p1, p2, p3)
}

// Members of testProxyRecorder
func (r *testProxyRecorder) Reset()                     { r.reset() }
func (r *testProxyRecorder) Snapshot() testWakeEvidence { return r.snapshot() }

// Members of testRehearsalSession
func (r *testRehearsalSession) Root() *string { return &r.root }

// Members of validateParamFlags
func (r validateParamFlags) Resolve(p0 map[string]bool) (*api.EdgeRuleValidateParameters, error) {
	return r.resolve(p0)
}

// Members of zeroConfigStubServer
func (r *zeroConfigStubServer) GotCalls() *map[string]int { return &r.gotCalls }
func (r *zeroConfigStubServer) Srv() **httptest.Server    { return &r.srv }

// Realtime preview (ADR-20261019102657).
type ManifestCronTransaction = manifestCronTransaction

var ErrRealtimeManifestHidden = errRealtimeManifestHidden
var ReconcileManifestRealtimeEndpoints = reconcileManifestRealtimeEndpoints
var RealtimePreviewVerbs = realtimePreviewVerbs
var RealtimeUsage = realtimeUsage
var RealtimePublishUsage = realtimePublishUsage

var PrintEdgeRuleEvents = printEdgeRuleEvents
var ResolveArchiveInstance = resolveArchiveInstance
var RequestLatencyMS = requestLatencyMS
var SafeDeployWaitTimeoutSeconds = safeDeployWaitTimeoutSeconds
var InferLatestWorkflowName = inferLatestWorkflowName
var CompactUsage = compactUsage

const UsageSynopsisLimit = usageSynopsisLimit

var InvokedCommandPath = &invokedCommandPath

var AppArgumentEnabled = &appArgumentEnabled
var AppArgumentAsFlag = appArgumentAsFlag
var AppFlagAsArgument = appFlagAsArgument

// AppArgumentPaths lists every visible command path whose leaf takes the
// app as its first argument.
func AppArgumentPaths() [][]string {
	var paths [][]string
	var walk func(path []string, flags []cliFlag, positionals []string, children []cliSub)
	walk = func(path []string, flags []cliFlag, positionals []string, children []cliSub) {
		if takesAppArgument(flags, positionals, children, "app") {
			paths = append(paths, append([]string{}, path...))
		}
		for _, child := range children {
			if child.SubcommandsAfterPositionals {
				continue
			}
			walk(append(append([]string{}, path...), child.Name), child.Flags, child.Positionals, child.Subcommands)
		}
	}
	for _, command := range visibleCliCommands() {
		if command.SubcommandsAfterPositionals {
			continue
		}
		walk([]string{command.Name}, command.Flags, command.Positionals, command.Subcommands)
	}
	return paths
}

// Capability hiding (ADR-20261023115741).
var CliPathCapability = cliPathCapability
var CommandCapabilityPath = commandCapabilityPath

var ValidateMCPProtocolResponseHeaders = validateMCPProtocolResponseHeaders

// SavedDeployRecovery returns the resume command and hint a saved deploy
// session with no staged settings offers after the given submission outcome.
func SavedDeployRecovery(outcome string) (resume, hint string) {
	s := &savedDeploySession{record: deploySessionRecord{ID: "sess-1", AppSlug: "probe-app", Outcome: deploySubmissionOutcome(outcome)}}
	r := s.recovery("submission")
	return r.ResumeCommand, r.Hint
}

var RoutesAppFlagAsPositional = routesAppFlagAsPositional

var RenderProject = renderProject
var InspectAttention = inspectAttention
var SelectInspectDeployments = selectInspectDeployments
var RemoveCLITestSandbox = removeCLITestSandbox
var WorkerScalingMetrics = workerScalingMetrics
var WorkerMetricLabel = workerMetricLabel

// ADR-20261025110000: registry credentials from a Docker config.
type DockerCredentialHelper = dockerCredentialHelper

func DockerConfigRegistryAuth(ctx context.Context, configPath, image string, helper DockerCredentialHelper) (*api.ImageRunRegistryAuth, error) {
	return dockerConfigRegistryAuth(ctx, configPath, image, helper)
}

var RenderRouteMonitorReport = renderRouteMonitorReport

var PreviewCompanionsFromManifest = previewCompanionsFromManifest

// FormatStageDuration is the live ticker's stage duration cell.
var FormatStageDuration = formatStageDuration

// ADR-20261026145832: the new command tree.
type CliTreeAlias = cliTreeAlias
type CliTreeNoun = cliTreeNoun

var CliTreeAliases = &cliTreeAliases
var CliTreeNouns = &cliTreeNouns
var RewriteCliTree = rewriteCliTree
var OldPathDepth = oldPathDepth

// CliPathHasVerbs reports whether words name a manifest command or verb, and
// whether it has verbs of its own that a tree noun can expose.
func CliPathHasVerbs(words []string) (exists, hasVerbs bool) {
	node, ok := lookupCliPath(words)
	return ok, ok && len(node.Subs) > 0 && !node.AfterPositionals
}

// CliTreeChild is one verb under a manifest path, for the tree's verb baseline.
type CliTreeChild struct {
	Name     string
	HasVerbs bool
}

var CliTreeStandardVerb = cliTreeStandardVerb

// CliPathChildren lists the verbs under words, or nil for a slug-first family.
func CliPathChildren(words []string) []CliTreeChild {
	node, ok := lookupCliPath(words)
	if !ok || node.AfterPositionals {
		return nil
	}
	children := make([]CliTreeChild, 0, len(node.Subs))
	for _, sub := range node.Subs {
		children = append(children, CliTreeChild{Name: sub.Name, HasVerbs: len(sub.Subcommands) > 0 && !sub.SubcommandsAfterPositionals})
	}
	return children
}

// gregale logs --project (ADR-20261026190244).
var RunProjectLogs = runProjectLogs
var ProjectLogJSON = projectLogJSON

// dev --all and gregale dev Go commands (ADR-20261026120412).
var DiscoverDevAllApps = discoverDevAllApps

func DevAllChildArgs(app devAllApp, stop bool) []string {
	return devAllOptions{stop: stop}.childArgs(app, false)
}
func DevAllSourceLabel(app devAllApp) string { return app.sourceLabel() }
func CheckDevGoCommand(dir string, function bool, command string) error {
	config := devSourceConfig{shape: shapeApp}
	if function {
		config.shape = shapeFunction
	}
	return checkDevGoCommand(dir, config, command)
}
func DevGoCommandRequest(command string) api.UpsertDevSessionRequest {
	return withDevGoCommand(devSourceConfig{shape: shapeApp}.sessionRequest("", false, "", 0, nil), command)
}

var NormalizeTreeFlags = normalizeTreeFlags

var CronRunCommand = cronRunCommand

// Bucket CORS CLI (ADR-20261025202805).
var (
	DecodeBucketCORSFile = decodeBucketCORSFile
	ValidBucketCORSArgs  = validBucketCORSArgs
	RunBucketCORS        = runBucketCORS
)

// TierDiscoverableFlags is discovery's flag filter under the logged-out tier
// view (preview and internal capabilities hidden).
func TierDiscoverableFlags(names, capabilities []string) []string {
	flags := make([]cliFlag, len(names))
	for i := range names {
		flags[i] = cliFlag{Name: names[i], Capability: capabilities[i]}
	}
	var kept []string
	for _, flag := range (capabilityView{tier: true}).discoverableFlags(flags) {
		kept = append(kept, flag.Name)
	}
	return kept
}

// BindExclusiveLock binds a created cron or broker trigger to its lock.
var BindExclusiveLock = bindExclusiveLock

type ExclusiveLock = exclusiveLock

// CLI audit 2026-10-31 (auth and credentials).
var CmdLogin = cmdLogin
var WithEntry = withEntry

// GitHub connector commands (ADR-20261101160300).
var CmdGithub = cmdGithub
var CmdGithubSetup = cmdGithubSetup

// Manifest cron staging stays with production deploys (ADR-20261104120000).
var DeployStagesManifestCrons = deployStagesManifestCrons
var CronStagingEnvironment = cronStagingEnvironment

// DebugWatchPoll runs one debug requests watch poll against baseURL and
// returns the request IDs it read and whether it reported a gap.
func DebugWatchPoll(baseURL, slug string, limit int, seen map[string]string, follow bool) ([]string, bool, error) {
	resp, missed, err := debugWatchPoll(context.Background(), api.NewClient(baseURL, "fp_test"), slug, api.DebugTelemetryListOptions{Limit: limit}, seen, follow)
	ids := make([]string, 0, len(resp.Requests))
	for _, request := range resp.Requests {
		ids = append(ids, request.ID)
	}
	return ids, missed, err
}

const DebugWatchMaxPages = debugWatchMaxPages

var McpNativeDrainBudget = mcpNativeDrainBudget
var McpNativeDrainableWorkerIDs = mcpNativeDrainableWorkerIDs

var McpNativeWorkerIDs = mcpNativeWorkerIDs

// Rate comparison seams for boundary regression tests.
type TestLoadMetrics = testLoadMetrics
type TestLoadRelativeSpec = testLoadRelativeSpec

var TestLoadThresholdEvidenceFor = testLoadThresholdEvidenceFor
var CompareTestBaseline = compareTestBaseline
var ResolveTestRegressionConfig = resolveTestRegressionConfig
var CompareTestStagedLoadRelative = compareTestStagedLoadRelative
var CompareTestGateErrorRate = compareTestGateErrorRate
var CompareRepeatedTestGateErrorRate = compareRepeatedTestGateErrorRate

// Profile warnings in doctor --strict and deploy --plan (#6640).
var CmdDoctor = cmdDoctor
var ResolveSimpleAppPlanWithWarnings = resolveSimpleAppPlanWithWarnings
var RenderSimpleAppPlanWithWarnings = renderSimpleAppPlanWithWarnings

// Deploy preflights (#6625, #6631).
var PreflightDeploySessionStorage = preflightDeploySessionStorage
var GregaleStateHome = gregaleStateHome
var SourceCapLookupBudgets = &sourceCapLookupBudgets
var SourceUploadLimitError = sourceUploadLimitError

// ResolveSourceUploadCapForTest exposes resolveSourceUploadCap's result.
func ResolveSourceUploadCapForTest(ctx context.Context, c *Client) (mb int, verified bool, warning string, explain func(error) error) {
	got := resolveSourceUploadCap(ctx, c)
	return got.mb, got.verified, got.warning(), got.explain
}

// ForwardAppTaskSession is the shared port-forward / postgres connect relay.
var ForwardAppTaskSession = forwardAppTaskSession

// AddBucketBindingUnavailable is add bucket's compute-binding precheck.
var AddBucketBindingUnavailable = addBucketBindingUnavailable

// AddPostgresUnsupported and PrintAddPostgresMigrationHint are add postgres's
// region capability check and its migration-binding hint.
var AddPostgresUnsupported = addPostgresUnsupported
var PrintAddPostgresMigrationHint = printAddPostgresMigrationHint

var DebugWatchNextWait = debugWatchNextWait

var (
	ProfileCapturesVerb     = profileCapturesVerb
	RenderDebugCompareTable = renderDebugCompareTable
)

// ADR-20261107180000 `gregale config explain`.
var (
	ExplainConfigKey         = explainConfigKey
	RenderSettingExplanation = renderSettingExplanation
)

// gregale mcp connect seams (ADR-20261109121411).
var (
	MCPConnectLookPath    = &mcpConnectLookPath
	MCPConnectRun         = &mcpConnectRun
	MCPConnectInteractive = &mcpConnectInteractive
	MCPConnectHome        = &mcpConnectHome
	MCPEndpointCheck      = &mcpEndpointCheck
	MCPHeadlessToolCount  = &mcpHeadlessToolCount
	MCPConnectRunStdin    = &mcpConnectRunStdin
	UpsertCodexServer     = upsertCodexServer
)

// MCPConnectOptions builds the options a test passes to MCPHeadlessToolCount.
type MCPConnectOptions = mcpConnectOptions

var RefuseDevFunctionSet = refuseDevFunctionSet

// MetricLabelFlags exposes the --metric-label parser.
var MetricLabelFlags = metricLabelFlags

// ValidateSubscriptionCapacityCommandBudget exposes the receipt budget check.
var ValidateSubscriptionCapacityCommandBudget = validateSubscriptionCapacityCommandBudget

// ActivityDisplayTime exposes the org activity / keys list time formatter.
var ActivityDisplayTime = activityDisplayTime

// RenderHTTPLogQueryEvent exposes the `logs --source http` row renderer.
var RenderHTTPLogQueryEvent = renderHTTPLogQueryEvent

// ManifestDeclaredBindings exposes the bindings check manifest reader (ADR-20261110213000).
var ManifestDeclaredBindings = manifestDeclaredBindings

// ApplyFunctionHookLifecycleForTest exposes applyFunctionHookLifecycle.
func ApplyFunctionHookLifecycleForTest(ctx context.Context, client interface {
	GetApp(ctx context.Context, slug string) (api.AppResponse, error)
	UpdateApp(ctx context.Context, slug string, req api.UpdateAppRequest) (api.AppResponse, error)
}, slug, sourceDir, handler string) error {
	return applyFunctionHookLifecycle(ctx, client, slug, sourceDir, handler)
}

// InvalidAlertMetricError exposes the unknown --metric error (H32-18).
var InvalidAlertMetricError = invalidAlertMetricError
var RealtimeClientURL = realtimeClientURL
var CmdEdgeRuleListsRm = cmdEdgeRuleListsRm
var CmdAppScaling = cmdAppScaling
var CmdEventsSchemaRollout = cmdEventsSchemaRollout
var CmdOrgsSeatUsage = cmdOrgsSeatUsage

type CustomerOperationTypesResult = customerOperationTypesResult

var CustomerOperationTypesMessage = customerOperationTypesMessage

// WorkerNoInstancesLine exposes the empty worker pool explanation.
var WorkerNoInstancesLine = workerNoInstancesLine

// PrintPublicURLs exposes the plan's public URL list (H32-30).
var PrintPublicURLs = printPublicURLs

var RenderRequestAnalytics = renderRequestAnalytics

var InvocationSourceLabel = invocationSourceLabel
var RenderDeploymentHostingReceiptFor = renderDeploymentHostingReceiptFor

var ParseAppPortsFlag = parseAppPortsFlag

var RenderThrottleSuggestions = renderThrottleSuggestions

// Runtime OOM doctor check (2026-11-13).
var DoctorRuntimeOOMFromSummary = doctorRuntimeOOMFromSummary
var DoctorRuntimeOOMForApp = doctorRuntimeOOMForApp
var ReplaceDoctorCheck = replaceDoctorCheck
var DoctorCheckRuntimeOOM = doctorCheckRuntimeOOM

// Readiness probe flag (2026-11-13).
var ResolveDeployReadiness = resolveDeployReadiness
var DeployProbeOverrides = deployProbeOverrides

// UpdateAppCLISurface is one reviewed CLI surface of an API field.
type UpdateAppCLISurface = updateAppCLISurface

// DeployRequestCLISurfaces returns the deploy request's CLI surfaces.
func DeployRequestCLISurfaces() map[string]UpdateAppCLISurface { return deployRequestCLISurfaces }

// DeployOverridesCLISurfaces returns the deploy overrides' CLI surfaces.
func DeployOverridesCLISurfaces() map[string]UpdateAppCLISurface { return deployOverridesCLISurfaces }

func WaitPrivatePublicationVerificationForTest(ctx context.Context, c *Client, id string, in api.ToolPublicationVerificationRequest) (api.ToolPublicationVerification, error) {
	return waitPrivatePublicationVerification(ctx, c, id, in)
}

// TaskQuarantineGateForTest exposes the fixed adapter to subprocess contract tests.
func TaskQuarantineGateForTest() string { return mcpTaskQuarantineGate }

// TaskRetirementGateForTest exposes the fixed read-only adapter.
func TaskRetirementGateForTest() string { return mcpTaskRetirementGate }

var RememberInitApp = rememberInitApp
var LinkedDefaultAppName = linkedDefaultAppName

// gregale dev findings (2026-11-15).
var DevSourceFingerprint = devSourceFingerprint
var DevLocalEnvExcludes = devLocalEnvExcludes
var DeployCredentialExcludes = deployCredentialExcludes
var WithoutExcludedFindings = withoutExcludedFindings
var ShouldExcludeWithRules = func(srcDir, rel string, isDir, dockerfile bool) bool {
	return loadPackRules(srcDir, dockerfile).exclude(rel, isDir)
}
var DevAuthProxyHandler = devAuthProxyHandler
var DevNeedsAuthProxy = devNeedsAuthProxy
var IsDevSourceRouteMissing = isDevSourceRouteMissing
var OnDiskPathCase = onDiskPathCase
var RenewDevSessionWhileWatching = renewDevSessionWhileWatching

type DevSeedRunner = devSeedRunner
type DevSeedOutcome = devSeedOutcome

var NewDevSeedRunner = newDevSeedRunner

const DevSeedOutcomeDone = devSeedOutcomeDone
const DevSeedOutcomeFailed = devSeedOutcomeFailed

func (r *devSeedRunner) Start() (bool, bool)                    { return r.start() }
func (r *devSeedRunner) Finish(o devSeedOutcome) devSeedOutcome { return r.finish(o) }

// DevLogFollowerRender renders events through one follower and returns
// whether each ended the stream as parked.
func DevLogFollowerRender(events []api.Event) (parked []bool) {
	f := newDevLogFollower(nil)
	for _, event := range events {
		end, done, _ := f.render(event)
		parked = append(parked, done && end == devStreamParked)
	}
	return parked
}

// SyncDevEnvPairs runs one env-file sync with in-memory pairs.
func SyncDevEnvPairs(ctx context.Context, s *devEnvSyncState, client devSecretClient, app, path string, pairs map[string]string, fingerprint byte) (written int, err error) {
	list := make([]secretsPair, 0, len(pairs))
	for key, value := range pairs {
		list = append(list, secretsPair{Key: key, Value: value})
	}
	var fp [32]byte
	fp[0] = fingerprint
	report, err := s.syncPairs(ctx, client, app, path, list, fp, "developer config")
	return report.Written, err
}

// RenderAppProtection exposes renderAppProtection (ADR-20261116120000 §5).
func RenderAppProtection(w io.Writer, slug string, resp api.AppProtectionResponse) {
	renderAppProtection(w, slug, resp)
}

var ExplainPerInstanceConcurrencyRefusal = explainPerInstanceConcurrencyRefusal

// UncommittedProjectFile exposes the HEAD-deploy project-file check.
func UncommittedProjectFile(porcelain, repoRoot, sourceDir string) (string, bool) {
	return uncommittedProjectFile(porcelain, repoRoot, sourceDir)
}

// BucketUploadContentType returns the content type an upload would store.
func BucketUploadContentType(args []string) (string, error) {
	o, err := parseBucketTransfer(args)
	return o.contentType, err
}

// RenderInspectFailureDetails renders inspect's failure section for one
// failed deployment of app slug.
func RenderInspectFailureDetails(w io.Writer, slug string, failed api.DeploymentResponse) {
	renderInspectFailureDetails(w, inspectSummary{App: inspectAppSummary{Slug: slug}, Failure: inspectPersistedFailure(&failed, nil)})
}

// FormatObserved renders a measured alert value.
var FormatObserved = formatObserved

// PrintAffectedText renders a project plan's affected apps.
var PrintAffectedText = printAffectedText

// AppGetURL is the url row of `app get`.
var AppGetURL = appGetURL

// LatestWakeID picks the wake shown by `app wake-timeline APP`.
var LatestWakeID = latestWakeID

// SplitTargetSlug and RenderRouteSplit expose the route split suggestion
// (ADR-20261121170000 amendment 1).
var (
	SplitTargetSlug  = splitTargetSlug
	RenderRouteSplit = renderRouteSplit
)

// WaitForDevPostgres exposes waitForDevPostgres.
func WaitForDevPostgres(ctx context.Context, session api.DevSessionResponse, timeout, poll time.Duration, get func(context.Context) (api.DevSessionResponse, error), waiting func(string)) (api.DevSessionResponse, error) {
	return waitForDevPostgres(ctx, session, timeout, poll, get, waiting)
}

// ADR-20261122194324: gateway-enforced MCP access modes.
var AuditMCPGatewayRules = auditMCPGatewayRules

// MCPAuthEnableConfig is mcpAuthEnableConfig with a Gregale Auth issuer
// URL (empty for api-key).
func MCPAuthEnableConfig(cfg *mcphosting.Config, mode, endpoint, issuer, scopes string) error {
	return mcpAuthEnableConfig(cfg, mode, endpoint, mcpAuthIssuer{url: issuer}, scopes)
}

// RealtimePreviewInView is realtimePreviewInView for the tier defaults
// (tier) or for an account view that hides hidden.
func RealtimePreviewInView(tier bool, hidden ...string) bool {
	if tier {
		return realtimePreviewInView(capabilityView{tier: true})
	}
	return realtimePreviewInView(accountCapabilityView(hidden))
}

// MonetizationInView is monetizationInView for the tier defaults (tier) or
// for an account view that hides hidden.
func MonetizationInView(tier bool, hidden ...string) bool {
	if tier {
		return monetizationInView(capabilityView{tier: true})
	}
	return monetizationInView(accountCapabilityView(hidden))
}

// VerbsDiscoveredBy lists the verbs of command that discovery shows to an
// account whose view hides hidden.
func VerbsDiscoveredBy(command string, hidden ...string) []string {
	view := accountCapabilityView(hidden)
	var names []string
	for _, c := range view.discoverableCommands(visibleCliCommandsWith(realtimePreviewInView(view), monetizationInView(view))) {
		if c.Name == command {
			for _, sub := range c.Subcommands {
				names = append(names, sub.Name)
			}
		}
	}
	return names
}

// RealtimeVerbsDiscoveredBy lists the realtime verbs discovery shows to an
// account whose view hides hidden.
func RealtimeVerbsDiscoveredBy(hidden ...string) []string {
	view := accountCapabilityView(hidden)
	var names []string
	for _, command := range view.discoverableCommands(visibleCliCommandsWith(realtimePreviewInView(view), monetizationInView(view))) {
		if command.Name == "realtime" {
			for _, sub := range command.Subcommands {
				names = append(names, sub.Name)
			}
		}
	}
	return names
}

// Built-in Task release gate (commands_mcp_native_gate.go).
type (
	MCPGateReport      = mcpGateReport
	MCPGateDeployments = mcpGateDeployments
)

var (
	MCPNativeWorkerRuntime     = mcpNativeWorkerRuntime
	RequireMCPNativeNodeWorker = requireMCPNativeNodeWorker
	RequireMCPWebTaskMigration = requireMCPWebTaskMigration
	MCPReportReplacementsReady = mcpReportReplacementsReady
	MCPReportPreviousGone      = mcpReportPreviousGone
	ErrMCPGateOutsideWorkers   = errMCPGateOutsideWorkers
)

// RunMCPGate runs the built-in gate against a test compatibility report and
// hooks.
func RunMCPGate(ctx context.Context, report func(context.Context) (api.MCPTaskCompatibilityReportResponse, error), deployments func() (MCPGateDeployments, error),
	namespace, releaseID, checkpointPath string, timeout time.Duration,
	migrate func(context.Context) error, start func(context.Context, map[string]any) ([]string, error), drain func(context.Context, map[string]any) error) MCPGateReport {
	g := &mcpGate{view: reportMCPGateView{report: report, deployments: deployments}, namespace: namespace, releaseID: releaseID, timeout: timeout, poll: time.Millisecond,
		checkpointPath: checkpointPath, migrate: migrate, start: start, drain: drain}
	return g.run(ctx)
}

var MCPNativeDeployOrder = mcpNativeDeployOrder

// LeadingDeploymentID exposes the traffic verbs' positional-id fold (prod hunt r32).
var LeadingDeploymentID = leadingDeploymentID

// adr: 20261123110137
var WithMCPReplayGate = withMCPReplayGate

// RunMCPReplayGateForTest runs the deploy replay gate and returns its skip
// reason and verdict.
func RunMCPReplayGateForTest(ctx context.Context, endpoint, candidate, serving string, gate *mcpSmokeGate) (string, bool) {
	report, ok := runMCPReplayGate(ctx, endpoint, candidate, serving, "", gate)
	return report.Skipped, ok
}

// SaveAnonymousSession and LoadStoredToken expose the anonymous deploy key
// file and the login store (ADR-20261124080000).
var SaveAnonymousSession = saveAnonymousSession

func LoadStoredToken() string { return loadStoredToken(io.Discard) }

// UseAnonymousDeploySession makes this invocation's deploy run without an
// account for accountID until the test ends.
func UseAnonymousDeploySession(t interface{ Cleanup(func()) }, accountID string) {
	anonymousDeploySession = &api.AnonymousSessionResponse{AccountID: accountID}
	t.Cleanup(func() { anonymousDeploySession = nil })
}

// LoadAnonymousSessionToken exposes the saved anonymous deploy key.
var LoadAnonymousSessionToken = loadAnonymousSessionToken

// LoadAnonymousSessionURL returns the saved anonymous deploy's claim link.
func LoadAnonymousSessionURL() string {
	saved, _ := loadAnonymousSession()
	return saved.ClaimURL
}

// AnonymousDeployByDefault exposes the no-credential deploy default (ADR-20261124080000).
func AnonymousDeployByDefault(getenv func(string) string) bool {
	return anonymousDeployByDefault(getenv)
}

// MCPDiscoverJWKS is mcpDiscoverJWKS.
var MCPDiscoverJWKS = mcpDiscoverJWKS

// MCPToolKeyScopes is mcpToolKeyScopes.
var MCPToolKeyScopes = mcpToolKeyScopes

// MCPAuthNext is mcpAuthNext.
var MCPAuthNext = mcpAuthNext

// SetMCPOIDCHTTPClient replaces the issuer-discovery client until the test ends.
func SetMCPOIDCHTTPClient(t interface{ Cleanup(func()) }, c *http.Client) {
	previous := mcpOIDCHTTPClient
	mcpOIDCHTTPClient = c
	t.Cleanup(func() { mcpOIDCHTTPClient = previous })
}

// MCPAuthEnableExternalIssuer is mcpAuthEnableConfig with an external issuer.
func MCPAuthEnableExternalIssuer(cfg *mcphosting.Config, mode, endpoint, issuer, jwksURL, audience, scopes string) error {
	return mcpAuthEnableConfig(cfg, mode, endpoint, mcpAuthIssuer{url: issuer, jwksURL: jwksURL, audience: audience}, scopes)
}

// ParseToolWeights is parseToolWeights.
var ParseToolWeights = parseToolWeights

// ApplyRouteLimitSpecs parses --route-limit and --tool-limit values and
// applies them to current, as plan-update does.
func ApplyRouteLimitSpecs(current map[string]api.APIConsumerPlanRouteLimit, routes, tools []string) (map[string]api.APIConsumerPlanRouteLimit, error) {
	edits, err := parseRouteLimitEdits(routes, tools)
	if err != nil {
		return nil, err
	}
	return applyRouteLimitEdits(current, edits), nil
}

var CheckManifestAccessFlags = checkManifestAccessFlags

// WithLinkedApp fills a missing leading <slug> from the linked app.
var WithLinkedApp = withLinkedApp

// InitDeployArgs is the deploy flag list init --deploy runs.
var InitDeployArgs = initDeployArgs

// TemplatesPublicOnPurpose lists the templates that deploy public.
var TemplatesPublicOnPurpose = templatesPublicOnPurpose

// DotenvValue reads one .env value (prod hunt r48).
var DotenvValue = dotenvValue

// NewIdleWatchdogReader wraps rc in the deploy log stream's idle watchdog.
func NewIdleWatchdogReader(rc io.ReadCloser, idle time.Duration) io.ReadCloser {
	return newIdleWatchdogReader(rc, idle)
}

var ErrDeployStreamIdle = errDeployStreamIdle

// StreamDeployLogsWait follows a deployment's log stream with --wait
// semantics and the given stream idle bound.
func StreamDeployLogsWait(ctx context.Context, c *Client, dep api.DeploymentResponse, slug string, wait, idle time.Duration) int {
	return streamDeployLogsContextWithOptions(ctx, c, dep, slug, streamDeployOptions{waitTimeout: wait, streamIdleTimeout: idle})
}

// LinkedAppAsArgument exposes linkedAppAsArgument.
var LinkedAppAsArgument = linkedAppAsArgument

// JobReplayCommandNote exposes jobReplayCommandNote.
var JobReplayCommandNote = jobReplayCommandNote

// PublicCommandPath exposes publicCommandPath (prod hunt r59).
var PublicCommandPath = publicCommandPath

var WarnShellLineCommand = warnShellLineCommand

// MCPListingKeep exposes mcpListingKeep.
var MCPListingKeep = mcpListingKeep

// MCPListingContractCheckable exposes mcpListingContractCheckable.
var MCPListingContractCheckable = mcpListingContractCheckable

// SetDoctorBootTimings shortens `doctor --boot` waits for the test.
func SetDoctorBootTimings(cleanup func(func()), startup, stayUp, poll time.Duration) {
	oldStartup, oldStay, oldPoll := doctorBootStartupTimeout, doctorBootStayUp, doctorBootPoll
	doctorBootStartupTimeout, doctorBootStayUp, doctorBootPoll = startup, stayUp, poll
	cleanup(func() { doctorBootStartupTimeout, doctorBootStayUp, doctorBootPoll = oldStartup, oldStay, oldPoll })
}

// ProcNetListeners renders parseProcNetListeners' result as host:port strings.
func ProcNetListeners(text string) []string {
	var out []string
	for _, l := range parseProcNetListeners(text) {
		out = append(out, l.String())
	}
	return out
}

// RunDoctorImageChecksWithLocal runs the image checks with a stand-in for the
// local Docker store: images maps a reference to its `docker image inspect`
// JSON.
func RunDoctorImageChecksWithLocal(ctx context.Context, ref string, inspector doctorImageInspector, images map[string]string) doctorReport {
	return runDoctorImageChecksWithLocal(ctx, ref, nil, inspector, func(_ context.Context, ref string) (doctorLocalImage, bool) {
		raw, ok := images[ref]
		if !ok {
			return doctorLocalImage{}, false
		}
		img, err := parseDockerImageInspect([]byte(raw))
		return img, err == nil
	})
}

// SetDoctorBootMemorySources stubs where `doctor --boot` reads the plan, and
// the --app app's RAM and public host.
func SetDoctorBootMemorySources(cleanup func(func()), plan func(context.Context) (api.Limits, bool), appInfo func(context.Context, string) (int, string, error)) {
	oldPlan, oldApp := doctorImagePlanLimits, doctorBootAppInfo
	doctorImagePlanLimits, doctorBootAppInfo = plan, nil
	if appInfo != nil {
		doctorBootAppInfo = func(ctx context.Context, slug string) (doctorBootAppFacts, error) {
			ram, host, err := appInfo(ctx, slug)
			return doctorBootAppFacts{ramMB: ram, host: host}, err
		}
	}
	cleanup(func() { doctorImagePlanLimits, doctorBootAppInfo = oldPlan, oldApp })
}

// SetDoctorBootAppClass stubs the existing app `--boot` reads as one with
// this workload class.
func SetDoctorBootAppClass(cleanup func(func()), class string) {
	old := doctorBootAppInfo
	doctorBootAppInfo = func(context.Context, string) (doctorBootAppFacts, error) {
		return doctorBootAppFacts{ramMB: 256, workloadClass: class}, nil
	}
	cleanup(func() { doctorBootAppInfo = old })
}

// RunDeployBootCheck runs `deploy --local-image IMAGE --boot`'s check alone.
func RunDeployBootCheck(ctx context.Context, image string) int {
	return runDeployBootCheck(ctx, deployBootFlags{image: image, app: "demo"})
}

// RetainedLineMatches applies `logs --grep` to an archived log frame.
var RetainedLineMatches = retainedLineMatches

var CmdAppTasks = cmdAppTasks
