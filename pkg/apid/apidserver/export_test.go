// export_test.go exposes unexported identifiers to the external test package
// apidserver_test (ADR-982). External tests compile as a separate unit from
// the package and its internal tests, so `go test` never holds all of
// apidserver's tests in one compiler process. Package-level vars are exposed
// as pointers ((*apidserver.V)) and struct fields as pointer accessors
// ((*x.F())) so tests can read and write them.
//
// When an external test needs another unexported identifier, add it here.

package apidserver

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/apid/issueslack"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appevents"
	"github.com/onebox-faas/faas/pkg/apid/billingruns"
	"github.com/onebox-faas/faas/pkg/apid/consumerbilling"
	"github.com/onebox-faas/faas/pkg/apid/consumercontrols"
	"github.com/onebox-faas/faas/pkg/apid/consumercredits"
	"github.com/onebox-faas/faas/pkg/apid/durable"
	"github.com/onebox-faas/faas/pkg/apid/edgerules"
	"github.com/onebox-faas/faas/pkg/apid/issuetrackers"
	"github.com/onebox-faas/faas/pkg/apid/managedrealtime"
	"github.com/onebox-faas/faas/pkg/apid/object"
	"github.com/onebox-faas/faas/pkg/apid/operation"
	"github.com/onebox-faas/faas/pkg/apid/product"
	"github.com/onebox-faas/faas/pkg/apid/statementinvoices"
	"github.com/onebox-faas/faas/pkg/apid/tenantplans"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/issuetracker"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/profiling"
	"github.com/onebox-faas/faas/pkg/promql"
	objruntime "github.com/onebox-faas/faas/pkg/rehearsalobj/runtime"
	pgruntime "github.com/onebox-faas/faas/pkg/rehearsalpg/runtime"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// Package-level types
type AffectedPartition = affectedPartition
type AnnotationForm = annotationForm
type AuditFailedOps = auditFailedOps
type Auditor = auditor
type BatchCreateResponse = batchCreateResponse
type CachedSource = cachedSource
type CaptureNotifier = captureNotifier
type ChannelDeliverer = channelDeliverer
type CloneCaptureBarrierObservation = cloneCaptureBarrierObservation
type CloneCoordinatorFixture = cloneCoordinatorFixture
type ComputeNodeHeartbeatsResponse = computeNodeHeartbeatsResponse
type ComputeNodeResponse = computeNodeResponse
type ConsumeRefundProvider = consumeRefundProvider
type DebugEvidenceSpan = debugEvidenceSpan
type DebugRunningEvent = debugRunningEvent
type DebugTelemetryCursorFilters = debugTelemetryCursorFilters
type DebugTelemetryFilters = debugTelemetryFilters
type ErrEnvClassifier = errEnvClassifier
type ErrorsCursorShape = errorsCursorShape
type ExtractLimits = extractLimits
type FailedLoginRow = failedLoginRow
type FakeObjectProvider = fakeObjectProvider
type FeatureFlagEvidencePage = featureFlagEvidencePage
type FeatureFlagOutcome = featureFlagOutcome
type FeatureFlagOutcomesResponse = featureFlagOutcomesResponse
type FeatureFlagRequestEvidence = featureFlagRequestEvidence
type FeatureFlagRolloutPromotionResponse = featureFlagRolloutPromotionResponse
type GithubActivityResponse = githubActivityResponse
type GithubCheckActivityResponse = githubCheckActivityResponse
type GithubWebhookActivityResponse = githubWebhookActivityResponse
type GoogleJWKSVerifier = googleJWKSVerifier
type InspectFeatureFlagRequest = inspectFeatureFlagRequest
type LiveClient = liveClient
type ManagedWorkflowDependencyInput = managedWorkflowDependencyInput
type ManagedWorkflowObservedStep = managedWorkflowObservedStep
type NoopMailer = noopMailer
type NoopNotifier = noopNotifier
type NopReadCloser = nopReadCloser
type PendingHostnameRow = pendingHostnameRow
type PgHandlerEnv = pgHandlerEnv
type PlanCron = planCron
type PlanTokenWire = planTokenWire
type ProjectEnvironmentBindingClone = projectEnvironmentBindingClone
type PrometheusTargetGroup = prometheusTargetGroup
type PromoteFeatureFlagRolloutRequest = promoteFeatureFlagRolloutRequest
type RecordingMailer = recordingMailer
type ResendEvent = resendEvent
type RollbackFeatureFlagsRequest = rollbackFeatureFlagsRequest
type RouteStats = routeStats
type RuntimeConfigEntryResponse = runtimeConfigEntryResponse
type RuntimeConfigManager = runtimeConfigManager
type RuntimeConfigRestartNotifier = runtimeConfigRestartNotifier
type ScanPlanResponse = scanPlanResponse
type Server = server
type SourceRefManagedPostgresProvider = sourceRefManagedPostgresProvider
type SourceRefTestEnv = sourceRefTestEnv
type SpyNotifier = spyNotifier
type StartUploadRequest = startUploadRequest
type StartUploadResponse = startUploadResponse
type StubGithubdClient = stubGithubdClient
type StubRollbackArtifactVerifier = stubRollbackArtifactVerifier
type TestEnv = testEnv
type UpdateFeatureFlagsRequest = updateFeatureFlagsRequest

// Package-level consts
const AppTaskTestDigest = appTaskTestDigest
const AppWebhookSecretSealLabel = appWebhookSecretSealLabel
const BuilderdMetricsDiscoveryPath = builderdMetricsDiscoveryPath
const CertStatusPending = certStatusPending
const ComputeMetricsDiscoveryPath = computeMetricsDiscoveryPath
const CsrfActionLogout = csrfActionLogout
const CsrfActionSessionRevoke = csrfActionSessionRevoke
const CsrfActionSessionsRevokeAll = csrfActionSessionsRevokeAll
const DashboardAsyncInvocationPath = dashboardAsyncInvocationPath
const DashboardDataAPICookie = dashboardDataAPICookie
const DashboardAuthSettingsCookie = dashboardAuthSettingsCookie
const DashboardDebugReplayCSRFCookie = dashboardDebugReplayCSRFCookie
const DashboardDevBridgeAction = dashboardDevBridgeAction
const DashboardDevBridgeCSRFCookie = dashboardDevBridgeCSRFCookie
const DashboardEdgeRulesAction = dashboardEdgeRulesAction
const DashboardEdgeRulesCSRFCookie = dashboardEdgeRulesCSRFCookie
const DashboardFailedEventsAction = dashboardFailedEventsAction
const DashboardFailedEventsCSRFCookie = dashboardFailedEventsCSRFCookie
const DashboardFailedEventsPageSize = dashboardFailedEventsPageSize
const DashboardInstanceAction = dashboardInstanceAction
const DashboardInstanceCSRFCookie = dashboardInstanceCSRFCookie
const DashboardIssueCookie = dashboardIssueCookie
const DashboardJobSchedulePolicyAction = dashboardJobSchedulePolicyAction
const DashboardJobSchedulePolicyCookie = dashboardJobSchedulePolicyCookie
const DashboardJobsAction = dashboardJobsAction
const DashboardJobsCSRFCookie = dashboardJobsCSRFCookie
const DashboardKeyConfirmField = dashboardKeyConfirmField
const DashboardKeyDeleteCSRFCookie = dashboardKeyDeleteCSRFCookie
const DashboardMFAPath = dashboardMFAPath
const DashboardMCPAlertCookie = dashboardMCPAlertCookie
const DashboardMirrorsCSRFCookie = dashboardMirrorsCSRFCookie
const DashboardReplayFailedAction = dashboardReplayFailedAction
const DashboardReplayFailedCSRFCookie = dashboardReplayFailedCSRFCookie
const DashboardRollbackCSRFCookie = dashboardRollbackCSRFCookie
const DashboardSecretCSRFCookie = dashboardSecretCSRFCookie
const DashboardStorageCSRFCookie = dashboardStorageCSRFCookie
const DashboardTenantSurfacesCSRFCookie = dashboardTenantSurfacesCSRFCookie
const DashboardWebhooksAction = dashboardWebhooksAction
const DashboardWebhooksCSRFCookie = dashboardWebhooksCSRFCookie
const DebugDependencyComparisonMaxItems = debugDependencyComparisonMaxItems
const DebugDependencyHistoryMaxGroups = debugDependencyHistoryMaxGroups
const DebugDependencyHistoryMaxOutput = debugDependencyHistoryMaxOutput
const DebugRunningEventKind = debugRunningEventKind
const DebugTelemetryAnonymousConsumer = debugTelemetryAnonymousConsumer
const DefaultCLIAuthURLBase = defaultCLIAuthURLBase
const DevPatchDeliveryEnv = devPatchDeliveryEnv
const DevSourceManifestsKept = devSourceManifestsKept
const EmailVerificationPath = emailVerificationPath
const FeatureFlagAutoAdvanceActor = featureFlagAutoAdvanceActor
const GithubBindAction = githubBindAction
const GithubBindCSRFCookie = githubBindCSRFCookie
const GithubConnectAction = githubConnectAction
const GithubConnectCSRFCookie = githubConnectCSRFCookie
const GithubConnectReturnCookie = githubConnectReturnCookie
const GithubNoAppInstallError = githubNoAppInstallError
const GithubWizardCreateAction = githubWizardCreateAction
const GithubWizardCreateCSRFCookie = githubWizardCreateCSRFCookie
const GoodSidecarImage = goodSidecarImage
const GoogleAuthNonceCookie = googleAuthNonceCookie
const GoogleAuthPKCECookie = googleAuthPKCECookie
const GoogleAuthStateCookie = googleAuthStateCookie
const GoogleCallbackPath = googleCallbackPath
const ImagedMetricsDiscoveryPath = imagedMetricsDiscoveryPath
const InboundWebhookSignatureTolerance = inboundWebhookSignatureTolerance
const ListAuditLogLimitDefault = listAuditLogLimitDefault
const LoginPath = loginPath
const MaxMetricsDiscoveryTargets = maxMetricsDiscoveryTargets
const NodeMetricsDiscoveryPath = nodeMetricsDiscoveryPath
const OauthCodeCallbackPath = oauthCodeCallbackPath
const OauthCodeStateCookie = oauthCodeStateCookie
const OauthHTTPTimeout = oauthHTTPTimeout
const OauthResponseMaxBytes = oauthResponseMaxBytes
const PassedObjectStorageVerification = passedObjectStorageVerification
const PassedPostgresVerification = passedPostgresVerification
const PlanCacheTTL = planCacheTTL
const Pr3AdminEmail = pr3AdminEmail
const ProbeFail = probeFail
const ProfileDeploymentPolicyAction = profileDeploymentPolicyAction
const ProfileDeploymentPolicyCookie = profileDeploymentPolicyCookie
const ProfileInvestigationCSRFAction = profileInvestigationCSRFAction
const ProfileInvestigationCSRFCookie = profileInvestigationCSRFCookie
const PromtailMetricsDiscoveryPath = promtailMetricsDiscoveryPath
const RealtimedMetricsDiscoveryPath = realtimedMetricsDiscoveryPath
const RequestAnalyticsRouteMaxLength = requestAnalyticsRouteMaxLength
const RouteCostByRequestTime = routeCostByRequestTime
const RouteCostByRequests = routeCostByRequests
const RuntimeConfigDataPlacement = runtimeConfigDataPlacement
const RuntimeConfigDomainDoctor = runtimeConfigDomainDoctor
const RuntimeConfigDomainDoctorTTL = runtimeConfigDomainDoctorTTL
const RuntimeConfigHSTS = runtimeConfigHSTS
const RuntimeConfigS3 = runtimeConfigS3
const SafeReleaseEmergencyGrace = safeReleaseEmergencyGrace
const ScanSpoolRootEnv = scanSpoolRootEnv
const ScheddMetricsDiscoveryPath = scheddMetricsDiscoveryPath
const GatewaydPublicEdgeMetricsDiscoveryPath = gatewaydPublicEdgeMetricsDiscoveryPath
const CloudflaredMetricsDiscoveryPath = cloudflaredMetricsDiscoveryPath
const ServiceCallerKeysCacheControl = serviceCallerKeysCacheControl
const SessionCookie = sessionCookie
const SessionCookieLifetime = sessionCookieLifetime
const SourceSpoolRootEnv = sourceSpoolRootEnv
const StripeWebhookSecretForTest = stripeWebhookSecretForTest
const SweepDefaultReason = sweepDefaultReason
const UploadSessionOpenCap = uploadSessionOpenCap
const VmmdMetricsDiscoveryPath = vmmdMetricsDiscoveryPath

// Package-level vars
var AppsDomainFunc = &appsDomainFunc
var BuildPlanCacheOnce = &buildPlanCacheOnce
var CapsDecl = &capsDecl
var CertDialContextFunc = &certDialContextFunc
var CertLookupIPFunc = &certLookupIPFunc
var ChannelSender = &channelSender
var CnameLookupFunc = &cnameLookupFunc
var DialCertFunc = &dialCertFunc
var ErrCertAddressBlocked = &errCertAddressBlocked
var ErrCertFailure = &errCertFailure
var ErrClassifierHostHashFailed = &errClassifierHostHashFailed
var ErrClassifierInsert = &errClassifierInsert
var ErrClassifierInternal = &errClassifierInternal
var ErrClassifierPortRange = &errClassifierPortRange
var ErrClassifierUUIDParse = &errClassifierUUIDParse
var ErrCloneCompensationUnavailable = &errCloneCompensationUnavailable
var ErrGithubdNotReady = &errGithubdNotReady
var ErrIsolatedObjectStorageCloneUnsupported = &errIsolatedObjectStorageCloneUnsupported
var HostHMACKey = &hostHMACKey
var HostIdentitiesForUnseal = &hostIdentitiesForUnseal
var MfaIdentities = &mfaIdentities
var MfaIdentity = &mfaIdentity
var MfaRecipient = &mfaRecipient
var ObsJailInternalMarkers = &obsJailInternalMarkers
var ObsSealedBlobMarkers = &obsSealedBlobMarkers
var OutboundCredentialRecipient = &outboundCredentialRecipient
var PgBackupStateRoot = &pgBackupStateRoot
var PlanCache = &planCache
var PlanCacheRoot = &planCacheRoot
var PlanCacheRootErr = &planCacheRootErr
var PlanCacheRootOnce = &planCacheRootOnce
var PlanSourceCache = &planSourceCache
var SetSecretRecipient = &setSecretRecipient
var SseChannels = &sseChannels
var TimeNow = &timeNow
var TxtLookupFunc = &txtLookupFunc

// Package-level funcs
var AccountAbuseHoldView = accountAbuseHoldView
var ActivatedAPIDListener = activatedAPIDListener
var AggregateWakeTimeline = aggregateWakeTimeline
var AlertChannelItem = alertChannelItem
var AlertRuleReq = alertRuleReq
var AllocateRequestAnalyticsRouteCost = allocateRequestAnalyticsRouteCost
var AllocateRequestAnalyticsRouteCostByTime = allocateRequestAnalyticsRouteCostByTime
var AnnotateDeploymentCPURegressions = annotateDeploymentCPURegressions
var AnnotateRouteDeploymentCPURegressions = annotateRouteDeploymentCPURegressions
var ApiManifestFromState = apiManifestFromState
var ApidListener = apidListener
var AppDeclaresTCPListener = appDeclaresTCPListener
var AppDeclaresUDPListener = appDeclaresUDPListener
var AppHostForDomain = appHostForDomain
var AppIDFromPayload = appIDFromPayload
var ApplyProjectOneWorkloadTarGz = applyProjectOneWorkloadTarGz
var AssertCloneCaptureHasNoPoint = assertCloneCaptureHasNoPoint
var AssertProblem = assertProblem
var AssertPromotionWeights = assertPromotionWeights
var AttachRequestAnalyticsRouteDeploymentObservations = attachRequestAnalyticsRouteDeploymentObservations
var AuditEventResponse = auditEventResponse
var BeginVerificationTask = beginVerificationTask
var BoolJSON = boolJSON
var BoolPtr = boolPtr
var BucketResponse = bucketResponse
var BuildCachedSourceRequest = buildCachedSourceRequest
var BuildCapturedProjectEnvironmentDatabasePlans = buildCapturedProjectEnvironmentDatabasePlans
var BuildCapturedProjectEnvironmentObjectPlans = buildCapturedProjectEnvironmentObjectPlans
var BuildDebugCriticalPathHistory = buildDebugCriticalPathHistory
var BuildDebugDependencyComparisonBetween = buildDebugDependencyComparisonBetween
var BuildDebugDependencyDeploymentComparison = buildDebugDependencyDeploymentComparison
var BuildDebugDependencyLatencyHistory = buildDebugDependencyLatencyHistory
var BuildEnvDiffResponse = buildEnvDiffResponse
var BuildObsCapacityProfiles = buildObsCapacityProfiles
var BuildObsTenantUsageProfiles = buildObsTenantUsageProfiles
var BuildSourceRefTarGz = buildSourceRefTarGz
var BuildTestAlertEvent = buildTestAlertEvent
var BuildTestTarGz = buildTestTarGz
var CapturedCloneConfigurationResources = capturedCloneConfigurationResources
var CapturedCloneObjectWorkerContract = capturedCloneObjectWorkerContract
var CapturedProjectEnvironmentDatabaseResources = capturedProjectEnvironmentDatabaseResources
var CapturedProjectEnvironmentObjectResources = capturedProjectEnvironmentObjectResources
var CheckTXT = checkTXT
var CheckedRollbackAPIFixture = checkedRollbackAPIFixture
var ClaimAppRestart = claimAppRestart
var ClassifierFailureMetricReason = classifierFailureMetricReason
var ClientIPFromRequest = clientIPFromRequest
var CloneCaptureBarrierFixture = cloneCaptureBarrierFixture
var CloneConfigurationFenceMatches = cloneConfigurationFenceMatches
var ClosePoolAfterCancel = closePoolAfterCancel
var CompletePromotionProbe = completePromotionProbe
var CompleteVerificationTask = completeVerificationTask
var CompletenessWindow = completenessWindow
var ComputeAffectedPartition = computeAffectedPartition
var ComputeMetricsTarget = computeMetricsTarget
var ConfigureSourceRefManagedPostgres = configureSourceRefManagedPostgres
var Contains = contains
var CookieDo = cookieDo
var CountAffectedRequests = countAffectedRequests
var CreateApp = createApp
var CreateAppTaskForTest = createAppTaskForTest
var CreateExecutionAs = createExecutionAs
var CreateProjectEnvironmentConfigFixture = createProjectEnvironmentConfigFixture
var CreateRunsOnlyKey = createRunsOnlyKey
var DaemonMetricsTarget = daemonMetricsTarget
var DashboardCronSchedulePolicyAction = dashboardCronSchedulePolicyAction
var DashboardCronSchedulePolicyCookie = dashboardCronSchedulePolicyCookie
var DashboardCustomerOperationsURL = dashboardCustomerOperationsURL
var DashboardInputToken = dashboardInputToken
var DashboardLogDrainPageItem = dashboardLogDrainPageItem
var DashboardOperationListOptions = dashboardOperationListOptions
var DashboardOperationSubjectURL = dashboardOperationSubjectURL
var DashboardPOST = dashboardPOST
var DataAPITaskCommand = dataAPITaskCommand
var DataAPITaskKind = dataAPITaskKind
var DataUpstreamResponseFromState = dataUpstreamResponseFromState
var DebugCoverageSignal = debugCoverageSignal
var DebugCoverageTimestamp = debugCoverageTimestamp
var DebugRegressionCandidateRoutes = debugRegressionCandidateRoutes
var DebugRegressionObservationToItem = debugRegressionObservationToItem
var DebugRegressionParams = debugRegressionParams
var DebugRequestIDCandidates = debugRequestIDCandidates
var DebugReplayRequestPath = debugReplayRequestPath
var DebugTelemetryCursorParams = debugTelemetryCursorParams
var DebugTelemetryExportFilename = debugTelemetryExportFilename
var DecodeBindingInventory = decodeBindingInventory
var DecodeDebugTelemetryCursor = decodeDebugTelemetryCursor
var DecodePlanToken = decodePlanToken
var DecodePlatformTenantActivityCursor = decodePlatformTenantActivityCursor
var DecodeProjectEnvironmentPromotionToken = decodeProjectEnvironmentPromotionToken
var DefaultDeps = defaultDeps
var DegradedAccountSLO = degradedAccountSLO
var DelayedTaskSchedule = delayedTaskSchedule
var DeriveLast4FromHash = deriveLast4FromHash
var DevPatchEntriesToState = devPatchEntriesToState
var DevSessionSlug = devSessionSlug
var DialCert = dialCert
var DiscardLogger = discardLogger
var DoctorReportFromObs = doctorReportFromObs
var DrainAuditOutboxOnce = drainAuditOutboxOnce
var DrainOrgActivityOutboxOnce = drainOrgActivityOutboxOnce
var DurableMetric = durableMetric
var EchoDebugSince = echoDebugSince
var EdgeRuleRouteReq = edgeRuleRouteReq
var EffectiveInvocationRetryPolicy = effectiveInvocationRetryPolicy
var EmergencyAbortSweep = emergencyAbortSweep
var EmitSkippedAuditRows = emitSkippedAuditRows
var EmitWorkloadSkippedRow = emitWorkloadSkippedRow
var EnableAppTaskAPIForTest = enableAppTaskAPIForTest
var EnableExecutionAPIForTest = enableExecutionAPIForTest
var EncodeDebugTelemetryCursor = encodeDebugTelemetryCursor
var EncodeDebugTelemetryCursorWithFilters = encodeDebugTelemetryCursorWithFilters
var EncodeErrorsCursor = encodeErrorsCursor
var EncodePlatformTenantActivityCursor = encodePlatformTenantActivityCursor
var EncodeSuspectedDependency = encodeSuspectedDependency
var EnforceCreateTriggerCaps = enforceCreateTriggerCaps
var EnvironmentAppsWithoutLiveDeployment = environmentAppsWithoutLiveDeployment
var EvaluateProjectedQuotaGate = evaluateProjectedQuotaGate
var EvaluateQuotaGate = evaluateQuotaGate
var ExecutionRequest = executionRequest
var ExtractInputValue = extractInputValue
var ExtractTarGzToDir = extractTarGzToDir
var FailedStageFromJSON = failedStageFromJSON
var FeatureFlagConfigRequiresSubjectTargeting = featureFlagConfigRequiresSubjectTargeting
var FeatureFlagOutcomeQuery = featureFlagOutcomeQuery
var FetchAccountMetricsSnapshot = fetchAccountMetricsSnapshot
var FindDashboardCookie = findDashboardCookie
var FindEventByKind = findEventByKind
var FinishExecutionWithArtifact = finishExecutionWithArtifact
var FlagEvidenceQuery = flagEvidenceQuery
var FocusExportProblem = focusExportProblem
var GetCachedBuildPlan = getCachedBuildPlan
var GithubAppInstallURL = githubAppInstallURL
var GithubDeploysAvailabilityProbe = githubDeploysAvailabilityProbe
var HasDeclaredRouteSource = hasDeclaredRouteSource
var HexPrefix = hexPrefix
var IdempotencyReplayable = idempotencyReplayable
var IdempotencyResponseReplayable = idempotencyResponseReplayable
var InjectCSRFToken = injectCSRFToken
var InstallPromFixture = installPromFixture
var IntPtr = intPtr
var IsDigestPinned = isDigestPinned
var IsPublicCertAddress = isPublicCertAddress
var IsSecurityHeadersRule = isSecurityHeadersRule
var IssueCreateToken = issueCreateToken

func IssueDecode[T any](p0 *testing.T, p1 *httptest.ResponseRecorder, p2 int) T {
	return issueDecode[T](p0, p1, p2)
}

var IssueSeedDeployment = issueSeedDeployment
var IssueSend = issueSend
var Itoa = itoa
var KafkaSkipVerifyRequested = kafkaSkipVerifyRequested
var KeyPrefixFromHash = keyPrefixFromHash
var KeyedReplayProblem = keyedReplayProblem
var LifecycleObserverLinkProblem = lifecycleObserverLinkProblem
var LoadHostHMACKey = loadHostHMACKey
var LoadManagedPostgres = loadManagedPostgres
var LoadSessionManager = loadSessionManager
var LoadSourceRefManifest = loadSourceRefManifest
var LookupPlanCache = lookupPlanCache
var MakeFeatureFlagOutcomesResponse = makeFeatureFlagOutcomesResponse
var ManagedExecutionWorkflowStepLabel = managedExecutionWorkflowStepLabel
var ManagedPostgresAccountingDiagnosticsView = managedPostgresAccountingDiagnosticsView
var ManagedPostgresCapabilitiesView = managedPostgresCapabilitiesView
var ManagedPostgresConnectionURL = managedPostgresConnectionURL
var ManagedPostgresNotConfiguredProblem = managedPostgresNotConfiguredProblem
var ManagedPostgresPlanAllows = managedPostgresPlanAllows
var ManagedPostgresProblem = managedPostgresProblem
var ManagedPostgresSpecFromRequest = managedPostgresSpecFromRequest
var ManagedPostgresUsageCeilings = managedPostgresUsageCeilings
var ManagedPostgresUsageOperatorView = managedPostgresUsageOperatorView
var ManagedPostgresUsageView = managedPostgresUsageView
var ManagedPostgresView = managedPostgresView
var ManagedWorkflowCanConsumeFailedDependencies = managedWorkflowCanConsumeFailedDependencies
var MapReconcileError = mapReconcileError
var MarshalAppRetryPolicy = marshalAppRetryPolicy
var MillicentsAsCurrency = millicentsAsCurrency
var MintDashboardSession = mintDashboardSession
var MustCreateAlertRule = mustCreateAlertRule
var MustSeedApp = mustSeedApp
var MustSeedAppFor = mustSeedAppFor
var MustSeedDeployment = mustSeedDeployment
var MustSeedEdgeRuleApp = mustSeedEdgeRuleApp
var NearestRunningTelemetryRow = nearestRunningTelemetryRow
var NewAppSecretCredentialSink = newAppSecretCredentialSink
var NewAuditor = newAuditor
var NewAuthedDashboardServer = newAuthedDashboardServer
var NewAuthedDashboardServerFull = newAuthedDashboardServerFull
var NewCloneCoordinatorFixture = newCloneCoordinatorFixture
var NewForceHarness = newForceHarness
var NewGithubdClient = newGithubdClient
var NewGoogleIDTokenVerifier = newGoogleIDTokenVerifier
var NewInvocationCompletionWaiter = newInvocationCompletionWaiter
var NewIssueCreditEnv = newIssueCreditEnv
var NewMailerAdapter = newMailerAdapter
var NewObsEnv = newObsEnv
var NewObsPR3Env = newObsPR3Env
var NewPgBackupPushedSampler = newPgBackupPushedSampler
var NewPreviewJanitor = newPreviewJanitor
var NewProjectLifecycleFixture = newProjectLifecycleFixture
var NewRuntimeConfigManager = newRuntimeConfigManager

func (s *server) ConfigureHostingForTest(getenv func(string) string) error {
	return s.configureHosting(getenv)
}

var NewSafeReleaseLeaseMetrics = newSafeReleaseLeaseMetrics
var NewServer = newServer
var NewServerWithDeps = newServerWithDeps
var NewSourceRefTestServer = newSourceRefTestServer
var NewStatusMetrics = newStatusMetrics
var NewTestServerWithCapturingNotifier = newTestServerWithCapturingNotifier
var NormalizeBillingField = normalizeBillingField
var NormalizeCLIAuthURLBase = normalizeCLIAuthURLBase
var NormalizedEventsFrameForAccount = normalizedEventsFrameForAccount
var NullableAnalyticsInt = nullableAnalyticsInt
var OauthHTTPClient = oauthHTTPClient
var ObjectRegistry = objectRegistry
var ObserveSafeReleaseLease = observeSafeReleaseLease
var OpenAPIDocHarness = openAPIDocHarness
var OpenDevSourceArchive = openDevSourceArchive
var OwnedBucketCleanupSuite = ownedBucketCleanupSuite
var ParseAppDomainsPath = parseAppDomainsPath
var ParseAppErrorsPath = parseAppErrorsPath
var ParseAppInstancesPath = parseAppInstancesPath
var ParseAppJobsPath = parseAppJobsPath
var ParseAppLogDrainsPath = parseAppLogDrainsPath
var ParseAppLogsPath = parseAppLogsPath
var ParseAppMirrorsPath = parseAppMirrorsPath
var ParseAppPreAuthPath = parseAppPreAuthPath
var ParseAppStoragePath = parseAppStoragePath
var ParseAppTenantSurfacesPath = parseAppTenantSurfacesPath
var ParseAppWebhooksPath = parseAppWebhooksPath
var ParseAuditLogQuery = parseAuditLogQuery
var ParseDataUpstreamHistoryQuery = parseDataUpstreamHistoryQuery
var ParseDebugSinceFromString = parseDebugSinceFromString
var ParseDebugSinceStrict = parseDebugSinceStrict
var ParseDebugTelemetryFilters = parseDebugTelemetryFilters
var ParseDomainDoctorPath = parseDomainDoctorPath
var ParseEgressFlowFilter = parseEgressFlowFilter
var ParseImageDigest = parseImageDigest
var ParseIssueListFilter = parseIssueListFilter
var ParseRequestAnalyticsGroupBy = parseRequestAnalyticsGroupBy
var ParseRequestAnalyticsRouteFilter = parseRequestAnalyticsRouteFilter
var ParseRequestAnalyticsWindow = parseRequestAnalyticsWindow
var ParseScanMultipart = parseScanMultipart
var ParseSuspectedDependency = parseSuspectedDependency
var ParseSweepOlderThan = parseSweepOlderThan
var ParseSweepReason = parseSweepReason
var PathStaysUnder = pathStaysUnder
var PlainReplayProblem = plainReplayProblem
var PlanCanApplyReasons = planCanApplyReasons
var PolicyPtrFromReq = policyPtrFromReq
var PostStripeEvent = postStripeEvent
var PrepareMirrorReplayInvocation = prepareMirrorReplayInvocation
var PreviewHostnameFor = previewHostnameFor
var PreviewSlugOK = previewSlugOK
var ProblemCode = problemCode
var ProductionQueueBindingHandler = productionQueueBindingHandler
var ProfileSourceProvenance = profileSourceProvenance
var ProjectAsyncRoutesPlanProblem = projectAsyncRoutesPlanProblem
var ProjectCronsWithPreserved = projectCronsWithPreserved
var ProjectDashboardGitHubActivity = projectDashboardGitHubActivity
var ProjectDashboardOperationEvent = projectDashboardOperationEvent
var ProjectEnvironmentPromotionDeploymentReason = projectEnvironmentPromotionDeploymentReason
var ProjectEnvironmentPromotionHash = projectEnvironmentPromotionHash
var ProjectEnvironmentSecretOwner = projectEnvironmentSecretOwner
var ProjectObsDrainStatus = projectObsDrainStatus
var ProjectObsNodeApps = projectObsNodeApps
var ProjectRequest = projectRequest
var ProjectWorkloadCrons = projectWorkloadCrons
var ProjectedAppCount = projectedAppCount
var PromotionFixture = promotionFixture
var BindingPromotionDeadline = bindingPromotionDeadline
var PromtailMetricsTarget = promtailMetricsTarget
var PtrInt64 = ptrInt64
var QualifyObjectAccounting = qualifyObjectAccounting
var QueueBindingConsumerLiveness = queueBindingConsumerLiveness
var QueueBindingTriggerID = queueBindingTriggerID
var ReadOAuthBody = readOAuthBody
var ReadyObjectStorageVerificationBinding = readyObjectStorageVerificationBinding
var ReadyVerificationBinding = readyVerificationBinding
var ReconcileProductionEnvironment = reconcileProductionEnvironment
var ReissueWithMFAFlag = reissueWithMFAFlag
var ReleaseAppRestartClaim = releaseAppRestartClaim
var RenderRevision = renderRevision
var Repeat = repeat
var ReplayIdempotent = replayIdempotent
var ResendReason = resendReason
var ReserveRecoveryBucket = reserveRecoveryBucket
var ResolveManagedPostgresDatabaseRecord = resolveManagedPostgresDatabaseRecord
var ResolveSourceOperations = resolveSourceOperations
var ArchiveHasSourceRoot = archiveHasSourceRoot
var ArchiveHasRootDockerfileAtRoot = archiveHasRootDockerfileAtRoot
var RestartProgressDashboardCookie = restartProgressDashboardCookie
var RestartProgressDashboardGET = restartProgressDashboardGET
var RollbackOn5xxBreached = rollbackOn5xxBreached
var RouteCustomerHealthOptions = routeCustomerHealthOptions
var RouteCustomerWindow = routeCustomerWindow
var RoutePolicyInventory = routePolicyInventory
var RoutePolicyServerPlan = routePolicyServerPlan
var RunWithDeps = runWithDeps
var RuntimeReleaseResponse = runtimeReleaseResponse
var RuntimeUpgradePreview = runtimeUpgradePreview
var SafeReleasesAlertMetric = safeReleasesAlertMetric
var SafeReleasesAuditKind = safeReleasesAuditKind
var SanContains = sanContains
var ScanExtractedTreeSecrets = scanExtractedTreeSecrets
var ScanSourceTarballSecrets = scanSourceTarballSecrets
var ScopeGenericRootWorkload = scopeGenericRootWorkload
var ScrapeOpsMetrics = scrapeOpsMetrics
var SeedActivityPersonalOrg = seedActivityPersonalOrg
var SeedApp = seedApp
var SeedAppForAudit = seedAppForAudit
var SeedAppForTimeline = seedAppForTimeline
var SeedAppTaskDeployment = seedAppTaskDeployment
var SeedAuditLogRow = seedAuditLogRow
var SeedBindingCandidate = seedBindingCandidate
var SeedDeadLetterRow = seedDeadLetterRow
var SeedImport = seedImport
var SeedJob = seedJob
var SeedPGApp = seedPGApp
var SeedPreviewAppForTest = seedPreviewAppForTest
var SeedSharedOrgWithOwner = seedSharedOrgWithOwner
var SeedTenantSurface = seedTenantSurface
var SelectDebugReplayMirrorRule = selectDebugReplayMirrorRule
var SetS3Flag = setS3Flag
var Setup = setup
var SetupAlerts = setupAlerts
var SetupChangePlan = setupChangePlan
var SetupMW = setupMW
var SetupPGHandler = setupPGHandler
var SetupWithMFA = setupWithMFA
var SetupWithScopes = setupWithScopes
var SourceDeltaLimits = sourceDeltaLimits
var SourceRefAsyncRouteTestFixture = sourceRefAsyncRouteTestFixture
var SourceRefManifestNeedsRollback = sourceRefManifestNeedsRollback
var SpoolRoot = spoolRoot
var SseFanIn = sseFanIn
var StartSSEStream = startSSEStream
var StateManifestForUpdate = stateManifestForUpdate
var StateManifestFromAPI = stateManifestFromAPI
var StatePolicyToDTO = statePolicyToDTO
var StoreAsAuthenticator = storeAsAuthenticator
var StoreAsSessionLookup = storeAsSessionLookup
var StorePlanCache = storePlanCache
var StripeWebhookHarness = stripeWebhookHarness
var SummariseBetaFunnel = summariseBetaFunnel
var SummarizeDevSyncHistory = summarizeDevSyncHistory
var SummarizeRuntimePolicySchedulerStatus = summarizeRuntimePolicySchedulerStatus
var SummarizeRuntimePolicyStatus = summarizeRuntimePolicyStatus
var SuspectedDependency = suspectedDependency
var SuspectedDependencyFor = suspectedDependencyFor
var SweepExpiredCacheEntries = sweepExpiredCacheEntries
var SweepUploadSessions = sweepUploadSessions
var TcpListenerTLSStatusResponse = tcpListenerTLSStatusResponse
var TenantWorkflowScheduleResponse = tenantWorkflowScheduleResponse
var TestLogger = testLogger
var ToComputeNodeResponse = toComputeNodeResponse
var ToPlanDetectionWarnings = toPlanDetectionWarnings
var ToPlanWorkload = toPlanWorkload
var ToProjectPreviewAffected = toProjectPreviewAffected
var ToProjectPreviewAsyncRoutes = toProjectPreviewAsyncRoutes
var TriggerResponse = triggerResponse
var UnsealAlertRuleWebhookSecret = unsealAlertRuleWebhookSecret
var UsageAppData = usageAppData
var UsageDailyPoints = usageDailyPoints
var UsageDailyRowsForMonth = usageDailyRowsForMonth
var UuidFromPgtype = uuidFromPgtype
var V1AuthJSONRequest = v1AuthJSONRequest
var V1AuthTestHarness = v1AuthTestHarness
var ValidDataUpstreamHistoryRegion = validDataUpstreamHistoryRegion
var ValidProjectBranch = validProjectBranch
var ValidProjectRepoFullName = validProjectRepoFullName
var ValidSlug = validSlug
var ValidateAndDeriveEnablePresetOpts = validateAndDeriveEnablePresetOpts
var ValidateAnnotationForm = validateAnnotationForm
var ValidateCapturedProjectEnvironmentDatabaseResources = validateCapturedProjectEnvironmentDatabaseResources
var ValidateCapturedProjectEnvironmentObjectResources = validateCapturedProjectEnvironmentObjectResources
var ValidateCloneCoordinatorInventory = validateCloneCoordinatorInventory
var ValidateOverrides = validateOverrides
var ValidateProjectEnvironmentPromotionResume = validateProjectEnvironmentPromotionResume
var ValidateScannedSchedules = validateScannedSchedules
var ValidateUpdateApp = validateUpdateApp
var WakeTimelineWindow = wakeTimelineWindow
var WithBillingKeysForTest = withBillingKeysForTest
var WithDomainDoctorDisabled = withDomainDoctorDisabled
var WithDomainDoctorEnabled = withDomainDoctorEnabled
var WithSeams = withSeams
var WithTenantSurfacesEnabled = withTenantSurfacesEnabled
var WithTestHMACFiles = withTestHMACFiles
var WithTestIdentities = withTestIdentities
var WithTestMailTransport = withTestMailTransport
var WithTestRecipient = withTestRecipient
var WithTestSidecarRecipient = withTestSidecarRecipient
var WithTestStore = withTestStore
var WorkflowScheduleResponses = workflowScheduleResponses
var WriteDashboardBadRequest = writeDashboardBadRequest
var WriteDashboardUnauthorized = writeDashboardUnauthorized
var WriteDebugTelemetryCSV = writeDebugTelemetryCSV
var WriteDebugTelemetryNDJSON = writeDebugTelemetryNDJSON
var WriteJSON = writeJSON
var WriteListAuditLogResponse = writeListAuditLogResponse

// Members of accountMetricsSnapshot
func (r *accountMetricsSnapshot) ColdBootsErr() *error { return &r.coldBootsErr }
func (r *accountMetricsSnapshot) LatencyErr() *error   { return &r.latencyErr }
func (r *accountMetricsSnapshot) RequestsErr() *error  { return &r.requestsErr }

// Members of adminAllowlist
func (r *adminAllowlist) Emails() *map[string]struct{} { return &r.emails }

// Members of appSecretCredentialSink
func (r *appSecretCredentialSink) Identities() *func() []*age.X25519Identity { return &r.identities }
func (r *appSecretCredentialSink) Probe() *func(context.Context, string, managedpostgres.CredentialAccess, int) error {
	return &r.probe
}

// Members of auditor
func (r *auditor) CloseSignal() *chan struct{}    { return &r.closeSignal }
func (r *auditor) FailedCh() *chan failedLoginRow { return &r.failedCh }
func (r *auditor) FailedOps() *auditFailedOps     { return &r.failedOps }
func (r *auditor) FlushBatch() *int               { return &r.flushBatch }
func (r *auditor) FlushEvery() *time.Duration     { return &r.flushEvery }
func (r *auditor) Ops() *auditOps                 { return &r.ops }

// Members of buildPlanCache
func (r *buildPlanCache) Entries() *map[string]*buildPlanCacheEntry { return &r.entries }
func (r *buildPlanCache) Mu() *sync.Mutex                           { return &r.mu }

// Members of buildPlanCacheEntry
func (r *buildPlanCacheEntry) Mtime() *time.Time { return &r.mtime }

// Members of cachedSource
func (r *cachedSource) ExpiresAt() *time.Time { return &r.expiresAt }

// Members of captureNotifier
func (r *captureNotifier) ByChannel(p0 string) []captured { return r.byChannel(p0) }

// Members of captured
func (r *captured) Payload() *string { return &r.payload }

// Members of capturedProjectEnvironmentDatabasePlan
func (r *capturedProjectEnvironmentDatabasePlan) Hash() *string                     { return &r.hash }
func (r *capturedProjectEnvironmentDatabasePlan) Name() *string                     { return &r.name }
func (r *capturedProjectEnvironmentDatabasePlan) Source() *managedpostgres.Database { return &r.source }

// Members of capturedProjectEnvironmentObjectPlan
func (r *capturedProjectEnvironmentObjectPlan) Source() *state.ProjectEnvironmentCloneObjectBucket {
	return &r.source
}

// Members of capturingNotifier
func (r *capturingNotifier) Emitted() *[]capturedNotification { return &r.emitted }
func (r *capturingNotifier) Mu() *sync.Mutex                  { return &r.mu }

// Members of cloneCaptureBarrierObservation
func (r *cloneCaptureBarrierObservation) ConfigurationFence() *state.ProjectEnvironmentCloneConfigurationFence {
	return &r.configurationFence
}
func (r *cloneCaptureBarrierObservation) InstrumentedWritersDrained() *bool {
	return &r.instrumentedWritersDrained
}

// Members of cloneCheckpointClosureProvider
func (r *cloneCheckpointClosureProvider) Closes() *int   { return &r.closes }
func (r *cloneCheckpointClosureProvider) Drained() *bool { return &r.drained }
func (r *cloneCheckpointClosureProvider) OnDiscover() *func(context.Context) error {
	return &r.onDiscover
}
func (r *cloneCheckpointClosureProvider) Roles() *int { return &r.roles }

// Members of cloneCoordinatorFailureStore
func (r *cloneCoordinatorFailureStore) LoseCaptureAck() *bool { return &r.loseCaptureAck }

// Members of cloneCoordinatorFixture
func (r *cloneCoordinatorFixture) Apps() *[]state.App                         { return &r.apps }
func (r *cloneCoordinatorFixture) Lease() *state.ProjectEnvironmentCloneLease { return &r.lease }
func (r *cloneCoordinatorFixture) Pool() **pgxpool.Pool                       { return &r.pool }
func (r *cloneCoordinatorFixture) Srv() **server                              { return &r.srv }
func (r *cloneCoordinatorFixture) Store() **cloneCoordinatorFailureStore      { return &r.store }
func (r cloneCoordinatorFixture) Targets(p0 *testing.T) []state.ProjectEnvironmentCloneWorkload {
	return r.targets(p0)
}

// Members of debugTelemetryCursor
func (r debugTelemetryCursor) Filters() debugTelemetryCursorFilters { return r.filters() }

// Members of debugTelemetryFilters
func (r debugTelemetryFilters) Cursor() debugTelemetryCursorFilters      { return r.cursor() }
func (r debugTelemetryFilters) Same(p0 debugTelemetryCursorFilters) bool { return r.same(p0) }
func (r debugTelemetryFilters) SqlColdBootFilter() int32                 { return r.sqlColdBootFilter() }
func (r debugTelemetryFilters) SqlConsumerAnonymous() bool               { return r.sqlConsumerAnonymous() }
func (r debugTelemetryFilters) SqlConsumerID() string                    { return r.sqlConsumerID() }

// Members of devPatchPlan
func (r *devPatchPlan) Preview() **api.DevPatchPreview { return &r.preview }

// Members of fakeObjectProvider
func (r *fakeObjectProvider) Accessed() *[]string           { return &r.accessed }
func (r *fakeObjectProvider) MultipartAborted() *[]string   { return &r.multipartAborted }
func (r *fakeObjectProvider) MultipartCompleted() *[]string { return &r.multipartCompleted }
func (r *fakeObjectProvider) MultipartConditions() *[]api.ObjectWriteConditions {
	return &r.multipartConditions
}
func (r *fakeObjectProvider) MultipartErr() *error { return &r.multipartErr }
func (r *fakeObjectProvider) MultipartParts() *objectstorage.MultipartPartsPage {
	return &r.multipartParts
}
func (r *fakeObjectProvider) Objects() *map[string][]byte { return &r.objects }

// Members of googleJWKSVerifier
func (r *googleJWKSVerifier) JwksURL() *string { return &r.jwksURL }

// Members of invocationCompletionWaiter
func (r *invocationCompletionWaiter) Mu() *sync.Mutex { return &r.mu }
func (r *invocationCompletionWaiter) Waiters() *map[string]map[chan struct{}]struct{} {
	return &r.waiters
}
func (r *invocationCompletionWaiter) CompleteFromPayload(p0 string) { r.completeFromPayload(p0) }

// Members of mfaTestEnv
func (r *mfaTestEnv) Acct() *state.Account    { return &r.acct }
func (r *mfaTestEnv) Cookie() **http.Cookie   { return &r.cookie }
func (r *mfaTestEnv) H() *http.Handler        { return &r.h }
func (r *mfaTestEnv) Srv() **server           { return &r.srv }
func (r *mfaTestEnv) Store() **state.MemStore { return &r.store }
func (r mfaTestEnv) GenerateEnrolledAccount(p0 *testing.T) ([]string, string, *http.Cookie) {
	return r.generateEnrolledAccount(p0)
}
func (r mfaTestEnv) MfaIssueWithPending(p0 *testing.T, p1 bool) *http.Cookie {
	return r.mfaIssueWithPending(p0, p1)
}

// Members of pgBackupPushedSampler
func (r *pgBackupPushedSampler) Run(p0 context.Context) { r.run(p0) }
func (r *pgBackupPushedSampler) Tick()                  { r.tick() }

// Members of pgHandlerEnv
func (r *pgHandlerEnv) Acct() *state.Account                           { return &r.acct }
func (r *pgHandlerEnv) H() *http.Handler                               { return &r.h }
func (r *pgHandlerEnv) Key() *string                                   { return &r.key }
func (r *pgHandlerEnv) Pool() **pgxpool.Pool                           { return &r.pool }
func (r *pgHandlerEnv) S() **server                                    { return &r.s }
func (r *pgHandlerEnv) Store() *state.Store                            { return &r.store }
func (r pgHandlerEnv) AddAdminSession(p0 *testing.T, p1 *http.Request) { r.addAdminSession(p0, p1) }
func (r pgHandlerEnv) Do(p0 *testing.T, p1 string, p2 string, p3 any, p4 map[string]string) *httptest.ResponseRecorder {
	return r.do(p0, p1, p2, p3, p4)
}

// Members of previewJanitor
func (r *previewJanitor) SweepOnce(p0 context.Context) error            { return r.sweepOnce(p0) }
func (r *previewJanitor) WithClock(p0 func() time.Time) *previewJanitor { return r.withClock(p0) }
func (r *previewJanitor) WithResourceCleanup(p0 func(context.Context, state.App) error) *previewJanitor {
	return r.withResourceCleanup(p0)
}

// Members of recordingMailer
func (r *recordingMailer) Snapshot() []Message { return r.snapshot() }

// Members of runDeps
func (r *runDeps) CaptureDialTLS() *func(name string, cfg *tls.Config)              { return &r.captureDialTLS }
func (r *runDeps) Config() **Config                                                 { return &r.config }
func (r *runDeps) Listen() *func(network string, addr string) (net.Listener, error) { return &r.listen }
func (r *runDeps) PreLoadedConfig() **Config                                        { return &r.preLoadedConfig }
func (r *runDeps) PreLoadedNodeVerifier() *wire.NodeVerifier                        { return &r.preLoadedNodeVerifier }

// Members of runtimeConfigManager
func (r *runtimeConfigManager) Mu() *sync.RWMutex                         { return &r.mu }
func (r *runtimeConfigManager) Versions() *map[string]int64               { return &r.versions }
func (r *runtimeConfigManager) Apply(p0 string, p1 json.RawMessage) error { return r.apply(p0, p1) }
func (r *runtimeConfigManager) ApplyVersion(p0 string, p1 json.RawMessage, p2 int64) (bool, error) {
	return r.applyVersion(p0, p1, p2)
}
func (r *runtimeConfigManager) Reconcile(p0 context.Context, p1 state.Store) error {
	return r.reconcile(p0, p1)
}

// Members of runtimeConfigRestartNotifier
func (r *runtimeConfigRestartNotifier) Channel() *string { return &r.channel }
func (r *runtimeConfigRestartNotifier) Payload() *string { return &r.payload }

// Members of safeReleaseLeaseMetrics
func (r *safeReleaseLeaseMetrics) CheckSuccess() *prometheus.Gauge { return &r.checkSuccess }
func (r *safeReleaseLeaseMetrics) LastCheck() *prometheus.Gauge    { return &r.lastCheck }
func (r *safeReleaseLeaseMetrics) Ready() *prometheus.Gauge        { return &r.ready }
func (r *safeReleaseLeaseMetrics) SecondsLeft() *prometheus.Gauge  { return &r.secondsLeft }

// Members of server
func (r *server) AdminAllowlist() **adminAllowlist                   { return &r.adminAllowlist }
func (r *server) AiGatewayEnabled() *bool                            { return &r.aiGatewayEnabled }
func (r *server) AppEvents() *appevents.Service                      { return &r.appEvents }
func (r *server) Audit() **auditor                                   { return &r.audit }
func (r *server) DevBridgeEnabled() *bool                            { return &r.devBridgeEnabled }
func (r *server) DevBridgeURL() *string                              { return &r.devBridgeURL }
func (r *server) Domain() *string                                    { return &r.domain }
func (r *server) Durable() *durable.Service                          { return &r.durable }
func (r *server) EdgeRules() *edgerules.Service                      { return &r.edgeRules }
func (r *server) FeatureFlagsEnabled() *bool                         { return &r.featureFlagsEnabled }
func (r *server) FlagsWorkloadVerifier() **workloadidentity.Verifier { return &r.flagsWorkloadVerifier }
func (r *server) WorkloadVerifier() **workloadidentity.Verifier      { return &r.workloadVerifier }
func (r *server) Log() **slog.Logger                                 { return &r.log }
func (r *server) MailBounce() *mailBounceHandler                     { return &r.mailBounce }
func (r *server) Mailer() *Mailer                                    { return &r.mailer }
func (r *server) ManagedPostgres() **managedpostgres.Service         { return &r.managedPostgres }
func (r *server) ManagedPostgresBindings() **managedpostgres.BindingService {
	return &r.managedPostgresBindings
}
func (r *server) ManagedPostgresCutovers() **managedpostgres.CutoverService {
	return &r.managedPostgresCutovers
}
func (r *server) Notif() *Notifier                              { return &r.notif }
func (r *server) Object() *object.Service                       { return &r.object }
func (r *server) ObjectStorage() **objectstorage.Registry       { return &r.objectStorage }
func (r *server) Operation() *operation.Service                 { return &r.operation }
func (r *server) Ops() **wire.OpsMetrics                        { return &r.ops }
func (r *server) OutboundProbeGatewayURL() *string              { return &r.outboundProbeGatewayURL }
func (r *server) ProfileBackend() *profiling.Backend            { return &r.profileBackend }
func (r *server) ProfileQuerySlots() *chan struct{}             { return &r.profileQuerySlots }
func (r *server) PromqlClient() **promql.Client                 { return &r.promqlClient }
func (r *server) Realtime() *managedrealtime.Service            { return &r.realtime }
func (r *server) ResendWebhookSecret() *string                  { return &r.resendWebhookSecret }
func (r *server) RuntimeConfig() **runtimeConfigManager         { return &r.runtimeConfig }
func (r *server) Sessions() **session.Manager                   { return &r.sessions }
func (r *server) StatementInvoices() *statementinvoices.Service { return &r.statementInvoices }
func (r *server) BillingRuns() *billingruns.Service             { return &r.billingRuns }
func (r *server) ConsumerBilling() *consumerbilling.Service     { return &r.consumerBilling }
func (r *server) ConsumerCredits() *consumercredits.Service     { return &r.consumerCredits }
func (r *server) ConsumerControls() *consumercontrols.Service   { return &r.consumerControls }
func (r *server) TenantPlans() *tenantplans.Service             { return &r.tenantPlans }
func (r *server) Store() *state.Store                           { return &r.store }
func (r *server) StripeWebhookSecret() *string                  { return &r.stripeWebhookSecret }
func (r *server) WorkflowRuntimeEnabled() *bool                 { return &r.workflowRuntimeEnabled }
func (r *server) AbandonProjectEnvironmentCloneConfigurationFence(p0 context.Context, p1 state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneLease, error) {
	return r.abandonProjectEnvironmentCloneConfigurationFence(p0, p1)
}
func (r *server) AdvanceCanaryByWorker(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.advanceCanaryByWorker(p0, p1, p2)
}
func (r *server) AlertRollbackSweep(p0 context.Context) error           { return r.alertRollbackSweep(p0) }
func (r *server) AppResponse(p0 state.App, p1 api.Plan) api.AppResponse { return r.appResponse(p0, p1) }
func (r *server) AppResponseWithContext(p0 context.Context, p1 state.App, p2 api.Plan) api.AppResponse {
	return r.appResponseWithContext(p0, p1, p2)
}
func (r *server) AppTCPListenerTLSStatus(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.appTCPListenerTLSStatus(p0, p1, p2)
}
func (r *server) ApplyDeploymentEnvironment(p0 context.Context, p1 state.Account, p2 state.App, p3 *api.CreateDeploymentRequest) *api.Problem {
	return r.applyDeploymentEnvironment(p0, p1, p2, p3)
}
func (r *server) ApplyRouteMonitorRollback(p0 context.Context, p1 state.RouteMonitorTarget) {
	r.applyRouteMonitorRollback(p0, p1)
}
func (r *server) ApplySourceRefManifest(p0 context.Context, p1 state.Account, p2 state.App, p3 *gregalemanifest.Manifest, p4 string, p5 bool) (sourceRefManifestStaged, *api.Problem) {
	return r.applySourceRefManifest(p0, p1, p2, p3, p4, p5)
}
func (r *server) AssessProfileDeploymentCheck(p0 context.Context, p1 state.ProfileDeploymentCheckWork) (api.ProfileRegressionAssessment, bool) {
	return r.assessProfileDeploymentCheck(p0, p1)
}
func (r *server) AuditAutomationMutation(p0 *http.Request, p1 state.Account, p2 string, p3 string, p4 state.AutomationMutation, p5 state.Automation) {
	r.auditAutomationMutation(p0, p1, p2, p3, p4, p5)
}
func (r *server) AutoAdvanceFeatureFlagRollout(p0 context.Context, p1 state.FeatureFlagAutoRolloutCandidate, p2 time.Time) error {
	return r.autoAdvanceFeatureFlagRollout(p0, p1, p2)
}
func (r *server) BillingPortalURLForProvider(p0 context.Context, p1 state.Account) string {
	return r.billingPortalURLForProvider(p0, p1)
}
func (r *server) BuildProjectEnvironmentPromotionPlan(p0 context.Context, p1 state.Account, p2 string, p3 string, p4 string, p5 bool) (projectEnvironmentPromotionPlan, *api.Problem) {
	return r.buildProjectEnvironmentPromotionPlan(p0, p1, p2, p3, p4, p5)
}
func (r *server) BuildProjectEnvironmentPromotionResumePlan(p0 context.Context, p1 state.Account, p2 state.ProjectEnvironmentPromotion, p3 []state.ProjectEnvironmentPromotionWorkload) (projectEnvironmentPromotionPlan, *api.Problem) {
	return r.buildProjectEnvironmentPromotionResumePlan(p0, p1, p2, p3)
}
func (r *server) BuilderdMetricsDiscovery(p0 http.ResponseWriter, p1 *http.Request) {
	r.builderdMetricsDiscovery(p0, p1)
}
func (r *server) CancelPendingWork(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.cancelPendingWork(p0, p1, p2)
}
func (r *server) CaptureProjectEnvironmentValues(p0 context.Context, p1 state.Account, p2 state.Project, p3 string) ([]state.App, state.ProjectEnvironmentCloneValuesSnapshot, error) {
	return r.captureProjectEnvironmentValues(p0, p1, p2, p3)
}
func (r *server) CheckedRollbackSweep(p0 context.Context) error { return r.checkedRollbackSweep(p0) }
func (r *server) ConfigureAIGateway(p0 Config, p1 func(string) string) error {
	return r.configureAIGateway(p0, p1)
}
func (r *server) ConfigureDurableEntities(p0 context.Context, p1 func(string) string) error {
	return r.configureDurableEntities(p0, p1)
}
func (r *server) ConfigureOperations(p0 Config, p1 func(string) string) error {
	return r.configureOperations(p0, p1)
}
func (r *server) ConfigureQueueWorkload(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.configureQueueWorkload(p0, p1, p2)
}
func (r *server) CountAccountConsumerKeys(p0 context.Context, p1 string) (int, error) {
	return r.countAccountConsumerKeys(p0, p1)
}
func (r *server) CountConsumerKeys(p0 context.Context, p1 string, p2 string) (int, error) {
	return r.countConsumerKeys(p0, p1, p2)
}
func (r *server) CreateFinancialBudget(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.createFinancialBudget(p0, p1, p2)
}
func (r *server) CreateFullProjectEnvironmentClone(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.createFullProjectEnvironmentClone(p0, p1, p2)
}
func (r *server) CreateImageRun(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.createImageRun(p0, p1, p2)
}
func (r *server) CreateProjectEnvironment(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.createProjectEnvironment(p0, p1, p2)
}
func (r *server) CreateProjectEnvironmentQualification(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.createProjectEnvironmentQualification(p0, p1, p2)
}
func (r *server) CreateQueueBinding(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.createQueueBinding(p0, p1, p2)
}
func (r *server) DashboardCheckProfileRegression(p0 http.ResponseWriter, p1 *http.Request) {
	r.dashboardCheckProfileRegression(p0, p1)
}
func (r *server) DashboardSaveProfileDeploymentPolicy(p0 http.ResponseWriter, p1 *http.Request) {
	r.dashboardSaveProfileDeploymentPolicy(p0, p1)
}
func (r *server) DashboardSaveProfileInvestigation(p0 http.ResponseWriter, p1 *http.Request) {
	r.dashboardSaveProfileInvestigation(p0, p1)
}
func (r *server) DeleteProjectEnvironment(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.deleteProjectEnvironment(p0, p1, p2)
}
func (r *server) DeleteQueueBinding(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.deleteQueueBinding(p0, p1, p2)
}
func (r *server) DeleteWorkPolicy(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.deleteWorkPolicy(p0, p1, p2)
}
func (r *server) DeploymentResponse(p0 context.Context, p1 state.Deployment, p2 state.App) api.DeploymentResponse {
	return r.deploymentResponse(p0, p1, p2)
}
func (r *server) DeploymentResponseWithBuild(p0 context.Context, p1 state.Deployment, p2 state.App) api.DeploymentResponse {
	return r.deploymentResponseWithBuild(p0, p1, p2)
}
func (r *server) DiffProjectEnvironment(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.diffProjectEnvironment(p0, p1, p2)
}
func (r *server) DrainAutomaticRouteChecks(p0 context.Context) (int, error) {
	return r.drainAutomaticRouteChecks(p0)
}
func (r *server) DrainRouteMonitors(p0 context.Context) (int, error) { return r.drainRouteMonitors(p0) }
func (r *server) DriveManagedExecutionWorkflow(p0 context.Context, p1 state.ExecutionWorkflowJobClaim) error {
	return r.driveManagedExecutionWorkflow(p0, p1)
}
func (r *server) EnqueueRetry(p0 context.Context, p1 state.App, p2 state.Deployment, p3 state.StageName) (state.Deployment, error) {
	return r.enqueueRetry(p0, p1, p2, p3)
}
func (r *server) EnsureProjectEnvironmentObjectStorageCloneBucket(p0 context.Context, p1 state.Account, p2 string, p3 projectEnvironmentBindingClone, p4 *[]func(context.Context) error) (state.ObjectBucket, error) {
	return r.ensureProjectEnvironmentObjectStorageCloneBucket(p0, p1, p2, p3, p4)
}
func (r *server) FetchAccountSLO(p0 context.Context, p1 state.Account, p2 string) (api.AccountSLOResponse, string) {
	return r.fetchAccountSLO(p0, p1, p2)
}
func (r *server) FetchAppSLO(p0 context.Context, p1 state.App, p2 state.Account, p3 string) (api.AppSLOResponse, string) {
	return r.fetchAppSLO(p0, p1, p2, p3)
}
func (r *server) GetActiveProjectReleaseSet(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.getActiveProjectReleaseSet(p0, p1, p2)
}
func (r *server) GetApp(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.getApp(p0, p1, p2)
}
func (r *server) GetDevBridgeActivity(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.getDevBridgeActivity(p0, p1, p2)
}
func (r *server) GetManagedPostgresDatabase(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.getManagedPostgresDatabase(p0, p1, p2)
}
func (r *server) GetProjectEnvironmentApprovalStatus(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.getProjectEnvironmentApprovalStatus(p0, p1, p2)
}
func (r *server) GetProjectEnvironmentCloneOperation(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.getProjectEnvironmentCloneOperation(p0, p1, p2)
}
func (r *server) GetProjectEnvironmentPromotionStatus(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.getProjectEnvironmentPromotionStatus(p0, p1, p2)
}
func (r *server) GetProjectEnvironmentQueueBindings(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.getProjectEnvironmentQueueBindings(p0, p1, p2)
}
func (r *server) GetProjectEnvironmentReleases(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.getProjectEnvironmentReleases(p0, p1, p2)
}
func (r *server) GetProjectEnvironmentState(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.getProjectEnvironmentState(p0, p1, p2)
}
func (r *server) GetProjectReleaseSet(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.getProjectReleaseSet(p0, p1, p2)
}
func (r *server) GetQueueBinding(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.getQueueBinding(p0, p1, p2)
}
func (r *server) GetQueueBindingStatus(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.getQueueBindingStatus(p0, p1, p2)
}
func (r *server) HandleBillingEventWithOptions(p0 context.Context, p1 billing.Event, p2 state.Account, p3 bool) error {
	return r.handleBillingEventWithOptions(p0, p1, p2, p3)
}
func (r *server) Handler() http.Handler { return r.handler() }

func (r *server) GitOpsDashboardRequestForTest(request *http.Request) (any, string, error) {
	body, _, method, err := r.environmentGitOpsDashboardRequest(request)
	return body, method, err
}
func (r *server) GitOpsPreviewArchiveForTest(t *testing.T, definition, resolvedSHA string) {
	r.githubd = &environmentGitOpsClient{repositories: []Repo{{ID: 123, FullName: "example/shop"}},
		archive: environmentGitOpsArchiveDefinition(t, definition), resolvedSHA: resolvedSHA}
}
func (r *server) Idempotent(p0 accountHandler) accountHandler { return r.idempotent(p0) }
func (r *server) ImagedMetricsDiscovery(p0 http.ResponseWriter, p1 *http.Request) {
	r.imagedMetricsDiscovery(p0, p1)
}
func (r *server) InvalidateAppSnapshots(p0 context.Context, p1 string) (int, error) {
	return r.invalidateAppSnapshots(p0, p1)
}
func (r *server) IssueProjectEnvironmentPromotionApproval(p0 context.Context, p1 state.Account, p2 string, p3 string, p4 string) (string, state.ProjectEnvironmentApproval, *api.Problem) {
	return r.issueProjectEnvironmentPromotionApproval(p0, p1, p2, p3, p4)
}
func (r *server) ListEgressFlows(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.listEgressFlows(p0, p1, p2)
}
func (r *server) ListJobs(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.listJobs(p0, p1, p2)
}
func (r *server) ListManagedPostgresDatabases(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.listManagedPostgresDatabases(p0, p1, p2)
}
func (r *server) ListProjectEnvironmentPromotions(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.listProjectEnvironmentPromotions(p0, p1, p2)
}
func (r *server) ListProjectReleaseSets(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.listProjectReleaseSets(p0, p1, p2)
}
func (r *server) ListQueueBindings(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.listQueueBindings(p0, p1, p2)
}
func (r *server) ListWorkPolicies(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.listWorkPolicies(p0, p1, p2)
}
func (r *server) LoadAndGateAlertPreset(p0 context.Context, p1 state.Account, p2 string) (state.AlertPreset, *api.Problem) {
	return r.loadAndGateAlertPreset(p0, p1, p2)
}
func (r *server) LoadAndResolveProjectManifest(p0 context.Context, p1 state.Account, p2 string, p3 []string, p4 string) (resolvedProjectManifest, *api.Problem) {
	return r.loadAndResolveProjectManifest(p0, p1, p2, p3, p4)
}
func (r *server) LoadJobManagedObject(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account, p3 string) (state.ObjectBucket, objectstorage.Provider, string, bool) {
	return r.loadJobManagedObject(p0, p1, p2, p3)
}
func (r *server) LoadProjectEnvironmentState(p0 context.Context, p1 state.Account, p2 string, p3 string) (api.ProjectEnvironmentStateResponse, *api.Problem) {
	return r.loadProjectEnvironmentState(p0, p1, p2, p3)
}
func (r *server) MetricsDiscoveryHandler() http.Handler { return r.metricsDiscoveryHandler() }
func (r *server) MountInternalSafeDeploy(p0 *http.ServeMux, p1 string, p2 string, p3 string) error {
	return r.mountInternalSafeDeploy(p0, p1, p2, p3)
}
func (r *server) NodeMetricsDiscovery(p0 http.ResponseWriter, p1 *http.Request) {
	r.nodeMetricsDiscovery(p0, p1)
}
func (r *server) PlanDevPatch(p0 context.Context, p1 state.App, p2 string, p3 string, p4 api.Limits) devPatchPlan {
	return r.planDevPatch(p0, p1, p2, p3, p4)
}
func (r *server) PlanProjectEnvironmentBindingClones(p0 context.Context, p1 state.Account, p2 []state.App, p3 map[string]string, p4 bool) ([]projectEnvironmentBindingClone, error) {
	return r.planProjectEnvironmentBindingClones(p0, p1, p2, p3, p4)
}
func (r *server) PrepareProjectEnvironmentCloneStableCaptureBarriers(p0 context.Context, p1 state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneLease, cloneCaptureBarrierObservation, error) {
	return r.prepareProjectEnvironmentCloneStableCaptureBarriers(p0, p1)
}
func (r *server) PreviewProjectEnvironmentPromotion(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.previewProjectEnvironmentPromotion(p0, p1, p2)
}
func (r *server) ProcessNextProjectEnvironmentClone(p0 context.Context, p1 projectEnvironmentCloneCoordinatorStore) error {
	return r.processNextProjectEnvironmentClone(p0, p1)
}
func (r *server) ProcessProjectEnvironmentCloneLease(p0 context.Context, p1 projectEnvironmentCloneCoordinatorStore, p2 state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneLease, error) {
	return r.processProjectEnvironmentCloneLease(p0, p1, p2)
}
func (r *server) ProfileInvestigationResponse(p0 context.Context, p1 state.Account, p2 state.App, p3 api.ProfileInvestigation) api.ProfileInvestigationResponse {
	return r.profileInvestigationResponse(p0, p1, p2, p3)
}
func (r *server) PromoteProjectEnvironment(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.promoteProjectEnvironment(p0, p1, p2)
}
func (r *server) PublishProjectReleaseSet(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.publishProjectReleaseSet(p0, p1, p2)
}
func (r *server) PurgeAppCache(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.purgeAppCache(p0, p1, p2)
}
func (r *server) RealtimedMetricsDiscovery(p0 http.ResponseWriter, p1 *http.Request) {
	r.realtimedMetricsDiscovery(p0, p1)
}
func (r *server) RecordDevPatchSource(p0 context.Context, p1 state.App, p2 string, p3 string, p4 string, p5 devPatchPlan) {
	r.recordDevPatchSource(p0, p1, p2, p3, p4, p5)
}
func (r *server) RedirectToGitHubAuthorization(p0 http.ResponseWriter, p1 *http.Request, p2 int64) bool {
	return r.redirectToGitHubAuthorization(p0, p1, p2)
}
func (r *server) ReleaseAccountAbuseHold(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.releaseAccountAbuseHold(p0, p1, p2)
}
func (r *server) RenderAppPreAuth(p0 http.ResponseWriter, p1 *http.Request, p2 *slog.Logger, p3 state.Account, p4 string) {
	r.renderAppPreAuth(p0, p1, p2, p3, p4)
}
func (r *server) RenderAppProfiles(p0 http.ResponseWriter, p1 *http.Request, p2 *slog.Logger, p3 state.Account, p4 string) {
	r.renderAppProfiles(p0, p1, p2, p3, p4)
}
func (r *server) RenderAuthCapabilities(p0 http.ResponseWriter, p1 *http.Request) {
	r.renderAuthCapabilities(p0, p1)
}
func (r *server) RenderDevBridgesDashboard(p0 http.ResponseWriter, p1 *http.Request) {
	r.renderDevBridgesDashboard(p0, p1)
}
func (r *server) RenderDeveloperEnvironments(p0 http.ResponseWriter, p1 *http.Request, p2 *slog.Logger, p3 state.Account) {
	r.renderDeveloperEnvironments(p0, p1, p2, p3)
}
func (r *server) RenderPreviewsList(p0 http.ResponseWriter, p1 *http.Request, p2 *slog.Logger, p3 state.Account) {
	r.renderPreviewsList(p0, p1, p2, p3)
}
func (r *server) ReplaceProjectEnvironmentQueueBindings(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.replaceProjectEnvironmentQueueBindings(p0, p1, p2)
}
func (r *server) RequireOperator(p0 accountHandler) accountHandler       { return r.requireOperator(p0) }
func (r *server) ResendWebhook(p0 http.ResponseWriter, p1 *http.Request) { r.resendWebhook(p0, p1) }
func (r *server) ResolveOnBehalfOf(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account, p3 string) (*state.Account, bool) {
	return r.resolveOnBehalfOf(p0, p1, p2, p3)
}
func (r *server) RetryOperationDefinitions(p0 context.Context, p1 state.App, p2 state.Deployment) ([]api.OperationDefinitionSpec, error) {
	return r.retryOperationDefinitions(p0, p1, p2)
}
func (r *server) RevokeDevBridgeDashboard(p0 http.ResponseWriter, p1 *http.Request) {
	r.revokeDevBridgeDashboard(p0, p1)
}
func (r *server) RollbackOn5xxSweep(p0 context.Context, p1 time.Time) error {
	return r.rollbackOn5xxSweep(p0, p1)
}
func (r *server) RollbackProjectEnvironmentPromotion(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.rollbackProjectEnvironmentPromotion(p0, p1, p2)
}
func (r *server) RollbackSourceRefManifest(p0 context.Context, p1 sourceRefManifestStaged) error {
	return r.rollbackSourceRefManifest(p0, p1)
}
func (r *server) RotateManagedPostgresBinding(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.rotateManagedPostgresBinding(p0, p1, p2)
}
func (r *server) RuleDestinations(p0 context.Context, p1 state.AlertRule) string {
	return r.ruleDestinations(p0, p1)
}
func (r *server) CreateManagedPostgresBinding(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.createManagedPostgresBinding(p0, p1, p2)
}
func (r *server) SaveOwnedProfileInvestigation(p0 context.Context, p1 state.Account, p2 state.App, p3 state.ProfileInvestigationStore, p4 string, p5 api.SaveProfileInvestigationRequest) (api.ProfileInvestigation, *api.Problem) {
	return r.saveOwnedProfileInvestigation(p0, p1, p2, p3, p4, p5)
}
func (r *server) SavedProfilePage(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account, p3 state.App) (profilePageData, *http.Request, bool) {
	return r.savedProfilePage(p0, p1, p2, p3)
}
func (r *server) ScheddMetricsDiscovery(p0 http.ResponseWriter, p1 *http.Request) {
	r.scheddMetricsDiscovery(p0, p1)
}
func (r *server) GatewaydPublicEdgeMetricsDiscovery(p0 http.ResponseWriter, p1 *http.Request) {
	r.gatewaydPublicEdgeMetricsDiscovery(p0, p1)
}
func (r *server) CloudflaredMetricsDiscovery(p0 http.ResponseWriter, p1 *http.Request) {
	r.cloudflaredMetricsDiscovery(p0, p1)
}
func (r *server) SendTestAlertPresetCore(p0 context.Context, p1 state.Account, p2 string, p3 string) (api.TestAlertPresetResponse, *api.Problem) {
	return r.sendTestAlertPresetCore(p0, p1, p2, p3)
}
func (r *server) ServiceRolloutBindingSweep(p0 context.Context, p1 *int) error {
	return r.serviceRolloutBindingSweep(p0, p1)
}
func (r *server) SetAccountPlan(p0 context.Context, p1 state.Account, p2 api.Plan) error {
	return r.setAccountPlan(p0, p1, p2)
}
func (r *server) SyncInvokeTimeoutProblem(p0 context.Context, p1 state.Invocation, p2 time.Duration) *api.Problem {
	return r.syncInvokeTimeoutProblem(p0, p1, p2)
}
func (r *server) UpdateAccountBillingInfo(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.updateAccountBillingInfo(p0, p1, p2)
}
func (r *server) UpdateApp(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.updateApp(p0, p1, p2)
}
func (r *server) UpdateJob(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.updateJob(p0, p1, p2)
}
func (r *server) UpdateProject(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.updateProject(p0, p1, p2)
}
func (r *server) UpdateProjectEnvironmentPolicies(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.updateProjectEnvironmentPolicies(p0, p1, p2)
}
func (r *server) UpdateProjectEnvironmentRoutes(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.updateProjectEnvironmentRoutes(p0, p1, p2)
}
func (r *server) UpdateQueueBinding(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.updateQueueBinding(p0, p1, p2)
}
func (r *server) UpsertWorkPolicy(p0 http.ResponseWriter, p1 *http.Request, p2 state.Account) {
	r.upsertWorkPolicy(p0, p1, p2)
}
func (r *server) ValidateProjectDeploymentEnvironment(p0 context.Context, p1 state.Account, p2 string, p3 string) *api.Problem {
	return r.validateProjectDeploymentEnvironment(p0, p1, p2, p3)
}
func (r *server) VerifyOAuthAccountEmail(p0 context.Context, p1 state.Account) (state.Account, error) {
	return r.verifyOAuthAccountEmail(p0, p1)
}
func (r *server) VmmdMetricsDiscovery(p0 http.ResponseWriter, p1 *http.Request) {
	r.vmmdMetricsDiscovery(p0, p1)
}

// Members of sourceRefFake
func (r *sourceRefFake) ListRepoCalls() *[]int64    { return &r.listRepoCalls }
func (r *sourceRefFake) StreamBody() *io.ReadCloser { return &r.streamBody }
func (r *sourceRefFake) StreamCalls() *int          { return &r.streamCalls }
func (r *sourceRefFake) StreamInstID() *int64       { return &r.streamInstID }
func (r *sourceRefFake) StreamRef() *string         { return &r.streamRef }
func (r *sourceRefFake) StreamRepo() *string        { return &r.streamRepo }

// Members of sourceRefManifestStaged
func (r *sourceRefManifestStaged) TriggerWorkChanges() *[]sourceRefTriggerWorkBindingChange {
	return &r.triggerWorkChanges
}

// Members of sourceRefTestEnv
func (r *sourceRefTestEnv) AcctID() *string         { return &r.acctID }
func (r *sourceRefTestEnv) AppID() *string          { return &r.appID }
func (r *sourceRefTestEnv) Gh() **sourceRefFake     { return &r.gh }
func (r *sourceRefTestEnv) H() *http.Handler        { return &r.h }
func (r *sourceRefTestEnv) Key() *string            { return &r.key }
func (r *sourceRefTestEnv) Srv() **server           { return &r.srv }
func (r *sourceRefTestEnv) Store() **state.MemStore { return &r.store }
func (r *sourceRefTestEnv) Tarball() *[]byte        { return &r.tarball }
func (r sourceRefTestEnv) Post(p0 *testing.T, p1 string, p2 any) *httptest.ResponseRecorder {
	return r.post(p0, p1, p2)
}
func (r sourceRefTestEnv) PostWithIdempotency(p0 *testing.T, p1 string, p2 any, p3 string) *httptest.ResponseRecorder {
	return r.postWithIdempotency(p0, p1, p2, p3)
}

// Members of spyNotifier
func (r *spyNotifier) Snapshot() []db.Notification { return r.snapshot() }

// Members of statusMetrics
func (r *statusMetrics) MarkRollupSuccess(p0 time.Time) { r.markRollupSuccess(p0) }
func (r *statusMetrics) ObserveEvaluation(p0 string)    { r.observeEvaluation(p0) }
func (r *statusMetrics) ObserveMutation(p0 string, p1 string, p2 string) {
	r.observeMutation(p0, p1, p2)
}

func (r *server) DrainMigrationVerificationsForTest(ctx context.Context) error {
	worker, ok := r.migrationVerificationWorker()
	if !ok {
		return nil
	}
	return worker.Drain(ctx)
}

func (r *server) RestoreMigrationServiceForTest(ctx context.Context, account, app string, run api.MigrationVerification) (string, error) {
	return r.restoreMigrationService(ctx, account, app, run)
}

// Members of testEnv
func (r *testEnv) Acct() *state.Account                           { return &r.acct }
func (r *testEnv) H() *http.Handler                               { return &r.h }
func (r *testEnv) Key() *string                                   { return &r.key }
func (r *testEnv) Ops() **wire.OpsMetrics                         { return &r.ops }
func (r *testEnv) S() **server                                    { return &r.s }
func (r *testEnv) Store() **state.MemStore                        { return &r.store }
func (r testEnv) AddAdminSession(p0 *testing.T, p1 *http.Request) { r.addAdminSession(p0, p1) }
func (r testEnv) Do(p0 *testing.T, p1 string, p2 string, p3 any, p4 map[string]string) *httptest.ResponseRecorder {
	return r.do(p0, p1, p2, p3, p4)
}
func (r testEnv) DoAdmin(p0 *testing.T, p1 string, p2 string, p3 any, p4 map[string]string) *httptest.ResponseRecorder {
	return r.doAdmin(p0, p1, p2, p3, p4)
}
func (r testEnv) DoAs(p0 *testing.T, p1 string, p2 string, p3 any, p4 map[string]string, p5 string) *httptest.ResponseRecorder {
	return r.doAs(p0, p1, p2, p3, p4, p5)
}

// Issues acceptance tests share the original PostgreSQL fixture and evidence setup.
var IssueSeedRequestAttribution = issueSeedRequestAttribution
var PrepareIssueOTLP = prepareIssueOTLP

// Storage integration tests compile in the external unit (ADR-982).
var SetupSecrets = setupSecrets
var SelectedObjectStorageVerification = selectedObjectStorageVerification

func (r *fakeObjectProvider) Created() *[]string { return &r.created }
func (r *fakeObjectProvider) CreateErr() *error  { return &r.createErr }
func (r *fakeObjectProvider) DeleteErr() *error  { return &r.deleteErr }
func (r *server) CleanupDevSessionResources(ctx context.Context, app state.App) error {
	return r.cleanupDevSessionResources(ctx, app)
}

func IssueNativeSetClients(e pgHandlerEnv, gh GithubdClient, provider issuetracker.StatusProvider) {
	e.s.githubd = gh
	e.s.nativeTrackerProvider = provider
}
func IssueNativeStore(e pgHandlerEnv) state.Store     { return e.store }
func IssueNativeAccount(e pgHandlerEnv) state.Account { return e.acct }

func IssueNativeSetTicketProvider(e pgHandlerEnv, p issuetracker.Provider) {
	e.s.nativeTicketProvider = p
}

func IssueNativeRunAutoSyncBatch(e pgHandlerEnv, ctx context.Context) {
	e.s.runIssueTrackerAutoSyncBatch(ctx)
}
func IssueNativeAutoSyncDue(e pgHandlerEnv, ctx context.Context, link string) error {
	_, err := e.pool.Exec(ctx, "UPDATE issue_tracker_auto_sync SET next_check_at=now(),claim_until=now()-interval '1 second' WHERE link_id=$1", link)
	return err
}

func IssueNativeSetLinearVerifier(e pgHandlerEnv, p issuetrackers.LinearConnectionVerifier) {
	e.s.nativeLinearVerifier = p
}

func IssueNativeTicketExpire(e pgHandlerEnv, ctx context.Context, account, request string) error {
	_, err := e.pool.Exec(ctx, "UPDATE native_issue_ticket_receipts SET recover_after=now()-interval '1 second' WHERE account_id=$1 AND request_id=$2", account, request)
	return err
}

func IssueNativeSetLinearOAuth(e pgHandlerEnv, cfg issuetracker.LinearOAuthConfig, provider issuetracker.LinearOAuthProvider) {
	e.s.linearOAuthConfig = cfg
	e.s.linearOAuthProvider = provider
}

func (r *server) IssueSlackResponses() **issueslack.Client { return &r.issueSlackResponses }

func IssueSetSlackOAuth(e pgHandlerEnv, cfg issueslack.OAuthConfig, client *issueslack.Client) {
	e.s.issueSlackOAuthConfig = cfg
	e.s.issueSlackOAuthClient = client
}

func (s *server) ConfigureCheckedReleaseCatalog(t *testing.T, accountID string) {
	configureSourceRefManagedPostgres(t, sourceRefTestEnv{acctID: accountID, srv: s})
}

// AddAuthBindingForTest gives appID an `auth` managed PostgreSQL binding,
// which marks it as an application Auth app (ADR-1190). The test provider has
// no auth credential mode, so the binding is reserved directly.
func AddAuthBindingForTest(t *testing.T, e TestEnv, appID string) {
	t.Helper()
	store, _, databaseID := configureSourceRefManagedPostgres(t, sourceRefTestEnv{srv: e.s, store: e.store, acctID: e.acct.ID, appID: appID})
	now := time.Now().UTC()
	if _, _, err := store.ReserveBinding(context.Background(), managedpostgres.Binding{
		ID: "auth-binding-" + appID, AccountID: e.acct.ID, DatabaseID: databaseID, AppID: appID, Scope: api.DefaultEnvScope,
		EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialAuth, CredentialGeneration: 1,
		State: managedpostgres.BindingStateProvisioning, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("reserve auth binding: %v", err)
	}
}

var ComposeDockerBuildContext = composeDockerBuildContext

func StageApplyTarball(ctx context.Context, scanDir, accountID, projectID string, app state.App, composeContext bool) (string, int64, error) {
	return (&server{}).stageApplyTarball(ctx, scanDir, accountID, projectID, app, composeContext)
}

// WorkloadTrust exposes workload-trust setting precedence (ADR-1141).
func WorkloadTrust(cfg Config, getenv func(string) string, pathEnv, path, issuerEnv, issuer string) (string, string) {
	return workloadTrust(cfg, getenv, pathEnv, path, issuerEnv, issuer)
}

func (r *server) ProductControl() *product.Service { return &r.productControl }

func (r *server) ConfigureProductEmail(getenv func(string) string) error {
	return r.configureProductEmail(getenv)
}

func (s *server) RehearsalNetworkEnabled() *bool { return &s.rehearsalNetworkEnabled }

func (s *server) RehearsalSimulationEnabled() *bool { return &s.rehearsalSimulationEnabled }
func (s *server) RehearsalCommandsEnabled() *bool   { return &s.rehearsalCommandsEnabled }

func (s *server) SetRehearsalPostgresForTest(service *pgruntime.Service, enabled bool) {
	s.rehearsalPostgres, s.rehearsalPostgresEnabled = service, enabled
}

func (s *server) SetRehearsalObjectsForTest(service *objruntime.Service, enabled bool) {
	s.rehearsalObjects, s.rehearsalObjectsEnabled = service, enabled
}

func (s *server) RehearsalWorkersEnabled() *bool        { return &s.rehearsalWorkersEnabled }
func (s *server) RehearsalSuspendResumeEnabled() *bool  { return &s.rehearsalSuspendResumeEnabled }
func (s *server) RehearsalRunningSuspendEnabled() *bool { return &s.rehearsalRunningSuspendEnabled }
func (s *server) RehearsalColdStartEnabled() *bool      { return &s.rehearsalColdStartEnabled }
func (s *server) RehearsalWorkerFaultsEnabled() *bool   { return &s.rehearsalWorkerFaultsEnabled }

func (s *server) StartInternalCIWorker(ctx context.Context, log *slog.Logger) func() {
	return s.startInternalCIWorker(ctx, log)
}
