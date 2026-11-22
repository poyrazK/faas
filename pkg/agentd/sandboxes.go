package agentd

// Sandbox sessions (ADR-20261027090013 §1, §5). A replica claims a queued
// session under a lease and holds one schedd RunSandbox stream for the
// session's life: schedd keeps the reservation and vmmd the VM as long as
// the stream lasts. The replica owns the session's state and its timers:
// it freezes the VM after SandboxIdleFreezeSec without calls, and ends the
// session after its idle timeout, at its end, or on a close request. vmmd
// reports calls on the stream; their time reaches the database only with
// each lease renewal.
//
// A persistent session (ADR-20261122132055) pauses instead of ending at its
// idle and run limits, or when apid asks: the replica sends pause, vmmd
// captures the VM and ends it, and the session is paused with no lease. A
// resume request makes it claimable again; the claim restores its capture,
// preferring the node that holds it.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/storagekey"
	"github.com/onebox-faas/faas/pkg/volbackup"
)

// SandboxStore is the sandbox_sessions surface agentd writes;
// *agentstore.Store implements it.
type SandboxStore interface {
	ClaimSandbox(ctx context.Context, owner, token string, ttl time.Duration, maxLive func(plan string) int) (agentstore.SandboxClaim, bool, error)
	MarkSandboxPlaced(ctx context.Context, id, token, nodeID string) error
	MarkSandboxReady(ctx context.Context, id, token, nodeID, instanceID string) error
	RenewSandbox(ctx context.Context, id, token string, ttl time.Duration, lastActivity time.Time) (agentstore.SandboxLease, error)
	SetSandboxFrozen(ctx context.Context, id, token string, frozen bool) error
	RequeueSandbox(ctx context.Context, id, token string) error
	FinishSandbox(ctx context.Context, id, token, state, code, message string) error
	ExpireSandboxLeases(ctx context.Context) (requeued, failed, expired int64, err error)
	ReserveSandboxSnapshot(ctx context.Context, r agentstore.SnapshotReservation) (agentstore.SandboxSnapshot, error)
	CompleteSandboxSnapshot(ctx context.Context, id string, bytes int64, sha256 string) error
	DeleteSandboxSnapshot(ctx context.Context, id string) error
	SweepableSandboxSnapshots(ctx context.Context, pendingTTL time.Duration, limit int) ([]agentstore.SweepableSandboxSnapshot, error)
	RecordSandboxUsage(ctx context.Context, id, token string, u agentstore.SandboxUsage) (bool, error)
	RecordSandboxTaskResult(ctx context.Context, id, token string, exitCode int, output []byte) error
	RecordSandboxDomains(ctx context.Context, id, token string, plan api.Plan, entries []agentstore.SandboxDomain) (int, error)
	// Persistent sessions (ADR-20261122132055).
	BeginSandboxPause(ctx context.Context, id, token string) error
	CancelSandboxPause(ctx context.Context, id, token string, frozen bool) error
	ReturnSandboxResume(ctx context.Context, id, token string, retry bool, after time.Duration) error
	TouchSandboxCaptures(ctx context.Context, ids []string) error
	RecordAbandonedSandboxCapture(ctx context.Context, sessionID string, c agentstore.SandboxCapture) error
	MarkSandboxPaused(ctx context.Context, id, token string, c agentstore.SandboxCapture, retention time.Duration) error
	MarkSandboxResumed(ctx context.Context, id, token, nodeID, instanceID string, cold bool, run time.Duration) error
	ConsumedSandboxCaptures(ctx context.Context, limit int) ([]agentstore.SandboxCapture, error)
	NodeOnlySandboxCaptures(ctx context.Context, limit int) ([]agentstore.SandboxCapture, error)
	MarkSandboxCaptureRemote(ctx context.Context, sessionID, captureID string) error
	DeleteSandboxCapture(ctx context.Context, id string) error
	// Workspace volumes (ADR-20261122132055 §4).
	MarkSandboxVolumeNode(ctx context.Context, id, token, nodeID string, sizeGB int) (string, error)
	RecordSandboxVolumeBackup(ctx context.Context, id, token string, b agentstore.SandboxVolumeBackup) (bool, error)
	MarkSandboxVolumeBackupStored(ctx context.Context, backupID string) error
	SupersededSandboxVolumeBackups(ctx context.Context, limit int) ([]agentstore.SandboxVolumeRef, error)
	EndedSandboxVolumes(ctx context.Context, limit int) ([]agentstore.SandboxVolumeRef, error)
	SandboxVolumeBackupIDs(ctx context.Context, sessionID string) ([]string, error)
	MarkSandboxVolumeBackupDeleted(ctx context.Context, backupID string) error
	DeleteSandboxVolume(ctx context.Context, sessionID string) error
}

// SandboxSchedd opens RunSandbox streams; scheddpb.ScheddClient implements
// it.
type SandboxSchedd interface {
	RunSandbox(ctx context.Context, opts ...grpc.CallOption) (grpc.BidiStreamingClient[scheddpb.SandboxRunRequest, scheddpb.SandboxRunEvent], error)
	// DeleteSandboxVolume removes a session's volume from one node
	// (ADR-20261122132055 §4).
	DeleteSandboxVolume(ctx context.Context, in *scheddpb.DeleteSandboxVolumeRequest, opts ...grpc.CallOption) (*scheddpb.DeleteSandboxVolumeResponse, error)
}

// SandboxConfig tunes a replica's sessions.
type SandboxConfig struct {
	// Concurrency bounds the sessions this replica holds at once; sessions
	// are long-lived, so they have slots of their own.
	Concurrency int
	// Tick is how often a session's timers are checked.
	Tick time.Duration
	// IdleFreeze is how long a session goes without calls before its VM is
	// frozen; SandboxIdleFreezeSec unless set.
	IdleFreeze time.Duration
	// PauseTimeout bounds a pause vmmd never answers (a capture of the
	// largest shape takes minutes, not this long); SandboxPauseTimeoutSec
	// unless set.
	// A capture that lands later is still taken for 3× as long: vmmd
	// answers a pause before it reads the next frame, so no other pause is
	// asked meanwhile.
	PauseTimeout time.Duration
	// PersistentOverrun is how long a persistent session keeps running past
	// its run limit while its pauses are refused before it ends like an
	// ephemeral one at its lifetime; SandboxPersistentOverrunSec unless set.
	PersistentOverrun time.Duration
	// Workspaces is the artifact store persisted workspaces are kept in
	// (ADR-20261027090013 step 7); the sweep deletes their archives from
	// it. Without it sessions persist nothing.
	Workspaces storage.StorageBackend
}

func (c SandboxConfig) withDefaults() SandboxConfig {
	if c.Concurrency <= 0 {
		c.Concurrency = 64
	}
	if c.Tick <= 0 {
		c.Tick = time.Second
	}
	if c.IdleFreeze <= 0 {
		c.IdleFreeze = api.SandboxIdleFreezeSec * time.Second
	}
	if c.PauseTimeout <= 0 {
		c.PauseTimeout = time.Duration(api.SandboxPauseTimeoutSec) * time.Second
	}
	if c.PersistentOverrun <= 0 {
		c.PersistentOverrun = time.Duration(api.SandboxPersistentOverrunSec) * time.Second
	}
	return c
}

// Session outcomes.
const (
	sandboxOutcomeClosed    = "closed"
	sandboxOutcomeIdle      = "idle"
	sandboxOutcomeLifetime  = "lifetime"
	sandboxOutcomeRejected  = "rejected"
	sandboxOutcomeVMLost    = "vm_lost"
	sandboxOutcomeUnplanned = "plan_has_no_sessions"
	// sandboxOutcomeWorkspace: the persisted workspace the session starts
	// from is gone or could not be unpacked.
	sandboxOutcomeWorkspace = "workspace_unavailable"
	// sandboxOutcomeImage: the seller image the session boots was deleted
	// or evicted after the claim.
	sandboxOutcomeImage = "image_unavailable"
	// sandboxOutcomeTaskDone and sandboxOutcomeTaskFailed end a sandbox
	// task (ADR-20261116182615 §3): its command ran, or could not, or never
	// reported within its timeout.
	sandboxOutcomeTaskDone   = "task_done"
	sandboxOutcomeTaskFailed = "task_failed"
	// sandboxTaskBootSlack is the time a task may take to boot and start
	// its browser beyond the task's own timeout.
	sandboxTaskBootSlack = 90 * time.Second
	// sandboxOutcomeEgressPolicy: the session has a domain policy and the
	// node's vmmd cannot enforce one (ADR-20261117091026 §3).
	sandboxOutcomeEgressPolicy = "egress_policy_unsupported"
	// sandboxPauseRetry is how long a refused pause waits before the next
	// try (ADR-20261122132055 §1).
	sandboxPauseRetry = 5 * time.Second
	// sandboxCaptureSweepBatch bounds the consumed captures one sweep
	// deletes.
	sandboxCaptureSweepBatch = 100
	// sandboxCaptureUploadWindow is how long a capture's node may still be
	// uploading it: vmmd gives up after SandboxCaptureUploadSec and then
	// keeps its copy as an ordinary, evictable cache entry. A consumed
	// capture's objects are deleted only once the shared store holds all of
	// them or this has passed, so no late upload outlives its row.
	sandboxCaptureUploadWindow = time.Duration(api.SandboxCaptureUploadSec)*time.Second + time.Hour
)

// Sandboxes drives one replica's sandbox sessions.
type Sandboxes struct {
	store   SandboxStore
	schedd  SandboxSchedd
	cfg     Config
	scfg    SandboxConfig
	log     *slog.Logger
	metrics *Metrics
	now     func() time.Time
	// actions are the held sessions that take browser actions
	// (ADR-20261117065818 §2).
	actions sandboxActions
}

// NewSandboxes returns a replica's session driver; cfg is the replica's
// lease and poll configuration.
func (a *Agentd) NewSandboxes(store SandboxStore, schedd SandboxSchedd, scfg SandboxConfig) *Sandboxes {
	return &Sandboxes{store: store, schedd: schedd, cfg: a.cfg, scfg: scfg.withDefaults(), log: a.log, metrics: a.metrics, now: time.Now}
}

// Run claims and holds sessions until ctx ends, and sweeps their expired
// leases.
func (s *Sandboxes) Run(ctx context.Context) error {
	go s.sweep(ctx)
	go s.pumpActions(ctx)
	slots := make(chan struct{}, s.scfg.Concurrency)
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		select {
		case <-ctx.Done():
			return nil
		case slots <- struct{}{}:
		}
		claim, found, err := s.claim(ctx)
		if err != nil || !found {
			<-slots
			if err != nil && ctx.Err() == nil {
				s.log.Warn("agentd: sandbox claim failed", "err", err)
			}
			if !sleep(ctx, s.cfg.PollInterval) {
				return nil
			}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			s.drive(ctx, claim)
		}()
	}
}

// ClaimOnce claims one session and holds it to its end. It reports whether
// a session was claimed.
func (s *Sandboxes) ClaimOnce(ctx context.Context) (bool, error) {
	claim, found, err := s.claim(ctx)
	if err != nil || !found {
		return false, err
	}
	s.drive(ctx, claim)
	return true, nil
}

func (s *Sandboxes) claim(ctx context.Context) (agentstore.SandboxClaim, bool, error) {
	return s.store.ClaimSandbox(ctx, s.cfg.Owner, uuid.NewString(), s.cfg.LeaseTTL, func(plan string) int {
		p := api.Plan(plan)
		if !p.Valid() {
			return 0
		}
		return api.AgentRunConcurrentPerAccount[p.PlanIndex()]
	})
}

// SweepOnce handles expired session leases once, and deletes persisted
// workspaces past their retention or asked to be deleted.
func (s *Sandboxes) SweepOnce(ctx context.Context) error {
	requeued, failed, expired, err := s.store.ExpireSandboxLeases(ctx)
	if requeued+failed+expired > 0 {
		s.log.Info("agentd: expired sandbox leases", "requeued", requeued, "failed", failed, "expired", expired)
	}
	return errors.Join(err, s.sweepSnapshots(ctx), s.sweepScreenshots(ctx), s.sweepCaptures(ctx), s.sweepDurability(ctx), s.sweepVolumes(ctx))
}

func (s *Sandboxes) sweep(ctx context.Context) {
	tick := time.NewTicker(s.cfg.SweepInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := s.SweepOnce(ctx); err != nil && ctx.Err() == nil {
				s.log.Warn("agentd: sandbox sweep failed", "err", err)
			}
		}
	}
}

// session is one held session's state; the driving goroutine and the
// event receiver share it under mu.
type session struct {
	claim agentstore.SandboxClaim

	mu sync.Mutex
	// ready: the VM is up and its placement recorded.
	ready  bool
	frozen bool
	// freezing: a freeze was asked and has not been answered.
	freezing     bool
	inFlight     int
	lastActivity time.Time
	lease        agentstore.SandboxLease
	// end is the decided terminal state and outcome, once there is one.
	end *sessionEnd
	// persists maps each workspace persist's request to the snapshot
	// reserved for it, so a report names only this session's reservations.
	persists map[string]string
	// usage is the last usage record counted in the metrics; records are
	// cumulative, so each adds its growth.
	usage agentstore.SandboxUsage
	// A persistent session's pause (ADR-20261122132055 §1): pausing while
	// vmmd captures, the capture's ID and when it was asked, paused once
	// vmmd captured it and the stream ends without the session ending, and
	// the earliest next try after a refusal.
	pausing      bool
	captureID    string
	pauseAsked   time.Time
	paused       bool
	pauseRetryAt time.Time
	// lateCaptureID is a pause agentd stopped waiting for: vmmd may still
	// capture it, so its Paused is taken until 3× PauseTimeout after
	// lateAsked.
	lateCaptureID string
	lateAsked     time.Time
	// abandoned are pauses agentd gave up on; the stream's end records them
	// so the sweep deletes what vmmd may have written.
	abandoned []string
	// placedNode is where schedd placed the VM.
	placedNode string
}

type sessionEnd struct{ state, code, message string }

func (ss *session) decide(state, code, message string) bool {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.end != nil {
		return false
	}
	ss.end = &sessionEnd{state: state, code: code, message: message}
	return true
}

func (ss *session) decided() *sessionEnd {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return ss.end
}

// drive holds one claimed session to its end.
func (s *Sandboxes) drive(ctx context.Context, claim agentstore.SandboxClaim) {
	plan := api.Plan(claim.Plan)
	if !plan.Valid() || api.SandboxMaxLifetimeSec[plan.PlanIndex()] == 0 {
		s.finish(ctx, claim, &sessionEnd{agentstore.SandboxFailed, sandboxOutcomeUnplanned, "the account's plan has no sandbox sessions"})
		return
	}
	// A resumed session's workspace is in its capture, not the archive it was
	// created from.
	if claim.WorkspaceMissing && !claim.Resuming {
		s.finish(ctx, claim, &sessionEnd{agentstore.SandboxFailed, sandboxOutcomeWorkspace, "the snapshot the session starts from was deleted or expired"})
		return
	}
	// A resume restores its image's drive from its capture.
	if claim.ImageMissing && (!claim.Resuming || claim.Resume == nil) {
		s.finish(ctx, claim, &sessionEnd{agentstore.SandboxFailed, sandboxOutcomeImage, "the image the session boots was deleted before it started"})
		return
	}
	persistent := claim.Kind == agentstore.SandboxPersistent
	vcpu := api.AgentRunVCPUMax[plan.PlanIndex()]
	if persistent && claim.VCPU > 0 {
		vcpu = claim.VCPU
	}
	claimed := s.now()
	streamCtx, stop := context.WithCancel(ctx)
	defer stop()
	stream, err := s.schedd.RunSandbox(streamCtx)
	if err != nil {
		s.requeue(ctx, claim, err)
		return
	}
	var sendMu sync.Mutex
	send := func(req *scheddpb.SandboxRunRequest) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(req)
	}
	start := &scheddpb.SandboxRunStart{
		SandboxId: claim.ID, AccountId: claim.AccountID, Plan: claim.Plan, RamMb: int32(claim.RAMMB), Vcpu: int32(vcpu),
		CpuMillicores: int32(vcpu * 1000), NetworkMode: claim.Network, WorkspaceKey: claim.WorkspaceKey,
		ImageKey: claim.ImageKey, StartCmd: claim.StartCmd, ReadyCmd: claim.ReadyCmd, ReadyTimeoutMs: claim.ReadyTimeout.Milliseconds(),
		Browser: claim.Browser, Task: sandboxTaskToProto(claim),
		EgressAllow: claim.EgressAllow, EgressDeny: claim.EgressDeny, WebDomainLog: claim.Network == api.SandboxNetworkWeb,
		Persistent: persistent,
	}
	if persistent && claim.VolumeGB > 0 {
		// The session's /workspace is its volume, backed up while it runs
		// (ADR-20261122132055 §4).
		start.VolumeGb, start.VolumeBackupId = int32(claim.VolumeGB), claim.VolumeBackupID //nolint:gosec // G115: bounded by the plan's volume cap.
		start.VolumeBackupIntervalMs = int64(api.SandboxVolumeBackupSec) * 1000
	}
	if claim.Resuming {
		// A resumed session's workspace is in its capture; one without a
		// capture cold-boots as new (ADR-20261122132055 §2).
		start.WorkspaceKey = ""
		if c := claim.Resume; c != nil {
			// Until the shared store holds the capture, only its node can
			// restore it (ADR-20261122132055 §2).
			start.Resume = &scheddpb.SandboxRunResume{StorageKey: c.StorageKey, VmstateStorageKey: c.VMStateStorageKey,
				FcVersion: c.FCVersion, MemBytes: c.MemBytes, NodeId: c.NodeID, RequireNode: !c.Remote}
		}
	}
	if err := send(&scheddpb.SandboxRunRequest{Frame: &scheddpb.SandboxRunRequest_Start{Start: start}}); err != nil {
		s.requeue(ctx, claim, err)
		return
	}
	ss := &session{claim: claim, lease: claim.SandboxLease, lastActivity: s.now(), persists: map[string]string{}, pauseAsked: claimed}
	// A running action of an earlier claim ran in a VM that is gone.
	s.endOpenActions(ctx, claim, agentstore.SandboxActionRunning)
	defer s.actions.remove(claim.ID)
	closeVM := func() {
		_ = send(&scheddpb.SandboxRunRequest{Frame: &scheddpb.SandboxRunRequest_Close{Close: &scheddpb.SandboxRunClose{}}})
	}
	if claim.Task != nil {
		// A vmmd that never reports the task (one from before tasks) must
		// not keep the session to its lifetime.
		deadline := time.AfterFunc(sandboxTaskTimeout(claim)+sandboxTaskBootSlack, func() {
			if ss.decide(agentstore.SandboxFailed, sandboxOutcomeTaskFailed, "the task did not finish within its timeout") {
				closeVM()
			}
		})
		defer deadline.Stop()
	}
	lost := make(chan struct{})
	go s.keepSession(streamCtx, ss, closeVM, stop, lost)
	go s.timeSession(streamCtx, ss, send, closeVM)
	recvErr := s.receive(streamCtx, stream, ss, send)
	stop()
	s.recordAbandoned(ctx, ss)
	select {
	case <-lost:
		// Another replica may hold the session now; the sweeper decides.
		return
	default:
	}
	ss.mu.Lock()
	paused := ss.paused
	ss.mu.Unlock()
	if paused {
		// The session lives on, paused; a close asked meanwhile is the
		// sweeper's to end (ADR-20261122132055 §1).
		return
	}
	if end := ss.decided(); end != nil {
		s.finish(ctx, claim, end)
		return
	}
	ss.mu.Lock()
	ready := ss.ready
	ss.mu.Unlock()
	switch code := status.Code(recvErr); {
	case ctx.Err() != nil:
		// The replica is stopping; the lease lapses and the sweeper decides.
	case claim.Resuming && !ready:
		// A resume that never booted goes back to paused with its capture; a
		// refused one waits for the next request.
		s.returnResume(ctx, claim, code != codes.FailedPrecondition && code != codes.InvalidArgument && code != codes.DataLoss, recvErr)
	case ready:
		s.finish(ctx, claim, &sessionEnd{agentstore.SandboxFailed, sandboxOutcomeVMLost, "the session's VM ended unexpectedly"})
	case code == codes.DataLoss:
		s.finish(ctx, claim, &sessionEnd{agentstore.SandboxFailed, sandboxOutcomeWorkspace, "the session's persisted workspace could not be unpacked"})
	case code == codes.FailedPrecondition || code == codes.InvalidArgument:
		s.finish(ctx, claim, &sessionEnd{agentstore.SandboxFailed, sandboxOutcomeRejected, "the session is outside the account's plan"})
	default:
		s.requeue(ctx, claim, recvErr)
	}
}

// receive handles schedd's events until the stream ends.
func (s *Sandboxes) receive(ctx context.Context, stream grpc.BidiStreamingClient[scheddpb.SandboxRunRequest, scheddpb.SandboxRunEvent],
	ss *session, send func(*scheddpb.SandboxRunRequest) error) error {
	var placed *scheddpb.AgentRunPlaced
	for {
		ev, err := stream.Recv()
		if err != nil {
			return err
		}
		switch {
		case ev.GetPlaced() != nil:
			placed = ev.GetPlaced()
			ss.mu.Lock()
			ss.placedNode = placed.GetNodeId()
			ss.mu.Unlock()
			// ADR-20261117190000: the node reads the session's image and
			// snapshot through secretd grants before it boots the VM.
			if err := s.store.MarkSandboxPlaced(ctx, ss.claim.ID, ss.claim.LeaseToken, placed.GetNodeId()); err != nil {
				return err
			}
			if err := send(&scheddpb.SandboxRunRequest{Frame: &scheddpb.SandboxRunRequest_Proceed{Proceed: &scheddpb.AgentRunProceed{}}}); err != nil {
				return err
			}
		case ev.GetBooted() != nil:
			if placed == nil {
				return status.Error(codes.Internal, "schedd booted a session it never placed")
			}
			// A policy the node cannot enforce must not run (fails closed).
			if len(ss.claim.EgressAllow)+len(ss.claim.EgressDeny) > 0 && !ev.GetBooted().GetEgressPolicy() {
				if ss.decide(agentstore.SandboxFailed, sandboxOutcomeEgressPolicy, "the node cannot enforce the session's egress policy") {
					_ = send(&scheddpb.SandboxRunRequest{Frame: &scheddpb.SandboxRunRequest_Close{Close: &scheddpb.SandboxRunClose{}}})
				}
				continue
			}
			if ss.claim.Resuming {
				if err := s.markResumed(ctx, ss, placed, ev.GetBooted().GetMethod()); err != nil {
					return err
				}
			} else if err := s.store.MarkSandboxReady(ctx, ss.claim.ID, ss.claim.LeaseToken, placed.GetNodeId(), placed.GetInstance()); err != nil {
				return err
			}
			if err := s.markVolumeNode(ctx, ss, placed.GetNodeId()); err != nil {
				return err
			}
			ss.mu.Lock()
			ss.ready, ss.lastActivity = true, s.now()
			ss.mu.Unlock()
			s.log.Info("agentd: sandbox ready", "sandbox_id", ss.claim.ID, "node", placed.GetNodeId(),
				"method", ev.GetBooted().GetMethod(), "boot_ms", ev.GetBooted().GetBootMs())
			if ev.GetBooted().GetActions() && ss.claim.Browser && ss.claim.Task == nil {
				s.actions.add(ss, send)
			}
		case ev.GetActivity() != nil:
			a := ev.GetActivity()
			ss.mu.Lock()
			ss.inFlight = int(a.GetCallsInFlight())
			if ended := time.UnixMilli(a.GetLastCallEndedUnixMs()); a.GetLastCallEndedUnixMs() > 0 && ended.After(ss.lastActivity) {
				ss.lastActivity = ended
			}
			if ss.inFlight > 0 {
				ss.lastActivity = s.now()
			}
			ss.mu.Unlock()
		case ev.GetFrozen() != nil:
			if err := s.store.SetSandboxFrozen(ctx, ss.claim.ID, ss.claim.LeaseToken, true); err != nil {
				return err
			}
			ss.mu.Lock()
			ss.frozen, ss.freezing = true, false
			ss.mu.Unlock()
		case ev.GetFreezeRefused() != nil:
			ss.mu.Lock()
			ss.freezing = false
			// A refused freeze waits out another idle period.
			ss.lastActivity = s.now()
			ss.mu.Unlock()
		case ev.GetThawed() != nil:
			if err := s.store.SetSandboxFrozen(ctx, ss.claim.ID, ss.claim.LeaseToken, false); err != nil {
				return err
			}
			ss.mu.Lock()
			ss.frozen, ss.lastActivity = false, s.now()
			ss.mu.Unlock()
		case ev.GetPersistRequested() != nil:
			go s.grantPersist(ctx, ss, ev.GetPersistRequested().GetRequestId(), send)
		case ev.GetPersistDone() != nil:
			go s.recordPersist(ctx, ss, ev.GetPersistDone(), send)
		case ev.GetUsage() != nil:
			s.recordUsage(ctx, ss, ev.GetUsage())
		case ev.GetTaskDone() != nil:
			if err := s.finishTask(ctx, ss, ev.GetTaskDone(), send); err != nil {
				return err
			}
		case ev.GetActionDone() != nil:
			s.actionDone(ctx, ss, ev.GetActionDone())
		case ev.GetWebDomains() != nil:
			s.recordDomains(ctx, ss, ev.GetWebDomains())
		case ev.GetPaused() != nil:
			if err := s.markPaused(ctx, ss, ev.GetPaused()); err != nil {
				return err
			}
		case ev.GetPauseRefused() != nil:
			s.pauseRefused(ctx, ss, ev.GetPauseRefused().GetReason())
		case ev.GetVolumeBackup() != nil:
			s.recordVolumeBackup(ctx, ss, ev.GetVolumeBackup())
		}
	}
}

// keepSession renews the lease every third of its TTL with the last call
// seen, and closes the session when apid asked. A lost lease ends the
// stream, which destroys the VM.
func (s *Sandboxes) keepSession(ctx context.Context, ss *session, closeVM, stop func(), lost chan<- struct{}) {
	tick := time.NewTicker(s.cfg.LeaseTTL / 3)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		ss.mu.Lock()
		last := ss.lastActivity
		ss.mu.Unlock()
		lease, err := s.store.RenewSandbox(ctx, ss.claim.ID, ss.claim.LeaseToken, s.cfg.LeaseTTL, last)
		if errors.Is(err, agentstore.ErrLeaseLost) {
			close(lost)
			stop()
			return
		}
		if err != nil {
			if ctx.Err() == nil {
				s.log.Warn("agentd: sandbox lease renewal failed", "sandbox_id", ss.claim.ID, "err", err)
			}
			continue
		}
		ss.mu.Lock()
		ss.lease = lease
		ss.mu.Unlock()
		if lease.CloseRequested && ss.decide(agentstore.SandboxClosed, sandboxOutcomeClosed, "") {
			closeVM()
		}
	}
}

// timeSession checks the session's timers every tick: its end, its idle
// timeout and the idle freeze.
func (s *Sandboxes) timeSession(ctx context.Context, ss *session, send func(*scheddpb.SandboxRunRequest) error, closeVM func()) {
	tick := time.NewTicker(s.scfg.Tick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		now := s.now()
		ss.mu.Lock()
		idle := now.Sub(ss.lastActivity)
		if ss.inFlight > 0 {
			idle = 0
		}
		ready, frozen, freezing, lease := ss.ready, ss.frozen, ss.freezing, ss.lease
		pausing := ss.pausing || ss.paused
		freeze := ready && !frozen && !freezing && !pausing && idle >= s.scfg.IdleFreeze
		ss.mu.Unlock()
		sendFreeze := func() {
			ss.mu.Lock()
			ss.freezing = true
			ss.mu.Unlock()
			_ = send(&scheddpb.SandboxRunRequest{Frame: &scheddpb.SandboxRunRequest_Freeze{Freeze: &scheddpb.SandboxRunFreeze{}}})
		}
		if ss.claim.Kind == agentstore.SandboxPersistent {
			if pausing {
				// A vmmd from before pauses never answers one; the VM runs on.
				ss.mu.Lock()
				stuck := ss.pausing && now.Sub(ss.pauseAsked) > s.scfg.PauseTimeout
				ss.mu.Unlock()
				if stuck {
					s.pauseTimedOut(ctx, ss)
				}
				continue
			}
			if s.overran(ss, now) {
				if ss.decide(agentstore.SandboxExpired, sandboxOutcomeLifetime, "the session ran past its run limit and could not be paused") {
					closeVM()
				}
				continue
			}
			if why := s.pauseDue(ss, now, idle); why != "" {
				s.pause(ctx, ss, why, send)
				continue
			}
			if freeze {
				sendFreeze()
			}
			continue
		}
		switch {
		case !now.Before(lease.ExpiresAt):
			if ss.decide(agentstore.SandboxExpired, sandboxOutcomeLifetime, "the session reached its lifetime") {
				closeVM()
			}
		case ready && lease.IdleTimeout > 0 && idle >= lease.IdleTimeout:
			if ss.decide(agentstore.SandboxExpired, sandboxOutcomeIdle, "the session had no calls for its idle timeout") {
				closeVM()
			}
		case freeze:
			sendFreeze()
		}
	}
}

// overran reports a running persistent session PersistentOverrun past its
// run limit with no capture outstanding: every pause was refused,
// so it ends rather than run and bill on.
func (s *Sandboxes) overran(ss *session, now time.Time) bool {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.lateCaptureID != "" && now.Sub(ss.lateAsked) > 3*s.scfg.PauseTimeout {
		s.log.Warn("agentd: gave up on a late sandbox capture", "sandbox_id", ss.claim.ID, "capture_id", ss.lateCaptureID)
		ss.abandoned = append(ss.abandoned, ss.lateCaptureID)
		ss.lateCaptureID = ""
	}
	return ss.ready && !ss.pausing && ss.lateCaptureID == "" && !now.Before(ss.lease.ExpiresAt.Add(s.scfg.PersistentOverrun))
}

// pauseDue reports why a running persistent session pauses now, or "": apid
// asked, it reached its run limit, or it had no calls for the plan's idle
// pause (ADR-20261122132055 §1). A refused pause waits sandboxPauseRetry.
func (s *Sandboxes) pauseDue(ss *session, now time.Time, idle time.Duration) string {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if !ss.ready || ss.end != nil || now.Before(ss.pauseRetryAt) || ss.lateCaptureID != "" {
		return ""
	}
	plan := api.Plan(ss.claim.Plan)
	switch {
	case ss.lease.PauseRequested:
		return "requested"
	case !now.Before(ss.lease.ExpiresAt):
		return "run_limit"
	case plan.Valid() && idle >= time.Duration(api.SandboxPersistentIdlePauseSec[plan.PlanIndex()])*time.Second:
		return "idle"
	}
	return ""
}

// pause moves the session to pausing and asks vmmd to capture it.
func (s *Sandboxes) pause(ctx context.Context, ss *session, why string, send func(*scheddpb.SandboxRunRequest) error) {
	if err := s.store.BeginSandboxPause(ctx, ss.claim.ID, ss.claim.LeaseToken); err != nil {
		s.log.Warn("agentd: begin sandbox pause failed", "sandbox_id", ss.claim.ID, "err", err)
		ss.mu.Lock()
		ss.pauseRetryAt = s.now().Add(sandboxPauseRetry)
		ss.mu.Unlock()
		return
	}
	captureID := uuid.NewString()
	ss.mu.Lock()
	ss.pausing, ss.captureID, ss.pauseAsked = true, captureID, s.now()
	ss.mu.Unlock()
	s.log.Info("agentd: pausing sandbox", "sandbox_id", ss.claim.ID, "why", why, "capture_id", captureID)
	if err := send(&scheddpb.SandboxRunRequest{Frame: &scheddpb.SandboxRunRequest_Pause{Pause: &scheddpb.SandboxRunPause{CaptureId: captureID}}}); err != nil {
		s.pauseRefused(ctx, ss, "send failed")
	}
}

// markPaused records vmmd's capture: the session is paused and holds no
// lease; the stream ends after this.
func (s *Sandboxes) markPaused(ctx context.Context, ss *session, p *scheddpb.SandboxRunPaused) error {
	ss.mu.Lock()
	captureID, node, asked := ss.captureID, ss.placedNode, ss.pauseAsked
	late := captureID == "" && ss.lateCaptureID != ""
	if late {
		captureID, asked = ss.lateCaptureID, ss.lateAsked
	}
	ss.mu.Unlock()
	if captureID == "" {
		return status.Error(codes.Internal, "vmmd paused a session agentd did not pause")
	}
	if late {
		// The pause agentd stopped waiting for landed after all: the row went
		// back to ready, so it pauses again to take the capture.
		if err := s.store.BeginSandboxPause(context.WithoutCancel(ctx), ss.claim.ID, ss.claim.LeaseToken); err != nil {
			return err
		}
	}
	plan := api.Plan(ss.claim.Plan)
	retention := time.Duration(api.SandboxPausedRetentionDays[plan.PlanIndex()]) * 24 * time.Hour
	capture := agentstore.SandboxCapture{ID: captureID, NodeID: node, StorageKey: p.GetStorageKey(), VMStateStorageKey: p.GetVmstateStorageKey(),
		FCVersion: p.GetFcVersion(), MemBytes: p.GetMemBytes(), VMStateBytes: p.GetVmstateBytes(), HasBacking: p.GetHasBacking(),
		VolumeBackupID: p.GetVolumeBackupId()}
	if id := p.GetVolumeBackupId(); id != "" {
		// The capture's volume backup is the session's newest; it uploads
		// after the pause (ADR-20261122132055 §4).
		if _, err := s.store.RecordSandboxVolumeBackup(context.WithoutCancel(ctx), ss.claim.ID, ss.claim.LeaseToken, agentstore.SandboxVolumeBackup{
			ID: id, NodeID: node, Reason: agentstore.SandboxVolumeBackupPause, Uploading: true}); err != nil {
			return err
		}
	}
	if err := s.store.MarkSandboxPaused(context.WithoutCancel(ctx), ss.claim.ID, ss.claim.LeaseToken, capture, retention); err != nil {
		return err
	}
	s.actions.remove(ss.claim.ID)
	ss.mu.Lock()
	ss.paused, ss.pausing, ss.lateCaptureID = true, false, ""
	ss.mu.Unlock()
	s.metrics.sandboxPausedIn(s.now().Sub(asked))
	s.log.Info("agentd: sandbox paused", "sandbox_id", ss.claim.ID, "node", node, "capture_id", captureID,
		"mem_bytes", p.GetMemBytes(), "pause_ms", p.GetPauseMs())
	return nil
}

// pauseRefused returns a session vmmd did not pause to ready; the next try
// waits sandboxPauseRetry.
func (s *Sandboxes) pauseRefused(ctx context.Context, ss *session, reason string) {
	ss.mu.Lock()
	frozen, pausing := ss.frozen, ss.pausing
	if !pausing {
		// vmmd's answer to the pause agentd stopped waiting for.
		ss.lateCaptureID = ""
		ss.pauseRetryAt = s.now().Add(sandboxPauseRetry)
	}
	ss.mu.Unlock()
	if !pausing {
		s.log.Info("agentd: late sandbox pause refused", "sandbox_id", ss.claim.ID, "reason", reason)
		return
	}
	if err := s.store.CancelSandboxPause(ctx, ss.claim.ID, ss.claim.LeaseToken, frozen); err != nil {
		s.log.Warn("agentd: cancel sandbox pause failed", "sandbox_id", ss.claim.ID, "err", err)
	}
	ss.mu.Lock()
	ss.pausing, ss.captureID = false, ""
	ss.pauseRetryAt = s.now().Add(sandboxPauseRetry)
	ss.mu.Unlock()
	s.log.Info("agentd: sandbox pause refused", "sandbox_id", ss.claim.ID, "reason", reason)
}

// recordAbandoned records, once the stream has ended, every pause of the
// session vmmd never answered to agentd: the one in flight, a late one
// still awaited, and those given up. vmmd may have written their captures,
// so each becomes a used capture the sweep deletes.
func (s *Sandboxes) recordAbandoned(ctx context.Context, ss *session) {
	ss.mu.Lock()
	ids := ss.abandoned
	if !ss.paused {
		if ss.captureID != "" {
			ids = append(ids, ss.captureID)
		}
		if ss.lateCaptureID != "" {
			ids = append(ids, ss.lateCaptureID)
		}
	}
	node := ss.placedNode
	ss.mu.Unlock()
	if node == "" {
		node = "unknown"
	}
	for _, id := range ids {
		mem := state.SnapshotCaptureMemKey(storagekey.SandboxSessionSnapshotNamespace(ss.claim.AccountID), "", id)
		c := agentstore.SandboxCapture{ID: id, NodeID: node, StorageKey: mem, VMStateStorageKey: state.SnapshotVMStateKey(state.Snapshot{StorageKey: mem})}
		if err := s.store.RecordAbandonedSandboxCapture(context.WithoutCancel(ctx), ss.claim.ID, c); err != nil {
			s.log.Warn("agentd: record an abandoned sandbox capture failed", "sandbox_id", ss.claim.ID, "capture_id", id, "err", err)
		}
	}
}

// pauseTimedOut stops waiting for a pause vmmd has not answered: the VM runs
// on, and the capture, should vmmd still finish it, is taken late.
func (s *Sandboxes) pauseTimedOut(ctx context.Context, ss *session) {
	// Recorded first, so a Paused that lands meanwhile is still taken.
	ss.mu.Lock()
	ss.lateCaptureID, ss.lateAsked = ss.captureID, ss.pauseAsked
	ss.mu.Unlock()
	s.pauseRefused(ctx, ss, "timeout")
}

// markResumed records a resumed session on its new VM: restored from its
// capture, or cold-booted when there was none or it could not be restored.
func (s *Sandboxes) markResumed(ctx context.Context, ss *session, placed *scheddpb.AgentRunPlaced, method string) error {
	plan := api.Plan(ss.claim.Plan)
	run := time.Duration(api.SandboxPersistentRunMaxSec[plan.PlanIndex()]) * time.Second
	restored := ss.claim.Resume != nil && method == vmmdWakeRestore
	if err := s.store.MarkSandboxResumed(ctx, ss.claim.ID, ss.claim.LeaseToken, placed.GetNodeId(), placed.GetInstance(), !restored, run); err != nil {
		return err
	}
	ss.mu.Lock()
	ss.lease.ExpiresAt = s.now().Add(run)
	asked := ss.pauseAsked
	ss.mu.Unlock()
	source, reason := resumeCold, "no_capture"
	switch {
	case restored && placed.GetNodeId() == ss.claim.Resume.NodeID:
		source, reason = resumeNode, ""
	case restored:
		source, reason = resumeRemote, ""
	case ss.claim.Resume != nil:
		reason = "restore_failed"
	}
	s.metrics.sandboxResumed(source, reason, s.now().Sub(asked))
	s.log.Info("agentd: sandbox resumed", "sandbox_id", ss.claim.ID, "node", placed.GetNodeId(), "source", source, "method", method)
	return nil
}

// vmmdWakeRestore is the booted method of a restored VM, as schedd relays
// vmmd's WakeMethod name.
const vmmdWakeRestore = "WAKE_RESTORE"

// sweepCaptures deletes the objects of consumed pause captures, then their
// rows (ADR-20261122132055 §2: a capture restores once). A capture whose
// node may still be uploading it waits: an upload that landed after the
// delete would leave objects no row names.
func (s *Sandboxes) sweepCaptures(ctx context.Context) error {
	if s.scfg.Workspaces == nil {
		return nil
	}
	captures, err := s.store.ConsumedSandboxCaptures(ctx, sandboxCaptureSweepBatch)
	if err != nil {
		return err
	}
	var errs []error
	// waiting are captures still uploading; the next sweeps look past them.
	var waiting []string
	defer func() {
		if err := s.store.TouchSandboxCaptures(ctx, waiting); err != nil {
			s.log.Warn("agentd: touch sandbox captures failed", "err", err)
		}
	}()
	for _, c := range captures {
		snap := state.Snapshot{StorageKey: c.StorageKey}
		if s.now().Sub(c.CreatedAt) < sandboxCaptureUploadWindow {
			uploaded := []string{c.StorageKey, c.VMStateStorageKey, state.SnapshotDriveKey(snap)}
			if c.HasBacking {
				uploaded = append(uploaded, state.SnapshotBackingKey(snap))
			}
			held, err := s.storeHoldsAll(ctx, uploaded)
			if err != nil {
				errs = append(errs, fmt.Errorf("check capture %s: %w", c.ID, err))
				continue
			}
			if !held {
				waiting = append(waiting, c.ID)
				continue
			}
		}
		var failed bool
		for _, key := range []string{c.StorageKey, c.VMStateStorageKey, state.SnapshotDriveKey(snap), state.SnapshotBackingKey(snap)} {
			if key == "" {
				continue
			}
			if err := s.scfg.Workspaces.Delete(ctx, key); err != nil && !errors.Is(err, storage.ErrNotFound) {
				errs = append(errs, fmt.Errorf("delete capture %s object %s: %w", c.ID, key, err))
				failed = true
			}
		}
		if failed {
			continue
		}
		if err := s.store.DeleteSandboxCapture(ctx, c.ID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// sweepDurability marks a paused session's durability remote once the
// shared store holds its whole capture (ADR-20261122132055 §2): vmmd writes a
// capture to its node first and uploads it after the pause.
func (s *Sandboxes) sweepDurability(ctx context.Context) error {
	if s.scfg.Workspaces == nil {
		return nil
	}
	captures, err := s.store.NodeOnlySandboxCaptures(ctx, sandboxCaptureSweepBatch)
	if err != nil {
		return err
	}
	var errs []error
	// waiting are captures not uploaded yet; the next sweeps look past them.
	var waiting []string
	defer func() {
		if err := s.store.TouchSandboxCaptures(ctx, waiting); err != nil {
			s.log.Warn("agentd: touch sandbox captures failed", "err", err)
		}
	}()
	for _, c := range captures {
		snap := state.Snapshot{StorageKey: c.StorageKey}
		keys := []string{c.StorageKey, c.VMStateStorageKey, state.SnapshotDriveKey(snap)}
		if c.HasBacking {
			keys = append(keys, state.SnapshotBackingKey(snap))
		}
		if c.HasVolume {
			// Another node restores the capture only with the session's
			// volume as the capture left it (ADR-20261122132055 §4).
			if c.VolumeBackupID == "" {
				continue
			}
			keys = append(keys, volbackup.ManifestKey(c.AccountID, c.SessionID, c.VolumeBackupID))
		}
		held, err := s.storeHoldsAll(ctx, keys)
		if err != nil {
			errs = append(errs, fmt.Errorf("check capture %s: %w", c.ID, err))
			continue
		}
		if !held {
			waiting = append(waiting, c.ID)
			continue
		}
		if err := s.store.MarkSandboxCaptureRemote(ctx, c.SessionID, c.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		if c.VolumeBackupID != "" {
			if err := s.store.MarkSandboxVolumeBackupStored(ctx, c.VolumeBackupID); err != nil {
				errs = append(errs, err)
			}
		}
		s.log.Info("agentd: sandbox capture uploaded", "sandbox_id", c.SessionID, "capture_id", c.ID)
	}
	return errors.Join(errs...)
}

// storeHoldsAll reports whether the shared store holds every key.
func (s *Sandboxes) storeHoldsAll(ctx context.Context, keys []string) (bool, error) {
	for _, key := range keys {
		if key == "" {
			return false, nil
		}
		held, supported, err := storage.Exists(ctx, s.scfg.Workspaces, key)
		if !supported {
			rc, getErr := s.scfg.Workspaces.Get(ctx, key)
			if errors.Is(getErr, storage.ErrNotFound) {
				return false, nil
			}
			if getErr != nil {
				return false, getErr
			}
			held, err = true, rc.Close()
		}
		if err != nil || !held {
			return false, err
		}
	}
	return true, nil
}

func (s *Sandboxes) finish(ctx context.Context, claim agentstore.SandboxClaim, end *sessionEnd) {
	s.actions.remove(claim.ID)
	s.endOpenActions(ctx, claim, agentstore.SandboxActionPending, agentstore.SandboxActionRunning)
	if err := s.store.FinishSandbox(context.WithoutCancel(ctx), claim.ID, claim.LeaseToken, end.state, end.code, end.message); err != nil {
		s.log.Warn("agentd: finish sandbox failed", "sandbox_id", claim.ID, "err", err)
		return
	}
	s.log.Info("agentd: sandbox ended", "sandbox_id", claim.ID, "state", end.state, "outcome", end.code)
}

// requeue returns a session that never booted, after a capacity refusal or
// a transport failure before placement.
func (s *Sandboxes) requeue(ctx context.Context, claim agentstore.SandboxClaim, cause error) {
	if claim.Resuming {
		s.returnResume(ctx, claim, true, cause)
		return
	}
	if err := s.store.RequeueSandbox(context.WithoutCancel(ctx), claim.ID, claim.LeaseToken); err != nil {
		s.log.Warn("agentd: requeue sandbox failed", "sandbox_id", claim.ID, "err", err)
		return
	}
	s.log.Info("agentd: sandbox requeued", "sandbox_id", claim.ID, "cause", status.Code(cause).String())
}

// returnResume puts a resume that never booted back to paused.
func (s *Sandboxes) returnResume(ctx context.Context, claim agentstore.SandboxClaim, retry bool, cause error) {
	if err := s.store.ReturnSandboxResume(context.WithoutCancel(ctx), claim.ID, claim.LeaseToken, retry, time.Duration(api.SandboxResumeRetrySec)*time.Second); err != nil {
		s.log.Warn("agentd: return sandbox resume failed", "sandbox_id", claim.ID, "err", err)
		return
	}
	s.log.Info("agentd: sandbox resume returned to paused", "sandbox_id", claim.ID, "retry", retry, "cause", status.Code(cause).String())
}

// recordUsage keeps vmmd's cumulative usage of the session VM
// (ADR-20261027090013 step 11; not billed yet) and counts its growth. A
// failed write is logged: the next record carries the same totals.
func (s *Sandboxes) recordUsage(ctx context.Context, ss *session, u *scheddpb.AgentRunUsage) {
	usage := agentstore.SandboxUsage{Usage: agentstore.Usage{Method: int(u.GetMethod()), Samples: int(u.GetSamples()),
		ExpectedSamples: int(u.GetExpectedSamples()), MeasuredMillis: u.GetMeasuredMs(), PausedMillis: u.GetPausedMs(),
		PrivateMBMillis: u.GetPrivateMbMs(), UnpausedPrivateMBMillis: u.GetUnpausedPrivateMbMs(), BillableMBMillis: u.GetBillableMbMs(),
		PeakPrivateMB: u.GetPeakPrivateMb(), ColdBoot: u.GetColdBoot()}, FrozenMillis: u.GetFrozenMs(), Final: u.GetFinal()}
	ss.mu.Lock()
	prev := ss.usage
	if usage.MeasuredMillis >= prev.MeasuredMillis {
		ss.usage = usage
	}
	ss.mu.Unlock()
	if usage.MeasuredMillis >= prev.MeasuredMillis {
		s.metrics.sandboxUsed(prev, usage)
	}
	if _, err := s.store.RecordSandboxUsage(ctx, ss.claim.ID, ss.claim.LeaseToken, usage); err != nil {
		s.log.Warn("agentd: record sandbox usage", "sandbox_id", ss.claim.ID, "err", err)
	}
}

// recordDomains keeps vmmd's domain log for the session
// (ADR-20261117091026 §4). The log is best-effort: a failed write is
// logged, never fatal to the session.
func (s *Sandboxes) recordDomains(ctx context.Context, ss *session, f *scheddpb.SandboxRunWebDomains) {
	entries := make([]agentstore.SandboxDomain, 0, len(f.GetDomains()))
	for _, d := range f.GetDomains() {
		entries = append(entries, agentstore.SandboxDomain{Domain: d.GetDomain(), Refused: d.GetRefused(), FirstSeen: time.UnixMilli(d.GetAtUnixMs())})
	}
	if _, err := s.store.RecordSandboxDomains(ctx, ss.claim.ID, ss.claim.LeaseToken, api.Plan(ss.claim.Plan), entries); err != nil {
		s.log.Warn("agentd: record sandbox domains", "sandbox_id", ss.claim.ID, "err", err)
	}
}

// sandboxTaskTimeout is a task's command timeout on its plan.
func sandboxTaskTimeout(claim agentstore.SandboxClaim) time.Duration {
	plan := api.Plan(claim.Plan)
	if !plan.Valid() {
		return 0
	}
	return time.Duration(api.SandboxTaskTimeoutSec[plan.PlanIndex()]) * time.Second
}

// sandboxTaskToProto is a claim's task as vmmd runs it, or nil.
func sandboxTaskToProto(claim agentstore.SandboxClaim) *scheddpb.SandboxRunTask {
	if claim.Task == nil {
		return nil
	}
	return &scheddpb.SandboxRunTask{Kind: claim.Task.Kind, Url: claim.Task.URL, MaxChars: int32(claim.Task.MaxChars),
		TimeoutMs: sandboxTaskTimeout(claim).Milliseconds(), MaxOutputBytes: int64(api.SandboxTaskOutputMaxBytes)}
}

// finishTask records a sandbox task's end and closes its session
// (ADR-20261116182615 §3). A command that ran is task_done whatever its exit
// code, which the result carries; one that could not run is task_failed.
func (s *Sandboxes) finishTask(ctx context.Context, ss *session, d *scheddpb.SandboxRunTaskDone, send func(*scheddpb.SandboxRunRequest) error) error {
	if ss.claim.Task == nil {
		return nil
	}
	if err := s.store.RecordSandboxTaskResult(ctx, ss.claim.ID, ss.claim.LeaseToken, int(d.GetExitCode()), d.GetOutput()); err != nil {
		return err
	}
	state, code, message := agentstore.SandboxClosed, sandboxOutcomeTaskDone, ""
	switch {
	case d.GetError() != "":
		state, code, message = agentstore.SandboxFailed, sandboxOutcomeTaskFailed, "the task could not run"
	case d.GetTimedOut():
		state, code, message = agentstore.SandboxFailed, sandboxOutcomeTaskFailed, "the task's command timed out"
	}
	if ss.decide(state, code, message) {
		_ = send(&scheddpb.SandboxRunRequest{Frame: &scheddpb.SandboxRunRequest_Close{Close: &scheddpb.SandboxRunClose{}}})
	}
	return nil
}
