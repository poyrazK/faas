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
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingrun"
	"github.com/onebox-faas/faas/pkg/chaos"
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
type AutomationCheckReport = automationCheckReport
type AutomationCheckedPublishReport = automationCheckedPublishReport
type AutomationCoverageExclusion = automationCoverageExclusion
type AutomationCoverageGate = automationCoverageGate
type AutomationCoverageHint = automationCoverageHint
type AutomationDefinitionChange = automationDefinitionChange
type AutomationDefinitionDiff = automationDefinitionDiff
type AutomationEventPlan = automationEventPlan
type AutomationEventPlanItem = automationEventPlanItem
type AutomationEventPlanOutcome = automationEventPlanOutcome
type AutomationLiveGateReport = automationLiveGateReport
type AutomationLiveSuiteCaseResult = automationLiveSuiteCaseResult
type AutomationRecoveryJournalEntry = automationRecoveryJournalEntry
type AutomationRecoveryPlan = automationRecoveryPlan
type AutomationRecoveryPreview = automationRecoveryPreview
type AutomationRecoveryReport = automationRecoveryReport
type AutomationRecoveryRetryPreview = automationRecoveryRetryPreview
type AutomationRecoveryRetryRow = automationRecoveryRetryRow
type AutomationRecoverySelection = automationRecoverySelection
type AutomationResponseAction = automationResponseAction
type AutomationResponsePlan = automationResponsePlan
type AutomationRunSummary = automationRunSummary
type AutomationScenario = automationScenario
type AutomationScenarioCoverage = automationScenarioCoverage
type AutomationScenarioResult = automationScenarioResult
type AutomationScenarioSuite = automationScenarioSuite
type AutomationStepExpectation = automationStepExpectation
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
type InspectSummary = inspectSummary
type InspectSummaryInputs = inspectSummaryInputs
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
type TestHTTPRequest = testHTTPRequest
type TestHTTPRequestEvidence = testHTTPRequestEvidence
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
const AutomationDefinitionYAML = automationDefinitionYAML
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
var AuthAdminHTTPClient = &authAdminHTTPClient
var AuthAdminKeyID = &authAdminKeyID
var AuthAdminKeyName = &authAdminKeyName
var AuthEmailTypes = &authEmailTypes
var AuthKeyChangePoll = &authKeyChangePoll
var AuthRestartRetryPause = &authRestartRetryPause
var AuthRestartRetryWindow = &authRestartRetryWindow
var AuthSigningKeyName = &authSigningKeyName
var BillingRunPollInterval = &billingRunPollInterval
var CampaignTrendTemplate = &campaignTrendTemplate
var CliCommands = &cliCommands
var CorsDefaultMethods = &corsDefaultMethods
var DataAPIRotationActions = &dataAPIRotationActions
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
var AddRouteMonitorSourceOwners = addRouteMonitorSourceOwners
var AddSafeReleasePreview = addSafeReleasePreview
var AddValidateParamFlags = addValidateParamFlags
var AdmitOrReadAutomationLiveSuiteCase = admitOrReadAutomationLiveSuiteCase
var AdvancedCliCommands = advancedCliCommands
var AiRouteCallPath = aiRouteCallPath
var AlertRollbackWindowSeconds = alertRollbackWindowSeconds
var AnnounceBindingRun = announceBindingRun
var ApiBase = apiBase
var AppLabel = appLabel
var AppNetworkProbeState = appNetworkProbeState
var AppendDeployPreviewAnnotationIntent = appendDeployPreviewAnnotationIntent
var ApplyAIGuardrailFlags = applyAIGuardrailFlags
var ApplyAutomationCoverageGate = applyAutomationCoverageGate
var ApplyDeployLifecycleToCreateRequest = applyDeployLifecycleToCreateRequest
var ApplyDevManifestDefaults = applyDevManifestDefaults
var ApplyJSONFlag = applyJSONFlag
var ApplyMCPGatewayPolicy = applyMCPGatewayPolicy
var ApplyMCPTasksStatusChecks = applyMCPTasksStatusChecks
var ApplyRolloutFlag = applyRolloutFlag
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
var AutomationCommandArgument = automationCommandArgument
var AutomationEventJobRequest = automationEventJobRequest
var AutomationLiveUUID = automationLiveUUID
var AutomationPublicationEvidence = automationPublicationEvidence
var AutomationRecoveryEventReason = automationRecoveryEventReason
var AutomationRecoveryRetryPlan = automationRecoveryRetryPlan
var AutomationRecoveryRunKey = automationRecoveryRunKey
var AutomationRunJobRequest = automationRunJobRequest
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
var CaptureAutomationStdout = captureAutomationStdout
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
var CheckTestPoolAfterDrain = checkTestPoolAfterDrain
var ChooseMenuAction = chooseMenuAction
var ClassifyDevRuntimeLog = classifyDevRuntimeLog
var CleanupUploadCacheExclusive = cleanupUploadCacheExclusive
var CliConfigPath = cliConfigPath
var CliHelpGroup = cliHelpGroup
var CliHelpGroups = &cliHelpGroups
var CliRelatedCommands = &cliRelatedCommands
var CliSeeAlso = cliSeeAlso
var CliPreAuthPatch = cliPreAuthPatch
var CliRevisionPinTTLSeconds = cliRevisionPinTTLSeconds
var CliSessionMetadataPath = cliSessionMetadataPath
var CmdAI = cmdAI
var CmdReleaseGates = cmdReleaseGates
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
var CmdAdmin = cmdAdmin
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
var CmdAutomations = cmdAutomations
var CmdAutomationsCheck = cmdAutomationsCheck
var CmdAutomationsFailurePolicy = cmdAutomationsFailurePolicy
var CmdAutomationsFailureResume = cmdAutomationsFailureResume
var CmdAutomationsHealth = cmdAutomationsHealth
var CmdAutomationsHistory = cmdAutomationsHistory
var CmdAutomationsInit = cmdAutomationsInit
var CmdAutomationsPublish = cmdAutomationsPublish
var CmdAutomationsRecover = cmdAutomationsRecover
var CmdAutomationsRecoveryPolicy = cmdAutomationsRecoveryPolicy
var CmdAutomationsRecoveryPolicyEvaluate = cmdAutomationsRecoveryPolicyEvaluate
var CmdAutomationsRecoveryPolicyReviews = cmdAutomationsRecoveryPolicyReviews
var CmdAutomationsRecoveryPreview = cmdAutomationsRecoveryPreview
var CmdAutomationsRecoverySuppress = cmdAutomationsRecoverySuppress
var CmdAutomationsReport = cmdAutomationsReport
var CmdAutomationsResponseExecute = cmdAutomationsResponseExecute
var CmdAutomationsResponsePlan = cmdAutomationsResponsePlan
var CmdAutomationsSimulate = cmdAutomationsSimulate
var CmdBilling = cmdBilling
var CmdBillingBudgetPreview = cmdBillingBudgetPreview
var CmdBillingBudgets = cmdBillingBudgets
var CmdBillingCancel = cmdBillingCancel
var CmdBillingExport = cmdBillingExport
var CmdBillingFinancial = cmdBillingFinancial
var CmdBillingPaymentMethod = cmdBillingPaymentMethod
var CmdBillingPortal = cmdBillingPortal
var CmdBillingReconcilePaddleOverage = cmdBillingReconcilePaddleOverage
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
var CmdDataAPI = cmdDataAPI
var CmdDataAPIConfigure = cmdDataAPIConfigure
var CmdDataAPICreate = cmdDataAPICreate
var CmdDataAPIDev = cmdDataAPIDev
var CmdDataAPIExplain = cmdDataAPIExplain
var CmdDataAPIFixture = cmdDataAPIFixture
var CmdDataAPIInstall = cmdDataAPIInstall
var CmdDataAPILive = cmdDataAPILive
var CmdDataAPIStorage = cmdDataAPIStorage
var CmdDataAPIMigration = cmdDataAPIMigration
var CmdDataAPIOnboard = cmdDataAPIOnboard
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
var CmdGithubWebhookSecret = cmdGithubWebhookSecret
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
var CmdPostgresDiagnostics = cmdPostgresDiagnostics
var CmdPostgresGet = cmdPostgresGet
var CmdPostgresList = cmdPostgresList
var CmdPostgresReconcile = cmdPostgresReconcile
var CmdPostgresResize = cmdPostgresResize
var CmdPostgresUsage = cmdPostgresUsage
var CmdPostgresUsageImport = cmdPostgresUsageImport
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

// ReleaseGateRequest builds a release-gates add request from its flags.
func ReleaseGateRequest(name, slo string, maxBurn int, newErrors bool, maxGroups, minEvents int, metric, below, above, aiMetric string,
	maxIncrease, minSamples int) (api.CreateReleaseGateRequest, error) {
	return releaseGateRequest(releaseGateFlags{name: name, slo: slo, maxBurn: maxBurn, newErrors: newErrors, maxGroups: maxGroups,
		minEvents: minEvents, metric: metric, below: below, above: above, aiMetric: aiMetric, maxIncrease: maxIncrease, minSamples: minSamples})
}

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
var ConfiguredJSONPreference = configuredJSONPreference
var ConsumeLogStream = consumeLogStream
var ContainsString = containsString
var CopyBindingReleasePolicy = copyBindingReleasePolicy
var CorrelateRouteMonitorAffectedRoutes = correlateRouteMonitorAffectedRoutes
var CorrelateRouteMonitorIncidentSourceAuto = correlateRouteMonitorIncidentSourceAuto
var CoveragePolicy = coveragePolicy
var CreateDevSetupManifest = createDevSetupManifest
var CreateDurableProjectVerification = createDurableProjectVerification
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
var DeployManifestObjectStorageBindings = deployManifestObjectStorageBindings
var DeployManifestPostgresBindings = deployManifestPostgresBindings
var DeployManifestQueueBindings = deployManifestQueueBindings
var DeployManifestTriggersWithRollback = deployManifestTriggersWithRollback
var DeployOperationIdempotencyKey = deployOperationIdempotencyKey
var DeployOptionsFingerprint = deployOptionsFingerprint
var DeployPreflightListener = deployPreflightListener
var DeployPreflightListenerWith = deployPreflightListenerWith
var DeployPreflightRepoSource = deployPreflightRepoSource
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
var DevHistoryPatchNote = devHistoryPatchNote
var DevPatchExplanation = devPatchExplanation
var DevSetupManifestEdit = devSetupManifestEdit
var DevSetupShellCommand = devSetupShellCommand
var DeveloperIDPath = developerIDPath
var DiagnoseCommitSource = diagnoseCommitSource
var DiagnosticProblem = diagnosticProblem
var DialAppTaskAttach = dialAppTaskAttach
var DiffAutomationDefinition = diffAutomationDefinition
var DiffDataAPISnapshots = diffDataAPISnapshots
var DiffPreviewRoutePolicy = diffPreviewRoutePolicy
var DiffRequestFromCLI = diffRequestFromCLI
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
var EgressAllowlistChangeNeedsRestart = egressAllowlistChangeNeedsRestart
var EmitInspectWatchEvent = emitInspectWatchEvent
var EmptyPilotTextSHA256 = emptyPilotTextSHA256
var EncodeCustomerOperationReceipt = encodeCustomerOperationReceipt
var EnableDataAPILive = enableDataAPILive
var EncodeCustomerOperationRecoveryReceipt = encodeCustomerOperationRecoveryReceipt
var EndpointRecoveryReport = endpointRecoveryReport
var EndpointRecoverySpec = endpointRecoverySpec
var EnsurePostgresEgress = ensurePostgresEgress
var EnsureSourceRefApp = ensureSourceRefApp
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
var EvaluateAutomationScenario = evaluateAutomationScenario
var EvaluateDataAPICoverage = evaluateDataAPICoverage
var EvaluateTestCampaignCoverageRegression = evaluateTestCampaignCoverageRegression
var EvaluateTestCampaignRecoveryRegression = evaluateTestCampaignRecoveryRegression
var EvaluateTestConnectionWindow = evaluateTestConnectionWindow
var EvaluateTestLoadRecovery = evaluateTestLoadRecovery
var EvaluateTestPoolPhase = evaluateTestPoolPhase
var ExecutableHeaderMismatch = executableHeaderMismatch
var ExecuteAutomationLiveSuite = executeAutomationLiveSuite
var ExecuteAutomationRecoveryPlan = executeAutomationRecoveryPlan
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
var GeneratedTestCIRunCommand = generatedTestCIRunCommand
var GeneratedTestCIVMConfigCommand = generatedTestCIVMConfigCommand
var GeneratedTestCIVMRunCommand = generatedTestCIVMRunCommand
var GitArchiveHEAD = gitArchiveHEAD
var GithubTarballURL = githubTarballURL
var GithubWebhookSecretSet = githubWebhookSecretSet
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
var InspectCommitEvent = inspectCommitEvent
var InspectData = inspectData
var InspectOpenAPI = inspectOpenAPI
var InspectProductPilotHelp = inspectProductPilotHelp
var InspectRecommendations = inspectRecommendations
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
var JsonOut = jsonOut
var LinkedAppSlug = linkedAppSlug
var LinkedProjectContext = linkedProjectContext
var LiveCLIView = liveCLIView
var LiveServicePolicy = liveServicePolicy
var LiveSuiteCaseKey = liveSuiteCaseKey
var LiveSuiteHash = liveSuiteHash
var LoadAutomationScenarios = loadAutomationScenarios
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
var LocalTestConsumerEnv = localTestConsumerEnv
var LockAutomationLiveSuite = lockAutomationLiveSuite
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
var McpNativeDeploy = mcpNativeDeploy
var McpNativeFingerprint = mcpNativeFingerprint
var McpNativeWorkerLogs = mcpNativeWorkerLogs
var McpTasksObserverConfigEnvNames = mcpTasksObserverConfigEnvNames
var McpTasksObserverCredential = mcpTasksObserverCredential
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
var ParkedArchivePendingNote = parkedArchivePendingNote
var ParkedLogsSelection = parkedLogsSelection
var ParkedWindowInstances = parkedWindowInstances
var ParseAIPriceSpec = parseAIPriceSpec
var ParseAIRouteTargets = parseAIRouteTargets
var ParseAutomationLiveBaseline = parseAutomationLiveBaseline
var ParseAutomationLiveGate = parseAutomationLiveGate
var ParseAutomationLiveSuite = parseAutomationLiveSuite
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
var PrepareAutomationExpectation = prepareAutomationExpectation
var PrepareAutomationLiveSuite = prepareAutomationLiveSuite
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
var PrintAutomationCheckReport = printAutomationCheckReport
var PrintAutomationDefinitionDiff = printAutomationDefinitionDiff
var PrintAutomationRevisionDetails = printAutomationRevisionDetails
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
var PublishAutomationWithScenarios = publishAutomationWithScenarios
var PublishDeployIssueSourceMaps = publishDeployIssueSourceMaps
var PumpAppTaskAttach = pumpAppTaskAttach
var QualifyProjectEnvironmentWithProfile = qualifyProjectEnvironmentWithProfile
var QualifyProjectEnvironmentWithProfileAndHTTPClient = qualifyProjectEnvironmentWithProfileAndHTTPClient
var QueueScalingBounds = queueScalingBounds
var QueueStatusHealth = queueStatusHealth
var RandomAuthSecret = randomAuthSecret
var RankDataAPIQueryShapes = rankDataAPIQueryShapes
var ReadAuthzFile = readAuthzFile
var ReadAutomationDefinition = readAutomationDefinition
var ReadAutomationEventPlan = readAutomationEventPlan
var ReadAutomationItemMockAttempts = readAutomationItemMockAttempts
var ReadAutomationLiveSuite = readAutomationLiveSuite
var ReadAutomationRecoveryPlan = readAutomationRecoveryPlan
var ReadAutomationResponsePlan = readAutomationResponsePlan
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
var ReadStandardReviewCLI = readStandardReviewCLI
var ReadTestBaselineReport = readTestBaselineReport
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
var RehearsalTestRepo = rehearsalTestRepo
var RejectDevTriggerAppSelection = rejectDevTriggerAppSelection
var ReleaseDiffLines = releaseDiffLines
var ReleaseSummaryLine = releaseSummaryLine
var ReleasedUsage = releasedUsage
var RemoveTestFaultRules = removeTestFaultRules
var RenderAccountSLO = renderAccountSLO
var RenderAppBindingInventory = renderAppBindingInventory
var RenderAppMetrics = renderAppMetrics
var RenderAppSLO = renderAppSLO
var RenderApplyRescue = renderApplyRescue
var RenderAutomationLiveGate = renderAutomationLiveGate
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
var RunAutomationLiveGate = runAutomationLiveGate
var RunAutomationScenarios = runAutomationScenarios
var RunBindingCheckCLI = runBindingCheckCLI
var RunBindingVerificationJobs = runBindingVerificationJobs
var RunBucketCapacity = runBucketCapacity
var RunBucketCopySources = runBucketCopySources
var RunBucketDefaultEncryption = runBucketDefaultEncryption
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
var RunMCPBrowserConnection = runMCPBrowserConnection
var RunMCPCatalogMonitor = runMCPCatalogMonitor
var RunMCPNativeRelease = runMCPNativeRelease
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
var RunTestScenarioStepLoad = runTestScenarioStepLoad
var RunTestScenarioSteps = runTestScenarioSteps
var RunTestSuspendResume = runTestSuspendResume
var RunVersionProtection = runVersionProtection
var RunWithStderr = runWithStderr
var SafeMCPConnectionError = safeMCPConnectionError
var SafeReleaseHealthGate = safeReleaseHealthGate
var SameBindingDeployment = sameBindingDeployment
var SameSunsetContracts = sameSunsetContracts
var SaveAutomationLiveSuite = saveAutomationLiveSuite
var SaveAutomationResponsePlan = saveAutomationResponsePlan
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
var SharedAutomationLiveSuiteID = sharedAutomationLiveSuiteID
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
var TestLatencySelection = testLatencySelection
var TestManagedWorkDigest = testManagedWorkDigest
var TestPhaseRecoveryEvidence = testPhaseRecoveryEvidence
var TestRecoveryDeadlineSpec = testRecoveryDeadlineSpec
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
var UnexcludedAutomationCoverageHints = unexcludedAutomationCoverageHints
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
var ValidLiveSuiteHash = validLiveSuiteHash
var ValidSecretClass = validSecretClass
var ValidTestCIEnvironment = validTestCIEnvironment
var ValidateAutomationCoverageExclusions = validateAutomationCoverageExclusions
var ValidateAutomationEventPlan = validateAutomationEventPlan
var ValidateAutomationRecoveryPlan = validateAutomationRecoveryPlan
var ValidateAutomationResponsePlan = validateAutomationResponsePlan
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
var WaitAlertRollback = waitAlertRollback
var WaitCommitEvent = waitCommitEvent
var WaitDataAPIRefresh = waitDataAPIRefresh
var WaitDataAPIRestart = waitDataAPIRestart
var WaitDataPipeline = waitDataPipeline
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
var WarnRootOnlyProtectiveRule = warnRootOnlyProtectiveRule
var WarnTemplatedEdgeRulePaths = warnTemplatedEdgeRulePaths
var WatchDevPatch = watchDevPatch
var WithBindingProgress = withBindingProgress
var WithBindingVerificationBudget = withBindingVerificationBudget
var WithBindingVerificationSlot = withBindingVerificationSlot
var WithCwd = withCwd
var WithFixtureCronStaging = withFixtureCronStaging
var WithGetErr = withGetErr
var WithRecorder = withRecorder
var WithResetJSONOutput = withResetJSONOutput
var WithTTYForTest = withTTYForTest
var WithoutConfirmationFlags = withoutConfirmationFlags
var WorkerReplicaBounds = workerReplicaBounds
var WriteAutomationFixture = writeAutomationFixture
var WriteAutomationHealthRunHints = writeAutomationHealthRunHints
var WriteAutomationRecoveryAttention = writeAutomationRecoveryAttention
var WriteAutomationRecoveryPlanFile = writeAutomationRecoveryPlanFile
var WriteAutomationSuggestions = writeAutomationSuggestions
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

// Members of appTaskStreamSession
func (r *appTaskStreamSession) Stdout() **io.PipeReader { return &r.stdout }

// Members of authCreateOptions
func (r *authCreateOptions) EmailAPIKey() *string   { return &r.emailAPIKey }
func (r *authCreateOptions) EmailFrom() *string     { return &r.emailFrom }
func (r *authCreateOptions) EmailProvider() *string { return &r.emailProvider }

// Members of automationLiveSuiteOptions
func (r *automationLiveSuiteOptions) Interval() *time.Duration       { return &r.interval }
func (r *automationLiveSuiteOptions) Path() *string                  { return &r.path }
func (r *automationLiveSuiteOptions) Plan() *automationLiveSuitePlan { return &r.plan }

// Members of automationScenarioCoverage
func (r automationScenarioCoverage) XHints(p0 api.WorkflowSpec) []automationCoverageHint {
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

// Members of loadedAutomationScenario
func (r *loadedAutomationScenario) CoverageExclusions() *[]automationCoverageExclusion {
	return &r.coverageExclusions
}
func (r *loadedAutomationScenario) Outputs() *map[string]json.RawMessage    { return &r.outputs }
func (r *loadedAutomationScenario) Request() *api.SimulateAutomationRequest { return &r.request }
func (r *loadedAutomationScenario) Scenario() *automationScenario           { return &r.scenario }

// Members of manifestCronTransaction
func (r *manifestCronTransaction) Steps() *[]manifestCronRollbackStep { return &r.steps }

// Members of mcpReleaseAuthorization
func (r *mcpReleaseAuthorization) InsufficientToken() *string { return &r.insufficientToken }

// Members of mcpReleaseRole
func (r *mcpReleaseRole) Token() *string { return &r.token }

// Members of mcpServeOptions
func (r *mcpServeOptions) AllowWrites() *bool   { return &r.allowWrites }
func (r *mcpServeOptions) Onboarding() *string  { return &r.onboarding }
func (r *mcpServeOptions) Redirects() *[]string { return &r.redirects }
func (r *mcpServeOptions) StateFile() *string   { return &r.stateFile }

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

var PrintEdgeRuleEvents = printEdgeRuleEvents
var ResolveArchiveInstance = resolveArchiveInstance
var RequestLatencyMS = requestLatencyMS
var SafeDeployWaitTimeoutSeconds = safeDeployWaitTimeoutSeconds
var InferLatestWorkflowName = inferLatestWorkflowName
var CompactUsage = compactUsage

const UsageSynopsisLimit = usageSynopsisLimit

var InvokedCommandPath = &invokedCommandPath

var AppArgumentEnabled = &appArgumentEnabled

// AppArgumentPaths lists every visible command path whose leaf takes the
// app as its first argument.
func AppArgumentPaths() [][]string {
	var paths [][]string
	var walk func(path []string, flags []cliFlag, positionals []string, children []cliSub)
	walk = func(path []string, flags []cliFlag, positionals []string, children []cliSub) {
		if takesAppArgument(flags, positionals, children) {
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
