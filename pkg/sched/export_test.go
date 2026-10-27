// export_test.go exposes unexported identifiers to the external test package
// sched_test (ADR-982). External tests compile as a separate unit from
// the package and its internal tests, so `go test` never holds all of
// sched's tests in one compiler process. Package-level vars are exposed
// as pointers ((*sched.V)) and struct fields as pointer accessors
// ((*x.F())) so tests can read and write them.
//
// When an external test needs another unexported identifier, add it here.

package sched

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/nodereport"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/storagekey"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Package-level types
type ComposedWaveFixture = composedWaveFixture
type DiscardWriter = discardWriter
type DrainSynth = drainSynth
type ExecutionBackendFunc = executionBackendFunc
type FakeDeadLetterStore = fakeDeadLetterStore
type FakeLogStream = fakeLogStream
type FakeNotifier = fakeNotifier
type FakeVMM = fakeVMM
type FakeWakeVMM = fakeWakeVMM
type FrozenFixture = frozenFixture
type HeartbeatFakeDialer = heartbeatFakeDialer
type KafkaConfig = kafkaConfig
type KafkaSASLConfig = kafkaSASLConfig
type KafkaTLSConfig = kafkaTLSConfig
type NodeCapacityTable = nodeCapacityTable
type NotifyEvent = notifyEvent
type QueueDeliveryClaim = queueDeliveryClaim
type QueuePoller = queuePoller
type ReadCountingStore = readCountingStore
type RealtimeTriggerRecord = realtimeTriggerRecord
type RecordingJobVMM = recordingJobVMM
type RecordingSynth = recordingSynth
type RuntimeConfigDrainReason = runtimeConfigDrainReason
type RuntimeSnapshotVerifierFunc = runtimeSnapshotVerifierFunc
type ScheduleStepExecutor = scheduleStepExecutor
type StoreSettle = storeSettle
type TriggerDispatchRequest = triggerDispatchRequest
type TriggerDispatchResponse = triggerDispatchResponse
type TriggerDispatchResult = triggerDispatchResult
type TriggerHeaderCarrier = triggerHeaderCarrier
type TriggerSource = triggerSource
type WarmHintBroadcaster = warmHintBroadcaster
type WorkPool = workPool

// Package-level consts
const ArchAMD64 = archAMD64
const CapacityPayloadDomain = nodereport.CapacityPayloadDomain
const GatewayDispatchResponseMaxBytes = gatewayDispatchResponseMaxBytes
const PressureParkFreshness = pressureParkFreshness
const RuntimeConfigDrainReasonQuietPeriod = runtimeConfigDrainReasonQuietPeriod
const RuntimeConfigDrainReasonRequestsActive = runtimeConfigDrainReasonRequestsActive
const RuntimeConfigDrainReasonTelemetryMissing = runtimeConfigDrainReasonTelemetryMissing
const RuntimeSnapshotStorageNamespace = runtimeSnapshotStorageNamespace
const TriggerReasonBrokerError = triggerReasonBrokerError
const TriggerReasonExclusiveOperationRejected = triggerReasonExclusiveOperationRejected
const TriggerReasonMaxAttempts = triggerReasonMaxAttempts
const TriggerReasonPoisonRecord = triggerReasonPoisonRecord
const WakeTierColdBootFallback = wakeTierColdBootFallback
const WakeTierInit = wakeTierInit
const MaxDuePagesPerTick = maxDuePagesPerTick
const WorkReaper = workReaper

// Package-level vars
var ErrBoom = &errBoom
var ErrNoThawTicket = &errNoThawTicket
var RouterCloseGrace = &routerCloseGrace

// Package-level funcs
var AppTaskFailureStage = appTaskFailureStage
var AppTaskGRPCCode = appTaskGRPCCode
var AppTaskResultFromResponse = appTaskResultFromResponse
var AppendEnvironmentGitOpsJobServiceBindings = appendEnvironmentGitOpsJobServiceBindings
var AssertRecoveryReplicas = assertRecoveryReplicas
var BuildKafkaDialer = buildKafkaDialer
var BuildKafkaTLSConfig = buildKafkaTLSConfig
var ChoosePlacementWithCPU = choosePlacementWithCPU
var ClassifyServiceReplicas = classifyServiceReplicas
var CompleteClaimedInvocation = completeClaimedInvocation
var ConfigureExecutionMode = configureExecutionMode
var CountReads = countReads
var CreateGuardedRun = createGuardedRun
var DecodeKafkaConfig = decodeKafkaConfig
var DecodeNATSConfig = decodeNATSConfig
var DecodeOperationResult = decodeOperationResult
var DecodeRedisConfig = decodeRedisConfig
var DecodeSQSConfig = decodeSQSConfig
var EnableWarmSnapshot = enableWarmSnapshot
var EnqueueScopedDemand = enqueueScopedDemand
var EnvironmentGitOpsJobBindingIntentMatches = environmentGitOpsJobBindingIntentMatches
var EventsForInstance = eventsForInstance
var EventuallyEventsForInstance = eventuallyEventsForInstance
var ExecutionCoordinatorTestConfig = executionCoordinatorTestConfig
var ExecutionModeForApp = executionModeForApp
var QualificationWorkerPushDelivery = qualificationWorkerPushDelivery
var WorkerPushDelivery = (*Engine).workerPushDelivery
var ExerciseApplicationStandardComposedWaves = exerciseApplicationStandardComposedWaves
var ExerciseApplicationStandardOrdinaryWarmMeasuredPromotion = exerciseApplicationStandardOrdinaryWarmMeasuredPromotion
var ExerciseServiceRecoveryRestart = exerciseServiceRecoveryRestart
var ExerciseStandardEnvironmentRefresh = exerciseStandardEnvironmentRefresh
var ExerciseStandardServingRestore = exerciseStandardServingRestore
var ExerciseWorkerRecoveryRestart = exerciseWorkerRecoveryRestart
var ExpectTaskCohort = expectTaskCohort
var GetMetricsBody = getMetricsBody
var GuardOrchestrationStores = guardOrchestrationStores
var HealthcheckGRPCFromDep = healthcheckGRPCFromDep
var HealthcheckPathFromDep = healthcheckPathFromDep
var InstStatsTick = instStatsTick
var InstallPollerFactory = installPollerFactory
var IsFleetCapacityRefusal = isFleetCapacityRefusal
var JsonStdUnmarshal = jsonStdUnmarshal
var KafkaSASLMechanism = kafkaSASLMechanism
var KindsOf = kindsOf
var ListServiceReplicas = listServiceReplicas
var LiveCount = liveCount
var DeliverCronInvocations = deliverCronInvocations
var MakeEngine = makeEngine
var MakeLoopForDLQ = makeLoopForDLQ
var MarshalJSON = marshalJSON
var MaxPlanRAMMB = maxPlanRAMMB
var NewAppAndCron = newAppAndCron
var NewAppTaskCoordinatorFixture = newAppTaskCoordinatorFixture
var NewBrokerEgressObserver = newBrokerEgressObserver
var NewDrainHarness = newDrainHarness
var NewExecutionCoordinatorFixture = newExecutionCoordinatorFixture
var NewFakeNotify = newFakeNotify
var NewFireNowHarness = newFireNowHarness
var NewFrozenFixture = newFrozenFixture
var NewFrozenFixtureInStore = newFrozenFixtureInStore

// SetStore replaces the store the fixture's engine reads through.
func (r *frozenFixture) SetStore(s state.Store) { r.e.store = s }

var NewMockChecker = newMockChecker
var NewNodeCapacityTable = newNodeCapacityTable
var NewNodePresenceTracker = newNodePresenceTracker
var NewQueuePoller = newQueuePoller
var NewRealtimePoller = newRealtimePoller

type KafkaBrokerOp = kafkaBrokerOp

// NewKafkaPollerWithReader is a kafka poller over reader instead of a
// broker connection.
func NewKafkaPollerWithReader(reader KafkaBrokerOp) TriggerSource {
	return &kafkaPoller{reader: reader, batchMax: 100, inFlight: map[string]kafka.Message{},
		open: map[int]map[int64]bool{}, settled: map[int][]kafka.Message{}}
}

type NATSBroker = natsBroker

// NewNATSBrokerPool is a broker pool that dials with dial instead of
// connecting to NATS.
func NewNATSBrokerPool(dial func(rawURL string) (*NATSBroker, error)) *natsBrokerPool {
	return &natsBrokerPool{dial: dial, brokers: map[string]*natsBroker{}}
}

func (p *natsBrokerPool) ForURL(rawURL string) (*NATSBroker, error) { return p.forURL(rawURL) }

// NewRedisStreamPoller wraps an already connected client, as newRedisPoller
// does after its ping and group creation; the egress dialer refuses the
// loopback address a test server listens on.
func NewRedisStreamPoller(client *redis.Client, stream, group, consumer string) TriggerSource {
	return &redisPoller{client: client, stream: stream, group: group, consumer: consumer, addr: "test",
		batchMax: 100, visibility: 30 * time.Second, inFlight: map[string]string{}}
}

var NewStandardCaptureService = newStandardCaptureService
var NewStandardNativeTestVMM = newStandardNativeTestVMM
var NewWakeCoord = newWakeCoord
var NewWarmHintBroadcaster = newWarmHintBroadcaster
var Node = node
var ObservationTask = observationTask
var OOMFailsDeployment = oomFailsDeployment
var OutcomeFromProtocolResult = outcomeFromProtocolResult
var ParentBaseKeyAliases = storagekey.ParentBaseAliases
var ParseJSONHeaders = parseJSONHeaders
var ParseJSONMetadata = parseJSONMetadata
var ParseSnapKey = parseSnapKey
var PrepareStandardAdmittedRuntime = prepareStandardAdmittedRuntime
var PreviousServiceDeployment = previousServiceDeployment
var PrimeObservationCandidate = primeObservationCandidate
var PrimeObservationInstance = primeObservationInstance
var PrimeRunPlusFrameworkReady = primeRunPlusFrameworkReady
var PrimeStartupExtension = primeStartupExtension
var QuietEffective = quietEffective
var QuietLog = quietLog
var QuietPauseAfterMS = quietPauseAfterMS
var ReadCounterValue = readCounterValue
var ReadGatewayDispatchResponse = readGatewayDispatchResponse
var ReaperAbortedCanary = reaperAbortedCanary
var RequireWorkerBlocker = requireWorkerBlocker
var ResolvePriority = resolvePriority
var RowState = rowState
var RunningInstance = runningInstance
var RunningTransitions = runningTransitions
var RuntimeConfigResident = runtimeConfigResident
var RuntimeEnvironmentAdmissionKey = runtimeEnvironmentAdmissionKey
var RuntimeSnapshotVMStateStorageKey = runtimeSnapshotVMStateStorageKey
var ScaleInCooldownAnchor = scaleInCooldownAnchor
var ScopedWorkerCounter = scopedWorkerCounter
var ScrubCIJobEnv = scrubCIJobEnv
var SeedApp = seedApp
var SeedDrainInvocation = seedDrainInvocation
var SeedJobRun = seedJobRun
var SeedScopedWorker = seedScopedWorker
var SeedStageSnapshotPolicy = seedStageSnapshotPolicy
var SeedTwoNodes = seedTwoNodes
var SeedWorkerScopes = seedWorkerScopes
var SilenceLog = silenceLog
var StageReaperPolicyDeployment = stageReaperPolicyDeployment
var StalledSurgeMirror = stalledSurgeMirror
var StandardNativeTestNodeID = standardNativeTestNodeID
var StartupCPUBoostQuota = startupCPUBoostQuota
var StartupDeadlineForApp = startupDeadlineForApp
var TaskObservationStore = taskObservationStore
var TestLog = testLog
var TriggerDispatchParent = triggerDispatchParent
var TruncateCodeError = truncateCodeError

var JobDispatchResult = jobDispatchResult

// Dispatch DB-phase tokens (ADR-20261025232118).
func (o *WorkflowOrchestrator) SetDBPhaseSlotsForTest(slots int) {
	o.dbPhase = newWorkflowDBPhase(slots)
}
func (o *WorkflowOrchestrator) EnterDBPhaseForTest(ctx context.Context) (context.Context, func(), error) {
	return o.enterDBPhase(ctx)
}
func (o *WorkflowOrchestrator) ChildDBPhaseForTest(ctx context.Context) (context.Context, func()) {
	return o.childDBPhase(ctx)
}
func OutsideDBPhaseForTest(ctx context.Context, wait func()) { outsideDBPhase(ctx, wait) }

var UntilNextMinuteTick = untilNextMinuteTick
var ValidEnvironmentGitOpsJobVariables = validEnvironmentGitOpsJobVariables
var ValidRuntimeSnapshot = validRuntimeSnapshot
var ValidRuntimeSnapshotIdentity = validRuntimeSnapshotIdentity
var ValidRuntimeSnapshotRequest = validRuntimeSnapshotRequest
var WaitFor = waitFor
var WakeEngineWithEvents = wakeEngineWithEvents
var WakeN = wakeN
var WireRenderMetrics = wireRenderMetrics
var WithAppTaskStage = withAppTaskStage
var WithRequestedWakeID = withRequestedWakeID
var WithServiceReplicaPlacementSpread = withServiceReplicaPlacementSpread
var WithWakeEnvironment = withWakeEnvironment
var WorkflowFinalOutput = workflowFinalOutput
var WorkflowJSONEqual = workflowJSONEqual
var WorkflowStepIdempotencyKey = workflowStepIdempotencyKey
var XBaseKey = baseKey
var XDeploymentRuntimePort = deploymentRuntimePort
var XLayerKey = layerKey
var XNewEngine = newEngine
var ReadWakeRPC = readWakeRPC

// XWakeLeaderBudget is the deadline a coordinated wake's leader runs under.
func (e *Engine) XWakeLeaderBudget() time.Duration { return e.wakeLeaderBudget() }

// XEventFanoutSafetyTick and XRunEventFanoutSweep let the event substrate
// benchmark schedule fanout exactly as Loop.Run does (spec Appendix D V12).
// adr: 647 — the sweep admits independently routed recipients.
const XEventFanoutSafetyTick = eventFanoutSafetyTick

func (l *Loop) XRunEventFanoutSweep(ctx context.Context) { l.runEventFanoutSweep(ctx) }

// XRunEventFanoutSweepAt runs the sweep with the loop's clock at now.
// adr: 20261027125625 — held recipients route once their wait has passed.
func (l *Loop) XRunEventFanoutSweepAt(ctx context.Context, now time.Time) {
	l.now = func() time.Time { return now }
	l.runEventFanoutSweep(ctx)
}

// XDispatchExplicitWake is Loop.Run's arm for app_wake and
// runtime_config_restart deliveries.
func (l *Loop) XDispatchExplicitWake(ctx context.Context, n db.Notification) {
	l.dispatchExplicitWake(ctx, n)
}

// XRunEventRecipientSweep runs only the sweep's independent recipient stage.
// adr: 614 — recipients are claimed in batches and unrouted claims released.
func (l *Loop) XRunEventRecipientSweep(ctx context.Context) { l.runEventRecipientSweep(ctx) }

var XNewMemCacheOverageChecker = newMemCacheOverageChecker
var XTestWorkerScopedDemandLifecycle = testWorkerScopedDemandLifecycle

// Members of AppSpec
func (r AppSpec) ToProto() *vmmdpb.AppSpec { return r.toProto() }

// Members of DeploymentCounterWatcher
func (r *DeploymentCounterWatcher) LiveCount() *map[string]int      { return &r.liveCount }
func (r *DeploymentCounterWatcher) Mu() *sync.Mutex                 { return &r.mu }
func (r *DeploymentCounterWatcher) ResyncEvery() *time.Duration     { return &r.resyncEvery }
func (r *DeploymentCounterWatcher) Handle(p0 []byte)                { r.handle(p0) }
func (r *DeploymentCounterWatcher) Resync(p0 context.Context) error { return r.resync(p0) }

// Members of DiskDrift
func (r *DiskDrift) Timeout() *time.Duration { return &r.timeout }

// Members of EgressCircuitBreaker
func (r *EgressCircuitBreaker) Resolve() *EgressResolver { return &r.resolve }

// Members of Drain
func (r *Drain) Engine() **Engine       { return &r.engine }
func (r *Drain) Now() *func() time.Time { return &r.now }

// XObserveWith attaches a metrics registry and rebuilds the account cache
// on the drain's current clock, as the delayed-task metric test does.
func (r *Drain) XObserveWith(ops *wire.OpsMetrics) {
	r.ops = ops
	r.accts = newAcctCache(r.now)
}

// Members of EgressCircuitLoop
func (r *EgressCircuitLoop) Seen() *map[string]time.Time { return &r.seen }

// Members of EgressUpstream
func (r EgressUpstream) Key() string { return r.key() }

// Members of Engine
func (r *Engine) BootBudget() *func(state.State) time.Duration { return &r.bootBudget }
func (r *Engine) DeadNodeSettle() *storeSettle                 { return &r.deadNodeSettle }
func (r *Engine) DefaultLocalNodeID() *string                  { return &r.defaultLocalNodeID }
func (r *Engine) FrozenFastPath() *atomic.Bool                 { return &r.frozenFastPath }
func (r *Engine) Log() **slog.Logger                           { return &r.log }
func (r *Engine) NodePresence() **nodePresenceTracker          { return &r.nodePresence }
func (r *Engine) Now() *func() time.Time                       { return &r.now }
func (r *Engine) Ops() **wire.OpsMetrics                       { return &r.ops }
func (r *Engine) Owners() *ownerCache                          { return &r.owners }
func (r *Engine) ServiceReconcileSubmit() *func(context.Context, string) {
	return &r.serviceReconcileSubmit
}
func (r *Engine) ThawTickets() *frozenTickets         { return &r.thawTickets }
func (r *Engine) Vmm() *RoutedVMM                     { return &r.vmm }
func (r *Engine) WarmAffinity() **WarmAffinity        { return &r.warmAffinity }
func (r *Engine) XCapacityTable() **nodeCapacityTable { return &r.capacityTable }
func (r *Engine) XLedger() **NodeLedger               { return &r.ledger }
func (r *Engine) XOwnerNodeID() *string               { return &r.ownerNodeID }
func (r *Engine) XStore() *state.Store                { return &r.store }
func (r *Engine) AdmitAndDispatchWithOptions(p0 context.Context, p1 string, p2 string, p3 string, p4 string, p5 bool, p6 bool) (WakeResult, error) {
	return r.admitAndDispatchWithOptions(p0, p1, p2, p3, p4, p5, p6)
}
func (r *Engine) AppMustPark(p0 context.Context, p1 state.App, p2 state.Account) bool {
	return r.appMustPark(p0, p1, p2)
}
func (r *Engine) AppMutex(p0 string) *sync.Mutex { return r.appMutex(p0) }
func (r *Engine) ApplyLiveCapacityMB(p0 context.Context, p1 string) int64 {
	return r.applyLiveCapacityMB(p0, p1)
}
func (r *Engine) AtMinFloorWithNoSignal(p0 *state.App, p1 int) bool {
	return r.atMinFloorWithNoSignal(p0, p1)
}
func (r *Engine) ChoosePlacementLocked(p0 context.Context, p1 Request) (Placement, error) {
	return r.choosePlacementLocked(p0, p1)
}
func (r *Engine) ChooseWakeSnapshot(p0 context.Context, p1 string, p2 string, p3 int, p4 string) wakeSnapshotChoice {
	return r.chooseWakeSnapshot(p0, p1, p2, p3, p4)
}
func (r *Engine) ClaimPressureParkCandidate(p0 string, p1 int, p2 time.Time) (PressureParkCandidate, bool) {
	return r.claimPressureParkCandidate(p0, p1, p2)
}
func (r *Engine) CleanupJobInstance(p0 context.Context, p1 string, p2 string, p3 string) {
	r.cleanupJobInstance(p0, p1, p2, p3)
}
func (r *Engine) ConvergeServiceReplicas(p0 context.Context, p1 string) {
	r.convergeServiceReplicas(p0, p1)
}
func (r *Engine) ConvergeServiceReplicasToTarget(p0 context.Context, p1 string, p2 int, p3 bool) error {
	return r.convergeServiceReplicasToTarget(p0, p1, p2, p3)
}
func (r *Engine) DrainDeploymentInstances(p0 context.Context, p1 string, p2 bool) {
	r.drainDeploymentInstances(p0, p1, p2)
}
func (r *Engine) FailServiceRolloutHandoff(p0 context.Context, p1 string, p2 string, p3 string, p4 []string) {
	r.failServiceRolloutHandoff(p0, p1, p2, p3, p4)
}
func (r *Engine) IsOnScaleOutCooldown(p0 *state.App, p1 int) bool {
	return r.isOnScaleOutCooldown(p0, p1)
}
func (r *Engine) MarkPrimeFailed(p0 context.Context, p1 string, p2 error) {
	r.markPrimeFailed(p0, p1, p2)
}
func (r *Engine) NodeForRoute(p0 string) string { return r.nodeForRoute(p0) }
func (r *Engine) NoteEgressAbuseRecycle(p0 string, p1 time.Time) bool {
	return r.noteEgressAbuseRecycle(p0, p1)
}
func (r *Engine) ParkForCapacity(p0 context.Context, p1 string, p2 string, p3 error) bool {
	return r.parkForCapacity(p0, p1, p2, p3)
}
func (r *Engine) ParkZeroTrafficSibling(p0 context.Context, p1 string, p2 string) bool {
	return r.parkZeroTrafficSibling(p0, p1, p2)
}
func (r *Engine) PressureSweepCounterValue(p0 string) int    { return r.pressureSweepCounterValue(p0) }
func (r *Engine) PrimeColdBootBudget(p0 int32) time.Duration { return r.primeColdBootBudget(p0) }
func (r *Engine) PublishPressureParkCandidates(p0 []PressureParkCandidate, p1 time.Time) {
	r.publishPressureParkCandidates(p0, p1)
}
func (r *Engine) ResolveWakeEnvironment(p0 context.Context, p1 string, p2 *state.App) (wakeEnvironment, error) {
	return r.resolveWakeEnvironment(p0, p1, p2)
}
func (r *Engine) RuntimeConfigStale(p0 context.Context, p1 state.Instance) bool {
	return r.runtimeConfigStale(p0, p1)
}
func (r *Engine) SnapshotPlacementHints(p0 context.Context, p1 string, p2 string) (string, []string, bool) {
	return r.snapshotPlacementHints(p0, p1, p2)
}
func (r *Engine) ThawFromTicketLocked(p0 context.Context, p1 string, p2 string, p3 string, p4 time.Time) (WakeResult, bool) {
	return r.thawFromTicketLocked(p0, p1, p2, p3, p4)
}
func (r *Engine) ThawTicketFor(p0 context.Context, p1 state.Instance, p2 int) (frozenTicket, error) {
	return r.thawTicketFor(p0, p1, p2)
}
func (r *Engine) Transition(p0 context.Context, p1 string, p2 string, p3 state.State) {
	r.transition(p0, p1, p2, p3)
}
func (r *Engine) UpdateServiceRolloutHandoff(p0 context.Context, p1 string, p2 func(*state.ServiceRolloutHandoff)) {
	r.updateServiceRolloutHandoff(p0, p1, p2)
}
func (r *Engine) UsableSnapshotForWake(p0 context.Context, p1 string, p2 string, p3 int, p4 string) (state.Snapshot, bool, string) {
	return r.usableSnapshotForWake(p0, p1, p2, p3, p4)
}
func (r *Engine) WaitForRuntimeConfigInstanceDrain(p0 context.Context, p1 string, p2 string, p3 time.Time) error {
	return r.waitForRuntimeConfigInstanceDrain(p0, p1, p2, p3)
}
func (r *Engine) WakeFanoutFor(p0 context.Context, p1 string) WakeFanout {
	return r.wakeFanoutFor(p0, p1)
}
func (r *Engine) WakeFanoutForApp(p0 context.Context, p1 string, p2 *state.App) WakeFanout {
	return r.wakeFanoutForApp(p0, p1, p2)
}
func (r *Engine) WakeInstanceModeMatchesApp(p0 context.Context, p1 string, p2 state.Instance) bool {
	return r.wakeInstanceModeMatchesApp(p0, p1, p2)
}
func (r *Engine) XOwnsApp(p0 state.App) bool { return r.ownsApp(p0) }

// Members of ForkCoordinator
func (r *ForkCoordinator) Now() *func() time.Time { return &r.now }

// Members of Heartbeat
func (r *Heartbeat) Now() *func() time.Time { return &r.now }

// Members of InstanceDivergenceReconciler
func (r *InstanceDivergenceReconciler) CandidateCount() int { return r.candidateCount() }

// Members of LivenessWindow
func (r *LivenessWindow) Recent(p0 string, p1 time.Time) int { return r.recent(p0, p1) }
func (r *LivenessWindow) RecentOnNode(p0 string, p1 string, p2 time.Time) int {
	return r.recentOnNode(p0, p1, p2)
}

// Members of Loop
func (r *Loop) Engine() **Engine                          { return &r.engine }
func (r *Loop) GatewayBaseURL() *string                   { return &r.gatewayBaseURL }
func (r *Loop) GatewayHTTPClient() **http.Client          { return &r.gatewayHTTPClient }
func (r *Loop) Pool() **pgxpool.Pool                      { return &r.pool }
func (r *Loop) TriggerPollers() *map[string]triggerSource { return &r.triggerPollers }
func (r *Loop) PollerForTrigger(t sqlc.Trigger) (triggerSource, bool, error) {
	return r.pollerForTrigger(t)
}
func (r *Loop) EvictTriggerPollers(enabled []sqlc.Trigger) { r.evictTriggerPollers(enabled) }
func (r *Loop) BeatMain()                                  { r.beatMain() }
func (r *Loop) DispatchOneCron(p0 context.Context, p1 state.Cron, p2 time.Time) {
	r.dispatchOneCron(p0, p1, p2)
}
func (r *Loop) DispatchOneTrigger(p0 context.Context, p1 sqlc.Trigger, p2 storeLike, p3 func(string) api.Plan) error {
	return r.dispatchOneTrigger(p0, p1, p2, p3)
}
func (r *Loop) DispatchReaper(p0 context.Context)              { r.dispatchReaper(p0) }
func (r *Loop) DispatchWorkerRecovery(p0 context.Context)      { r.dispatchWorkerRecovery(p0) }
func (r *Loop) DrainPendingFireNowRequests(p0 context.Context) { r.drainPendingFireNowRequests(p0) }
func (r *Loop) EnrichReaperEnvironmentPolicies(p0 context.Context, p1 []state.App, p2 []InstanceInfo, p3 map[string]int) map[string]int {
	return r.enrichReaperEnvironmentPolicies(p0, p1, p2, p3)
}
func (r *Loop) HandleRuntimeConfigRestart(p0 context.Context, p1 db.Notification) error {
	return r.handleRuntimeConfigRestart(p0, p1)
}
func (r *Loop) ProcessFireNowRequest(p0 context.Context, p1 state.FireNowRequest) {
	r.processFireNowRequest(p0, p1)
}
func (r *Loop) RecoverPrimeCandidate(p0 context.Context, p1 string, p2 time.Time) error {
	return r.recoverPrimeCandidate(p0, p1, p2)
}
func (r *Loop) RunInstanceStats(p0 context.Context) { r.runInstanceStats(p0) }
func (r *Loop) RunPrimeRecovery(p0 context.Context) { r.runPrimeRecovery(p0) }
func (r *Loop) RunReaper(p0 context.Context)        { r.runReaper(p0) }
func (r *Loop) RunCronTick(p0 context.Context)      { r.runCronTick(p0) }
func (r *Loop) RecordTriggerConsumerHealth(p0 context.Context, p1 storeLike, p2 sqlc.Trigger, p3 PollResult) {
	r.recordTriggerConsumerHealth(p0, p1, p2, p3)
}

// Health returns the consumer health observations the fake recorded.
func (r *fakeDeadLetterStore) Health() []state.TriggerConsumerHealthObservation { return r.health }
func (r *Loop) RunRetention(p0 context.Context)                                 { r.runRetention(p0) }
func (r *Loop) RunScaleUp(p0 context.Context)                                   { r.runScaleUp(p0) }
func (r *Loop) RunServiceRecovery(p0 context.Context)                           { r.runServiceRecovery(p0) }
func (r *Loop) RunWorkflowRetention(p0 context.Context)                         { r.runWorkflowRetention(p0) }
func (r *Loop) RunWorkflowSchedulesTick(p0 context.Context) error {
	return r.runWorkflowSchedulesTick(p0)
}
func (r *Loop) WaitPrimes()         { r.waitPrimes() }
func (r *Loop) WorkPool() *workPool { return r.workPool() }
func (r *Loop) XHandleNotification(p0 context.Context, p1 db.Notification) {
	r.handleNotification(p0, p1)
}

// Members of MemLeaser
func (r *MemLeaser) Now() *func() time.Time { return &r.now }

// Members of NodeAwareFlowCounter
func (r *NodeAwareFlowCounter) Now() *func() time.Time { return &r.now }

// Members of NodeLedger
func (r *NodeLedger) Entries() *map[string]*reservation      { return &r.entries }
func (r *NodeLedger) Now() *func() time.Time                 { return &r.now }
func (r *NodeLedger) PerApp() *map[string]int                { return &r.perApp }
func (r *NodeLedger) PerAppEnvironment() *map[string]int     { return &r.perAppEnvironment }
func (r *NodeLedger) Resident() *map[string]*nodeReservation { return &r.resident }
func (r *NodeLedger) CeilingForNode_locked(p0 string, p1 api.Limits) int {
	return r.ceilingForNode_locked(p0, p1)
}

// Members of PressureRebalancer
func (r *PressureRebalancer) Tick(p0 context.Context) { r.tick(p0) }

// Members of RecoveryRunner
func (r *RecoveryRunner) Store() *state.Store { return &r.store }

// Members of RuntimeSnapshotIdentity
func (r RuntimeSnapshotIdentity) Equal(p0 RuntimeSnapshotIdentity) bool { return r.equal(p0) }

// Members of WakeFanout
func (r WakeFanout) Wants(p0 int, p1 int) bool { return r.wants(p0, p1) }

// Members of WarmAffinity
func (r *WarmAffinity) Ttl() *time.Duration          { return &r.ttl }
func (r *WarmAffinity) SetClock(p0 func() time.Time) { r.setClock(p0) }

// Members of Watchdog
func (r *Watchdog) SweepRuns(p0 context.Context) { r.sweepRuns(p0) }

// Members of WorkflowOrchestrator
func (r *WorkflowOrchestrator) ExecuteWorkflowHandler(p0 context.Context, p1 *state.WorkflowRun, p2 string, p3 string, p4 map[string]string, p5 []byte, p6 time.Duration, p7 string, p8 int64) (int, []byte, error) {
	return r.executeWorkflowHandler(p0, p1, p2, p3, p4, p5, p6, p7, p8)
}

// Members of composedWaveFixture
func (r *composedWaveFixture) Engine() **Engine { return &r.engine }
func (r *composedWaveFixture) Refresh() *func(*testing.T, state.ApplicationStandardRuntimeRefreshRequest) {
	return &r.refresh
}
func (r *composedWaveFixture) Vmm() **composedWaveNativeVMM { return &r.vmm }

// Members of composedWaveNativeVMM
func (r *composedWaveNativeVMM) Identity() *runtimeadmission.Identity { return &r.identity }

// Members of drainSynth
func (r *drainSynth) Calls() *atomic.Int64    { return &r.calls }
func (r *drainSynth) Transient() *atomic.Bool { return &r.transient }

// Members of fakeNotifier
func (r *fakeNotifier) Events() *[]notifyEvent { return &r.events }
func (r *fakeNotifier) Mu() *sync.Mutex        { return &r.mu }
func (r *fakeNotifier) Count(p0 string) int    { return r.count(p0) }

// Members of fakeVMM
// SetPingResult sets what Ping returns for target: err when non-nil, else
// a success carrying bases (ADR-20261022184531).
func (h *heartbeatFakeDialer) SetPingResult(target string, bases []BaseGenerationReadiness, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pingErr == nil {
		h.pingErr = map[string]error{}
	}
	if h.baseGenerations == nil {
		h.baseGenerations = map[string][]BaseGenerationReadiness{}
	}
	h.pingErr[target], h.baseGenerations[target] = err, bases
	if err == nil {
		delete(h.pingErr, target)
	}
}

var EvaluateNodeReadiness = evaluateNodeReadiness

func (r *fakeVMM) ColdBootHook() *func()            { return &r.coldBootHook }
func (r *fakeVMM) ColdBoots() *int                  { return &r.coldBoots }
func (r *fakeVMM) DestroyErr() *error               { return &r.destroyErr }
func (r *fakeVMM) Destroys() *int                   { return &r.destroys }
func (r *fakeVMM) LastColdBootSpec() *AppSpec       { return &r.lastColdBootSpec }
func (r *fakeVMM) LastColdBootCtx() context.Context { return r.lastColdBootCtx }
func (r *fakeVMM) LastSnapRef() *SnapshotRef        { return &r.lastSnapRef }
func (r *fakeVMM) Mu() *sync.Mutex                  { return &r.mu }
func (r *fakeVMM) Restores() *int                   { return &r.restores }
func (r *fakeVMM) SleepFor() *time.Duration         { return &r.sleepFor }
func (r *fakeVMM) SnapErr() *error                  { return &r.snapErr }
func (r *fakeVMM) Snapshots() *int                  { return &r.snapshots }
func (r *fakeVMM) StatsHook() *func(context.Context, string) (*StatsSnapshot, error) {
	return &r.statsHook
}
func (r *fakeVMM) WakeErr() *error           { return &r.wakeErr }
func (r *fakeVMM) WarmSnapshotHook() *func() { return &r.warmSnapshotHook }
func (r *fakeVMM) WarmSnapshots() *int       { return &r.warmSnapshots }

// Members of fireNowHarness
func (r *fireNowHarness) DeliverInvocations(p0 context.Context) { r.deliverInvocations(p0) }
func (r *fireNowHarness) Loop() **Loop                          { return &r.loop }
func (r *fireNowHarness) Store() *state.Store                   { return &r.store }
func (r *fireNowHarness) Synth() **recordingSynth               { return &r.synth }

// Members of frozenFakeVMM
func (r *frozenFakeVMM) Thaws() *int { return &r.thaws }

// Members of frozenFixture
func (r *frozenFixture) App() *state.App         { return &r.app }
func (r *frozenFixture) Dep() *state.Deployment  { return &r.dep }
func (r *frozenFixture) E() **Engine             { return &r.e }
func (r *frozenFixture) Ins() *state.Instance    { return &r.ins }
func (r *frozenFixture) Store() **state.MemStore { return &r.store }
func (r *frozenFixture) Vmm() **frozenFakeVMM    { return &r.vmm }

// Members of frozenTickets
func (r *frozenTickets) ByApp() *map[string]map[string]frozenTicket { return &r.byApp }

// Members of memCacheOverageChecker
func (r *memCacheOverageChecker) Now() *func() time.Time { return &r.now }

// Members of memLeaseRecord
func (r *memLeaseRecord) Key() *string     { return &r.key }
func (r *memLeaseRecord) OwnerID() *string { return &r.ownerID }

// Members of nodePresenceObservation
func (r *nodePresenceObservation) Fingerprint() *string { return &r.fingerprint }

// Members of nodePresenceTracker
func (r *nodePresenceTracker) Current(p0 string, p1 string, p2 time.Time) bool {
	return r.current(p0, p1, p2)
}
func (r *nodePresenceTracker) MarkReconciled(p0 string, p1 string) { r.markReconciled(p0, p1) }

// Members of nodeReservation
func (r *nodeReservation) ResidentRAM() *int { return &r.residentRAM }
func (r *nodeReservation) UsedVCPU() *int    { return &r.usedVCPU }

// Members of notifyEvent
func (r *notifyEvent) Channel() *string { return &r.channel }
func (r *notifyEvent) Payload() *string { return &r.payload }

// Members of ownerCache
func (r *ownerCache) Lookup(p0 string) (ownerEntry, bool) { return r.lookup(p0) }

// Members of ownerEntry
func (r *ownerEntry) Generation() *int64 { return &r.generation }

// Members of queuePoller
func (r *queuePoller) ItemsInFlight() *map[string]queueDeliveryClaim { return &r.itemsInFlight }
func (r *queuePoller) DeliveryPoller(p0 []SourceRecord) *queuePoller { return r.deliveryPoller(p0) }
func (r *queuePoller) ReleaseNamedClaims(p0 context.Context, p1 map[string]queueDeliveryClaim, p2 sqlc.Trigger) error {
	return r.releaseNamedClaims(p0, p1, p2)
}

// Members of readCountingStore
func (r *readCountingStore) Reads() map[string]int { return r.reads() }

// Members of recordingJobVMM
func (r *recordingJobVMM) Calls() *int       { return &r.calls }
func (r *recordingJobVMM) Spec() *JobVmmSpec { return &r.spec }

// Members of recordingSynth
func (r *recordingSynth) Calls() *atomic.Int64 { return &r.calls }

// Members of reservation
func (r *reservation) AdmissionMB() *int { return &r.admissionMB }
func (r *reservation) AppID() *string    { return &r.appID }
func (r *reservation) CountsConc() *bool { return &r.countsConc }
func (r *reservation) NodeID() *string   { return &r.nodeID }
func (r *reservation) Vcpu() *int        { return &r.vcpu }

// Members of scheduleStepExecutor
func (r *scheduleStepExecutor) Calls() *int    { return &r.calls }
func (r *scheduleStepExecutor) Input() *[]byte { return &r.input }

// Members of serviceReplicaStatus
func (r *serviceReplicaStatus) Ready() *int { return &r.ready }

// ReadyCount reads ready from a non-addressable value, such as a call result.
func (r serviceReplicaStatus) ReadyCount() int { return r.ready }
func (r serviceReplicaStatus) Managed() int    { return r.managed() }

// Members of stageSnapshotFixture
func (r *stageSnapshotFixture) Account() *state.Account  { return &r.account }
func (r *stageSnapshotFixture) App() *state.App          { return &r.app }
func (r *stageSnapshotFixture) Project() *state.Project  { return &r.project }
func (r *stageSnapshotFixture) Stage() *state.Deployment { return &r.stage }
func (r *stageSnapshotFixture) Store() **state.MemStore  { return &r.store }
func (r stageSnapshotFixture) RecreateStage(p0 context.Context, p1 *testing.T) {
	r.recreateStage(p0, p1)
}

// Members of standardNativeTestVMM
func (r *standardNativeTestVMM) BeforeNative() *func() { return &r.beforeNative }
func (r *standardNativeTestVMM) ColdBoots() *int       { return &r.coldBoots }
func (r *standardNativeTestVMM) Destroys() *int        { return &r.destroys }
func (r *standardNativeTestVMM) Snapshots() *int       { return &r.snapshots }

// Members of storeSettle
func (r *storeSettle) Mu() *sync.Mutex                                     { return &r.mu }
func (r *storeSettle) Since() *time.Time                                   { return &r.since }
func (r *storeSettle) Hold()                                               { r.hold() }
func (r *storeSettle) Observe(p0 error, p1 time.Time)                      { r.observe(p0, p1) }
func (r *storeSettle) Settled(p0 time.Time, p1 time.Duration) (bool, bool) { return r.settled(p0, p1) }

// Members of wakeCoord
func (r *wakeCoord) Cap() *int                              { return &r.cap }
func (r *wakeCoord) Inflight() *map[string][]*wakeCoordCall { return &r.inflight }

// Members of wakeCoordCall
func (r *wakeCoordCall) Waiters() *int { return &r.waiters }

// Members of wakeEnvironment
func (r *wakeEnvironment) Deployment() *state.Deployment { return &r.deployment }
func (r wakeEnvironment) ReusableFor(p0 string) bool     { return r.reusableFor(p0) }

// Members of wakeSnapshotChoice
func (r *wakeSnapshotChoice) ColdReason() *string       { return &r.coldReason }
func (r *wakeSnapshotChoice) Ok() *bool                 { return &r.ok }
func (r *wakeSnapshotChoice) Rejected() *state.Snapshot { return &r.rejected }
func (r *wakeSnapshotChoice) Snap() *state.Snapshot     { return &r.snap }
func (r *wakeSnapshotChoice) Tier() *string             { return &r.tier }

// Members of warmHintBroadcaster
func (r *warmHintBroadcaster) DroppedCount() uint64  { return r.droppedCount() }
func (r *warmHintBroadcaster) Emit(p0 WarmHintEvent) { r.emit(p0) }
func (r *warmHintBroadcaster) Subscribe(p0 int) (<-chan WarmHintEvent, func()) {
	return r.subscribe(p0)
}
func (r *warmHintBroadcaster) SubscriberCount() int { return r.subscriberCount() }

// CreateRuntimeWithRehearsalRun boots spec through createRuntimeWithStandards
// with the app's rehearsal run already read, as a wake hands it over.
// adr: 20261021090000
func CreateRuntimeWithRehearsalRun(ctx context.Context, vmm RoutedVMM, spec AppSpec, runID string) error {
	e := &Engine{vmm: vmm}
	// A wake's correlation envelope names the instance, so the boot needs no
	// instance lookup.
	ctx = wire.WithContext(ctx, wire.CorrelationFields{WakeID: "8f0e2b4c-2f3a-4c55-9d7e-0c1f6a2b3c4d", InstanceID: "instance", NodeID: "node", AppID: spec.AppID})
	ctx = withBootPrepared(ctx, bootPrepared{instanceID: "instance", rehearsalRunID: runID, haveRehearsal: true, haveCapture: true})
	_, err := e.createRuntimeWithStandards(ctx, "node", "instance", string(state.StateColdBooting), spec, nil, false)
	return err
}

// Members of workPool
func (r *workPool) Drain()                                          { r.drain() }
func (r *workPool) Submit(p0 workKind, p1 string, p2 func()) string { return r.submit(p0, p1, p2) }

// QueueRetryGroups exposes the queue retry last_error grouping (Hunt 12
// H12-34).
func QueueRetryGroups(items []string, statusByID map[string]TriggerDispatchResult) map[string][]string {
	out := map[string][]string{}
	for _, group := range queueRetryGroups(items, statusByID) {
		out[group.reason] = group.items
	}
	return out
}

// WarmCaptureInstanceGatesOpen exposes the warm-capture gates shared by the
// capture and the freeze decision (Hunt 12 H12-52).
var WarmCaptureInstanceGatesOpen = warmCaptureInstanceGatesOpen

// SettleTriggerDeadLetters runs the per-record DLQ settlement; queue selects
// the queue poller's Nack-first order (ADR-20261022203653).
func (r *Loop) SettleTriggerDeadLetters(ctx context.Context, t sqlc.Trigger, poller triggerSource, store storeLike, queue bool,
	recordIDs, items, reasons, lastErrors []string, attempts []int32) {
	claimed := make(map[string]sqlc.TriggerRecord, len(recordIDs))
	for _, id := range recordIDs {
		var record sqlc.TriggerRecord
		_ = record.ID.Scan(id)
		claimed[id] = record
	}
	r.settleTriggerDeadLetters(ctx, t, poller, store, queue, claimed, terminalTriggerResults{
		recordIDs: recordIDs, items: items, reasons: reasons, errors: lastErrors, attempts: attempts})
}

// ADR-20261024085817: a draining node keeps no resident work.
var PublishWarmFixture = publishWarmFixture

type WarmResumeFakeVMM = warmResumeFakeVMM

func NewWarmResumeFakeVMM() *warmResumeFakeVMM { return &warmResumeFakeVMM{fakeVMM: &fakeVMM{}} }

func (r *Engine) DrainRequestBound() *time.Duration { return &r.drainRequestBound }

func (r *warmResumeFakeVMM) ResumeCalls() *int { return &r.resumeCalls }

// ADR-20261024140000: build secrets resolve with state.ResolveRuntimeSecretSources;
// RuntimeMainSecretEntries is the wake's own serving delivery to compare it with.
func RuntimeMainSecretEntries(snapshot state.RuntimeAppValuesSnapshot, intent state.AppEnvironmentSecretIntent) ([]fcvm.SealedEnvEntry, error) {
	refs, err := state.DeploymentSecretReferences(snapshot.SecretGrants.OverrideEnvSecrets)
	if err != nil {
		return nil, err
	}
	delivery, err := sealedEnvDeliveryFromRowsWithIntent(snapshot.Secrets, snapshot.AccountID, snapshot.AppID, snapshot.Scope, refs, false, intent, true)
	if err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		return withInheritedProjectSecrets(snapshot.InheritedSecrets, delivery.Entries), nil
	}
	return delivery.Entries, nil
}

var QueueDeadLetterError = queueDeadLetterError
var PrimeRuntimeBusyBackoff = &primeRuntimeBusyBackoff
var ApplicationStandardRuntimeProblem = applicationStandardRuntimeProblem
var HealthcheckGRPCForApp = healthcheckGRPCForApp
var StartupProbeForApp = startupProbeForApp

// ADR-20261024225612 (CPU-compatible restore, per-platform snapshot slots).
var (
	SnapshotRestorableOn      = snapshotRestorableOn
	LegacySnapshotsRestorable = legacySnapshotsRestorable
)

func (r *Engine) XReusableSnapshotOn(ctx context.Context, depID, tier, nodeID string) (state.Snapshot, bool) {
	return r.reusableSnapshotOn(ctx, depID, tier, nodeID)
}

func (r *Engine) XEmitSnapshotWritten(ctx context.Context, nodeID string) {
	r.emitSnapshotWritten(ctx, "ins", time.Time{}, "dep", nodeID, "", "key", SnapshotBytes{}, state.SnapshotTierInit, nil)
}

// LastPayload is the newest notification payload on channel.
func (r *fakeNotifier) LastPayload(channel string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.events) - 1; i >= 0; i-- {
		if r.events[i].channel == channel {
			return r.events[i].payload, true
		}
	}
	return "", false
}

// WithPreWakeClockForTest starts a pre-wake clock with every stamp given.
func WithPreWakeClockForTest(ctx context.Context, rpc, ensure, env, leader time.Time) context.Context {
	return context.WithValue(ctx, preWakeClockKey{}, &preWakeClock{rpcStarted: rpc, ensureStarted: ensure, envReady: env, leader: leader})
}

// WithWakeStartedForTest is withWakeStarted.
func WithWakeStartedForTest(ctx context.Context, at time.Time) context.Context {
	return withWakeStarted(ctx, at)
}

// PreRPCWithPreWakeForTest is preRPCWithPreWake.
func PreRPCWithPreWakeForTest(ctx context.Context, t events.PreRPCTimings, attempt time.Time) events.PreRPCTimings {
	return preRPCWithPreWake(ctx, t, attempt)
}

// MarkPreWakeForTest stamps the ensure-started, environment-ready and leader
// marks in order.
func MarkPreWakeForTest(ctx context.Context) {
	markPreWake(ctx, preWakeEnsureStarted)
	markPreWake(ctx, preWakeEnvironmentReady)
	markPreWake(ctx, preWakeLeader)
}

var LogVMMBootFailure = logVMMBootFailure

// XRunCronHistoryRetention runs the hourly cron history sweep once.
func (l *Loop) XRunCronHistoryRetention(ctx context.Context) { l.runCronHistoryRetention(ctx) }
