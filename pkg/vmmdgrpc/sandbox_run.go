package vmmdgrpc

// Sandbox session VMs (ADR-20261027090013 §4, §5). RunSandbox boots one and
// keeps it for the stream's life: schedd holds the session's reservation for
// as long, and agentd, at the far end, owns the session's state and its idle
// timers. vmmd reports the session's calls so agentd can time them without a
// database write per call, freezes the VM when agentd asks, and thaws it in
// place when a call arrives, so a call never waits on the control plane.

import (
	"context"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/sandboxproto"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storagekey"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SandboxVMMAPI is the sandbox session capability. *fcvm.Manager implements
// it.
type SandboxVMMAPI interface {
	WakeSandbox(context.Context, fcvm.SandboxWakeRequest) (*fcvm.Instance, error)
	Destroy(context.Context, string) error
}

// sandboxMeter measures a session VM's usage (ADR-20261027090013 step 11).
// *fcvm.Manager implements it; a VMM without it reports no usage.
// sandboxWebLog drains a web-mode session's domain log
// (ADR-20261117091026 §4). *fcvm.Manager implements it.
type sandboxWebLog interface {
	DrainSandboxWebDomains(instance string) []fcvm.SandboxWebDomain
}

type sandboxMeter interface {
	MeterSandbox(instance string) fcvm.SandboxUsageMeter
}

const (
	// sandboxActivityEvery bounds how often RunSandbox reports activity.
	sandboxActivityEvery = time.Second
	// sandboxDestroyTimeout bounds the teardown after the stream ends.
	sandboxDestroyTimeout = 10 * time.Second
)

// sandboxActivity is one session VM's calls. thawMu serializes the thaw of a
// frozen VM between concurrent calls. outbox carries the events a workspace
// persist sends on the session's stream, and persists its answers by request
// ID; done closes when the stream's server loop ends.
type sandboxActivity struct {
	sandboxID string
	accountID string
	plan      api.Plan

	mu        sync.Mutex
	inFlight  int
	lastEnded time.Time
	dirty     bool
	// pausing refuses new calls while a pause captures the VM
	// (ADR-20261122132055 §2).
	pausing bool
	thawMu  sync.Mutex
	thawed  chan struct{}

	outbox chan *vmmdpb.RunSandboxEvent
	// webLog is set when the caller asked for the domain log
	// (ADR-20261117091026 §4).
	webLog   bool
	done     chan struct{}
	persists sandboxPersists
	// usage measures the VM, or is nil when the VMM cannot.
	usage fcvm.SandboxUsageMeter
	// runAction runs one browser action (ADR-20261117065818 §2), or is nil
	// for a session that takes none; actionMu runs them one at a time.
	runAction func(ctx context.Context, a *vmmdpb.RunSandboxAction)
	actionMu  sync.Mutex
	// volume: the session has a workspace volume (ADR-20261122132055 §4),
	// backed up every volumeBackupEvery while it runs (zero: only at a
	// pause); volumeBackingUp runs one backup at a time.
	volume            bool
	volumeBackupEvery time.Duration
	volumeBackingUp   atomic.Bool
	// forking runs one fork capture at a time (ADR-20261122132055 §3).
	forking atomic.Bool
}

func newSandboxActivity(sandboxID string, plan api.Plan) *sandboxActivity {
	return &sandboxActivity{sandboxID: sandboxID, plan: plan, thawed: make(chan struct{}, 1),
		outbox: make(chan *vmmdpb.RunSandboxEvent), done: make(chan struct{})}
}

func (a *sandboxActivity) begin() {
	a.mu.Lock()
	a.inFlight++
	a.dirty = true
	a.mu.Unlock()
}

// tryBegin begins a call unless a pause is capturing the VM.
func (a *sandboxActivity) tryBegin() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pausing {
		return false
	}
	a.inFlight++
	a.dirty = true
	return true
}

// beginPause fences new calls for a pause, unless one is in flight.
func (a *sandboxActivity) beginPause() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inFlight > 0 || a.pausing {
		return false
	}
	a.pausing = true
	return true
}

func (a *sandboxActivity) endPause() {
	a.mu.Lock()
	a.pausing = false
	a.mu.Unlock()
}

func (a *sandboxActivity) end(now time.Time) {
	a.mu.Lock()
	a.inFlight--
	a.lastEnded, a.dirty = now, true
	a.mu.Unlock()
}

// report returns the activity to send when it changed since the last one.
func (a *sandboxActivity) report() (*vmmdpb.RunSandboxActivity, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.dirty {
		return nil, false
	}
	a.dirty = false
	out := &vmmdpb.RunSandboxActivity{CallsInFlight: int32(a.inFlight)}
	if !a.lastEnded.IsZero() {
		out.LastCallEndedUnixMs = a.lastEnded.UnixMilli()
	}
	return out, true
}

func (s *Server) sandboxActivityFor(instance string) *sandboxActivity {
	if v, ok := s.sandboxRuns.Load(instance); ok {
		return v.(*sandboxActivity)
	}
	return nil
}

// RunSandbox boots one sandbox session VM and keeps it until close or the
// stream's end.
func (s *Server) RunSandbox(stream vmmdpb.Vmmd_RunSandboxServer) error {
	const op = "RunSandbox"
	started := time.Now()
	var opErr error
	defer func() { s.ops.Observe(op, time.Since(started), opErr) }()
	sandboxVMM, ok := s.vmm.(SandboxVMMAPI)
	if !s.agentRunsEnabled || !ok {
		problem := api.NewProblem(int(codes.Unimplemented), api.CodeNotImplemented, "Sandbox sessions unavailable", "vmmd agent runs are not enabled")
		opErr = problem
		return grpcerr.ToStatus(problem)
	}
	first, err := stream.Recv()
	if err != nil {
		opErr = err
		return err
	}
	wake, problem := sandboxStartFromProto(first.GetStart())
	if problem != nil {
		opErr = problem
		return grpcerr.ToStatus(problem)
	}
	task, problem := sandboxTaskFromProto(first.GetStart(), wake)
	if problem != nil {
		opErr = problem
		return grpcerr.ToStatus(problem)
	}
	volumeGB, volumeBackupID, volumeEvery, problem := sandboxVolumeFromProto(first.GetStart(), wake.Plan)
	if problem != nil {
		opErr = problem
		return grpcerr.ToStatus(problem)
	}
	runner, _ := s.vmm.(sandboxTaskRunner)
	if task != nil && runner == nil {
		problem := api.NewProblem(int(codes.Unimplemented), api.CodeNotImplemented, "Sandbox tasks unavailable", "this vmm cannot run sandbox tasks")
		opErr = problem
		return grpcerr.ToStatus(problem)
	}
	ctx := stream.Context()
	if volumeGB > 0 {
		if err := s.prepareSandboxVolume(ctx, &wake, volumeGB, volumeBackupID, first.GetStart().GetVolumeSourceSandboxId()); err != nil {
			opErr = err
			return err
		}
	}
	inst, err := sandboxVMM.WakeSandbox(ctx, wake)
	if err != nil {
		opErr = err
		return grpcerr.ToStatus(toProblem(err))
	}
	defer func() {
		destroyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sandboxDestroyTimeout)
		defer cancel()
		_ = sandboxVMM.Destroy(destroyCtx, wake.Instance)
	}()
	// A session from a persisted workspace is booted only once its archive
	// is unpacked: a session without its files is not the one asked for.
	if key := first.GetStart().GetWorkspaceKey(); key != "" {
		if problem := s.hydrateSandboxWorkspace(ctx, wake, key); problem != nil {
			opErr = problem
			return grpcerr.ToStatus(problem)
		}
	}
	act := newSandboxActivity(wake.SandboxID, wake.Plan)
	act.accountID = wake.AccountID
	act.volume, act.volumeBackupEvery = volumeGB > 0, volumeEvery
	if meter, ok := s.vmm.(sandboxMeter); ok {
		act.usage = meter.MeterSandbox(wake.Instance)
	}
	s.sandboxRuns.Store(wake.Instance, act)
	defer func() {
		close(act.done)
		s.sandboxRuns.Delete(wake.Instance)
	}()
	if runner != nil && wake.Browser && wake.Network == api.SandboxNetworkWeb && task == nil {
		act.runAction = func(ctx context.Context, a *vmmdpb.RunSandboxAction) { s.runSandboxAction(ctx, runner, wake, act, a) }
	}
	if err := stream.Send(&vmmdpb.RunSandboxEvent{Frame: &vmmdpb.RunSandboxEvent_Booted{Booted: &vmmdpb.RunSandboxBooted{
		Method: wakeMethodFrom(inst.Method), BootMs: time.Since(started).Milliseconds(), Actions: act.runAction != nil, EgressPolicy: true,
	}}}); err != nil {
		opErr = err
		return err
	}
	act.webLog = first.GetStart().GetWebDomainLog()
	if task != nil {
		go runSandboxTask(ctx, runner, wake, *task, act)
	}
	opErr = s.serveSandbox(ctx, stream, wake.Instance, act)
	return opErr
}

// serveSandbox runs the session's frames and reports until close or the
// stream's end; it is the stream's only sender after booted.
func (s *Server) serveSandbox(ctx context.Context, stream vmmdpb.Vmmd_RunSandboxServer, instance string, act *sandboxActivity) error {
	frames := make(chan *vmmdpb.RunSandboxRequest)
	recvErr := make(chan error, 1)
	go func() {
		for {
			msg, err := stream.Recv()
			if err != nil {
				recvErr <- err
				return
			}
			select {
			case frames <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()
	ticker := time.NewTicker(sandboxActivityEvery)
	defer ticker.Stop()
	usageEvery := s.sandboxUsageEvery
	if usageEvery <= 0 {
		usageEvery = api.SandboxUsageReportSec * time.Second
	}
	usageTick := time.NewTicker(usageEvery)
	defer usageTick.Stop()
	var volumeTick <-chan time.Time
	if act.volume && act.volumeBackupEvery > 0 {
		t := time.NewTicker(act.volumeBackupEvery)
		defer t.Stop()
		volumeTick = t.C
	}
	stopped := false
	defer func() {
		if act.usage != nil && !stopped {
			act.usage.Stop()
		}
	}()
	for {
		var ev *vmmdpb.RunSandboxEvent
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-recvErr:
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		case msg := <-frames:
			switch {
			case msg.GetClose() != nil:
				// The last records go out before the stream ends, so a
				// closed session's domains and usage are complete.
				if err := s.sendSandboxWebDomains(stream, instance, act); err != nil {
					return err
				}
				if act.usage != nil {
					stopped = true
					u := act.usage.Stop()
					ev := sandboxUsageEvent(&u)
					ev.GetUsage().Final = true
					return stream.Send(ev)
				}
				return nil
			case msg.GetFreeze() != nil:
				ev = s.freezeSandbox(ctx, instance)
			case msg.GetPause() != nil:
				paused, refused, err := s.pauseSandbox(ctx, instance, act, msg.GetPause())
				if err != nil {
					return err
				}
				if refused != nil {
					ev = refused
					break
				}
				// The VM is gone: its last records, then paused, end the
				// stream (ADR-20261122132055 §2).
				if err := s.sendSandboxWebDomains(stream, instance, act); err != nil {
					return err
				}
				if act.usage != nil {
					stopped = true
					u := act.usage.Stop()
					final := sandboxUsageEvent(&u)
					final.GetUsage().Final = true
					if err := stream.Send(final); err != nil {
						return err
					}
				}
				return stream.Send(paused)
			case msg.GetFork() != nil:
				ev = s.forkSandbox(ctx, instance, act, msg.GetFork())
			case msg.GetPersistGrant() != nil:
				act.persists.deliver(msg.GetPersistGrant().GetRequestId(), msg)
			case msg.GetPersistRecorded() != nil:
				act.persists.deliver(msg.GetPersistRecorded().GetRequestId(), msg)
			case msg.GetAction() != nil:
				if act.runAction == nil {
					ev = actionDone(msg.GetAction().GetActionId(), "this session takes no browser actions")
					break
				}
				go act.runAction(ctx, msg.GetAction())
			}
		case ev = <-act.outbox:
		case <-act.thawed:
			ev = &vmmdpb.RunSandboxEvent{Frame: &vmmdpb.RunSandboxEvent_Thawed{Thawed: &vmmdpb.RunSandboxThawed{}}}
		case <-ticker.C:
			if err := s.sendSandboxWebDomains(stream, instance, act); err != nil {
				return err
			}
			if report, changed := act.report(); changed {
				ev = &vmmdpb.RunSandboxEvent{Frame: &vmmdpb.RunSandboxEvent_Activity{Activity: report}}
			}
		case <-volumeTick:
			s.backupSandboxVolume(ctx, act)
		case <-usageTick.C:
			if act.usage != nil {
				u := act.usage.Snapshot()
				ev = sandboxUsageEvent(&u)
			}
		}
		if ev == nil {
			continue
		}
		if err := stream.Send(ev); err != nil {
			return err
		}
	}
}

// sendSandboxWebDomains sends the session's domain log since the last
// frame, if the caller asked for it and there is any. A caller that did not
// ask leaves the log to its bounds (fcvm keeps at most the plan's distinct
// domains and SandboxEgressRefusedLogMax refusals).
func (s *Server) sendSandboxWebDomains(stream vmmdpb.Vmmd_RunSandboxServer, instance string, act *sandboxActivity) error {
	log, ok := s.vmm.(sandboxWebLog)
	if !ok || !act.webLog {
		return nil
	}
	domains := log.DrainSandboxWebDomains(instance)
	if len(domains) == 0 {
		return nil
	}
	frame := &vmmdpb.RunSandboxWebDomains{Domains: make([]*vmmdpb.RunSandboxWebDomain, 0, len(domains))}
	for _, d := range domains {
		frame.Domains = append(frame.Domains, &vmmdpb.RunSandboxWebDomain{Domain: d.Domain, Refused: d.Refused, AtUnixMs: d.At.UnixMilli()})
	}
	return stream.Send(&vmmdpb.RunSandboxEvent{Frame: &vmmdpb.RunSandboxEvent_WebDomains{WebDomains: frame}})
}

func sandboxUsageEvent(u *fcvm.AgentRunUsage) *vmmdpb.RunSandboxEvent {
	return &vmmdpb.RunSandboxEvent{Frame: &vmmdpb.RunSandboxEvent_Usage{Usage: agentRunUsageToProto(u)}}
}

// freezeSandbox pauses the idle VM in place. A call in flight refuses the
// freeze; so does any failure, which leaves the guest running.
func (s *Server) freezeSandbox(ctx context.Context, instance string) *vmmdpb.RunSandboxEvent {
	refused := func(reason string) *vmmdpb.RunSandboxEvent {
		return &vmmdpb.RunSandboxEvent{Frame: &vmmdpb.RunSandboxEvent_FreezeRefused{FreezeRefused: &vmmdpb.RunSandboxFreezeRefused{Reason: reason}}}
	}
	f, ok := s.vmm.(freezer)
	if !ok {
		return refused("unsupported")
	}
	if _, err := f.Freeze(ctx, instance, fcvm.FreezeOptions{}); err != nil {
		if errors.Is(err, fcvm.ErrInstanceBusy) {
			return refused("busy")
		}
		s.log.Warn("sandbox: freeze failed", "instance", instance, "err", err)
		return refused("failed")
	}
	return &vmmdpb.RunSandboxEvent{Frame: &vmmdpb.RunSandboxEvent_Frozen{Frozen: &vmmdpb.RunSandboxFrozen{}}}
}

// sandboxPauser captures a persistent session's VM and destroys it
// (ADR-20261122132055 §2); *fcvm.Manager implements it.
type sandboxPauser interface {
	PauseSandbox(ctx context.Context, p fcvm.SandboxPause) (fcvm.SandboxPaused, bool, error)
}

// sandboxCapturePublisher uploads a pause capture's node-only objects to the
// shared store (ADR-20261122132055 §2); *fcvm.Manager implements it.
type sandboxCapturePublisher interface {
	PublishSandboxCapture(ctx context.Context, memKey string) error
	UnpinSandboxCapture(memKey string) error
	PublishPendingSandboxCaptures(ctx context.Context, maxAge time.Duration) (int, error)
}

const (
	// sandboxPublishFirstRetry and sandboxPublishMaxRetry pace a failed
	// capture upload's retries; sandboxPublishGiveUp ends them, leaving the
	// capture on its node as an ordinary cache entry. agentd's sweep waits
	// out this window (sandboxCaptureUploadWindow) before it deletes a used
	// capture, so keep the two in step.
	sandboxPublishFirstRetry = 2 * time.Second
	sandboxPublishMaxRetry   = 5 * time.Minute
	sandboxPublishGiveUp     = time.Duration(api.SandboxCaptureUploadSec) * time.Second
)

// publishSandboxCapture uploads a pause capture in the background, retrying
// until it succeeds or sandboxPublishGiveUp passes. agentd sees the upload
// in the shared store and marks the session's durability remote.
func (s *Server) publishSandboxCapture(ctx context.Context, memKey string) {
	pub, ok := s.vmm.(sandboxCapturePublisher)
	if !ok {
		return
	}
	started := time.Now()
	s.retryInBackground(ctx, "SandboxCaptureUpload", "capture upload", memKey, func(ctx context.Context) error {
		return pub.PublishSandboxCapture(ctx, memKey)
	}, func() {
		s.log.Info("sandbox: capture uploaded", "storage_key", memKey, "upload_ms", time.Since(started).Milliseconds())
	}, func() {
		// Left to the node cache's eviction rather than pinned for good.
		if err := pub.UnpinSandboxCapture(memKey); err != nil {
			s.log.Warn("sandbox: unpin a given-up capture failed", "storage_key", memKey, "err", err)
		}
	})
}

// retryInBackground runs upload until it succeeds or sandboxPublishGiveUp
// passes, backing off between tries, outliving the caller's stream; done
// runs after a success and gaveUp after the last failure.
func (s *Server) retryInBackground(ctx context.Context, op, what, key string, upload func(context.Context) error, done, gaveUp func()) {
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sandboxPublishGiveUp)
		defer cancel()
		started := time.Now()
		wait := sandboxPublishFirstRetry
		for {
			err := upload(ctx)
			s.ops.Observe(op, time.Since(started), err)
			if err == nil {
				if done != nil {
					done()
				}
				return
			}
			s.log.Warn("sandbox: "+what+" failed; retrying", "key", key, "retry_in", wait, "err", err)
			select {
			case <-ctx.Done():
				s.log.Warn("sandbox: "+what+" gave up; it stays on this node", "key", key)
				if gaveUp != nil {
					gaveUp()
				}
				return
			case <-time.After(wait):
			}
			wait = min(2*wait, sandboxPublishMaxRetry)
		}
	}()
}

// PublishPendingSandboxCaptures uploads the pause captures a restart left on
// this node alone, retrying as a pause's own upload does until none is left
// or sandboxPublishGiveUp passes. vmmd runs it at start, in the background.
func (s *Server) PublishPendingSandboxCaptures(ctx context.Context) {
	pub, ok := s.vmm.(sandboxCapturePublisher)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, sandboxPublishGiveUp)
	defer cancel()
	wait := sandboxPublishFirstRetry
	for {
		n, err := pub.PublishPendingSandboxCaptures(ctx, sandboxPublishGiveUp)
		if n > 0 {
			s.log.Info("sandbox: uploaded pending capture objects", "objects", n)
		}
		if err == nil {
			return
		}
		s.log.Warn("sandbox: pending capture uploads failed; retrying", "uploaded", n, "retry_in", wait, "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(2*wait, sandboxPublishMaxRetry)
	}
}

// pauseSandbox captures the session VM. It answers paused, or refused when
// the VM keeps running: a call in flight, a thaw or capture that failed
// before the guest was touched. An error is a VM lost to a failed capture,
// which ends the stream as a lost VM does.
func (s *Server) pauseSandbox(ctx context.Context, instance string, act *sandboxActivity, p *vmmdpb.RunSandboxPause) (paused, refused *vmmdpb.RunSandboxEvent, err error) {
	refuse := func(reason string) (*vmmdpb.RunSandboxEvent, *vmmdpb.RunSandboxEvent, error) {
		return nil, &vmmdpb.RunSandboxEvent{Frame: &vmmdpb.RunSandboxEvent_PauseRefused{PauseRefused: &vmmdpb.RunSandboxPauseRefused{Reason: reason}}}, nil
	}
	pauser, ok := s.vmm.(sandboxPauser)
	if !ok {
		return refuse("unsupported")
	}
	if id, err := uuid.Parse(p.GetCaptureId()); err != nil || id.String() != p.GetCaptureId() {
		return refuse("invalid")
	}
	if act.forking.Load() || !act.beginPause() {
		return refuse("busy")
	}
	defer act.endPause()
	if err := s.thawSandbox(ctx, instance, act); err != nil {
		s.log.Warn("sandbox: thaw before a pause failed", "instance", instance, "err", err)
		return refuse("failed")
	}
	started := time.Now()
	captured, kept, err := pauser.PauseSandbox(ctx, fcvm.SandboxPause{Instance: instance, SandboxID: act.sandboxID,
		AccountID: act.accountID, CaptureID: p.GetCaptureId()})
	if err != nil {
		s.log.Warn("sandbox: pause failed", "instance", instance, "vm_kept", kept, "err", err)
		if kept {
			return refuse("failed")
		}
		return nil, nil, status.Error(codes.Aborted, "sandbox pause failed: the session's VM ended while it was being captured")
	}
	snap := captured.Snapshot
	s.publishSandboxCapture(ctx, snap.StorageKey)
	// The VM is gone, so the volume is as the capture left it.
	volumeBackup := s.prepareSandboxPauseBackup(ctx, act)
	return &vmmdpb.RunSandboxEvent{Frame: &vmmdpb.RunSandboxEvent_Paused{Paused: &vmmdpb.RunSandboxPaused{
		Snapshot: &vmmdpb.AgentRunSnapshot{StorageKey: snap.StorageKey, VmstateStorageKey: snap.VMStateStorageKey,
			FcVersion: snap.FCVersion, MemBytes: snap.MemBytes},
		VmstateBytes: captured.Info.VMStateBytes, PauseMs: time.Since(started).Milliseconds(), HasBacking: captured.HasBacking,
		VolumeBackupId: volumeBackup}}}, nil, nil
}

// thawSandbox thaws a frozen session VM in place before a call. A freeze in
// progress is waited out first, so a call never meets a half-frozen guest.
func (s *Server) thawSandbox(ctx context.Context, instance string, act *sandboxActivity) error {
	gate, ok := s.vmm.(frozenGate)
	if !ok {
		return nil
	}
	act.thawMu.Lock()
	defer act.thawMu.Unlock()
	frozen, err := gate.AwaitFrozen(ctx, instance)
	if err != nil || !frozen {
		return err
	}
	t, ok := s.vmm.(thawer)
	if !ok {
		return errors.New("vmmd cannot thaw")
	}
	if err := t.ThawWith(ctx, instance, fcvm.ThawOptions{}); err != nil {
		return err
	}
	select {
	case act.thawed <- struct{}{}:
	default:
	}
	return nil
}

func sandboxStartFromProto(start *vmmdpb.RunSandboxStart) (fcvm.SandboxWakeRequest, *api.Problem) {
	invalid := func(detail string) *api.Problem {
		return api.NewProblem(int(codes.InvalidArgument), api.CodeValidation, "Invalid sandbox session", detail)
	}
	if start == nil {
		return fcvm.SandboxWakeRequest{}, invalid("the first frame must be start")
	}
	wake := fcvm.SandboxWakeRequest{
		Instance: start.GetInstance(), SandboxID: start.GetSandboxId(), AccountID: start.GetAccountId(), Plan: api.Plan(start.GetPlan()),
		BaseKey: start.GetBaseKey(), LayerKey: start.GetLayerKey(), VcpuCount: int(start.GetVcpuCount()),
		MemSizeMiB: int(start.GetMemSizeMib()), CPUMillicores: int(start.GetCpuMillicores()), Network: start.GetNetworkMode(),
		ImageKey: start.GetImageKey(), StartCmd: start.GetStartCmd(), ReadyCmd: start.GetReadyCmd(),
		ReadyTimeout: time.Duration(start.GetReadyTimeoutMs()) * time.Millisecond, Browser: start.GetBrowser(),
	}
	// A schedd from before network modes sends none.
	if wake.Network == "" {
		wake.Network = api.SandboxNetworkNone
	}
	if wake.Instance == "" || wake.SandboxID == "" || wake.AccountID == "" || !wake.Plan.Valid() || wake.BaseKey == "" ||
		wake.LayerKey == "" || wake.VcpuCount < 1 || wake.MemSizeMiB < 1 || wake.CPUMillicores < 1 {
		return fcvm.SandboxWakeRequest{}, invalid("instance, sandbox, account, plan, base, layer and a positive machine shape are required")
	}
	switch wake.Network {
	case api.SandboxNetworkNone, api.SandboxNetworkRegistries, api.SandboxNetworkWeb:
	default:
		return fcvm.SandboxWakeRequest{}, invalid("network_mode must be none, registries or web")
	}
	// Only a materialized job image or a sandbox template build boots as a
	// session's drive1, never another app's layer, a base or a snapshot.
	if wake.ImageKey != "" && !storagekey.IsJobLayer(wake.ImageKey) && !storagekey.IsSandboxTemplateLayer(wake.ImageKey) {
		return fcvm.SandboxWakeRequest{}, invalid("image_key must be a job image or sandbox template artifact")
	}
	// Commands ride in sandbox.json on the private copy of an image drive;
	// the platform sandbox layer carries none.
	if (wake.StartCmd != "" || wake.ReadyCmd != "") && wake.ImageKey == "" {
		return fcvm.SandboxWakeRequest{}, invalid("start_cmd and ready_cmd need image_key")
	}
	if wake.Browser && wake.ImageKey != "" {
		return fcvm.SandboxWakeRequest{}, invalid("browser goes with a preset session, not image_key")
	}
	if problem := sandboxEgressPolicy(start, &wake, invalid); problem != nil {
		return fcvm.SandboxWakeRequest{}, problem
	}
	if wake.ReadyTimeout < 0 || wake.ReadyTimeout > time.Duration(api.SandboxTemplateReadyTimeoutSecMax)*time.Second ||
		wake.ReadyCmd != "" && wake.ReadyTimeout == 0 {
		return fcvm.SandboxWakeRequest{}, invalid("ready_timeout_ms is out of range")
	}
	ms := start.GetQuietPauseAfterMs()
	if ms < 0 || ms > api.AgentWaitPauseAfterMaxMillis {
		return fcvm.SandboxWakeRequest{}, invalid("quiet_pause_after_ms is out of range")
	}
	wake.QuietPauseAfter = time.Duration(ms) * time.Millisecond
	if start.GetSessionCapture() {
		// A pause capture (ADR-20261122132055 §2) restores only into a session
		// of its own account, and carries the session's workspace itself.
		snap := start.GetSnapshot()
		switch {
		case snap == nil:
			return fcvm.SandboxWakeRequest{}, invalid("session_capture needs the capture's snapshot")
		case start.GetWorkspaceKey() != "":
			return fcvm.SandboxWakeRequest{}, invalid("a session capture carries its workspace; workspace_key goes only with a new session")
		case start.GetTask() != nil:
			return fcvm.SandboxWakeRequest{}, invalid("a sandbox task is never paused")
		case !storagekey.IsSandboxSessionSnapshotKey(snap.GetStorageKey(), wake.AccountID) ||
			snap.GetVmstateStorageKey() != state.SnapshotVMStateKey(state.Snapshot{StorageKey: snap.GetStorageKey()}) ||
			snap.GetFcVersion() == "" || snap.GetMemBytes() < 1:
			return fcvm.SandboxWakeRequest{}, invalid("snapshot must be a pause capture of this session's account, with its keys, version and size")
		}
		wake.Snapshot = &fcvm.Snapshot{StorageKey: snap.GetStorageKey(), VMStateStorageKey: snap.GetVmstateStorageKey(),
			FCVersion: snap.GetFcVersion(), MemBytes: snap.GetMemBytes(), Networkless: wake.Network == api.SandboxNetworkNone}
		wake.SessionCapture = true
		return wake, nil
	}
	if snap := start.GetSnapshot(); snap != nil {
		if !storagekey.IsSandboxSnapshotKey(snap.GetStorageKey(), wake.AccountID, wake.ImageKey != "") ||
			snap.GetVmstateStorageKey() != state.SnapshotVMStateKey(state.Snapshot{StorageKey: snap.GetStorageKey()}) ||
			snap.GetFcVersion() == "" || snap.GetMemBytes() < 1 {
			return fcvm.SandboxWakeRequest{}, invalid("snapshot must be a sandbox capture of this session's kind, with its keys, version and size")
		}
		wake.Snapshot = &fcvm.Snapshot{StorageKey: snap.GetStorageKey(), VMStateStorageKey: snap.GetVmstateStorageKey(),
			FCVersion: snap.GetFcVersion(), MemBytes: snap.GetMemBytes(), Networkless: wake.Network == api.SandboxNetworkNone}
	}
	return wake, nil
}

// CaptureSandboxSnapshot captures a session VM's warm snapshot (ADR-20261027090013
// §12). Like RunSandbox it is refused unless agent runs are on.
func (s *Server) CaptureSandboxSnapshot(ctx context.Context, req *vmmdpb.CaptureSandboxSnapshotRequest) (*vmmdpb.CaptureAgentSnapshotResponse, error) {
	const op = "CaptureSandboxSnapshot"
	started := time.Now()
	var opErr error
	defer func() { s.ops.Observe(op, time.Since(started), opErr) }()
	capturer, ok := s.vmm.(interface {
		CaptureSandboxSnapshot(context.Context, fcvm.SandboxCapture) (*fcvm.Snapshot, fcvm.SnapshotInfo, error)
	})
	if !s.agentRunsEnabled || !ok {
		problem := api.NewProblem(int(codes.Unimplemented), api.CodeNotImplemented, "Sandbox sessions unavailable", "vmmd agent runs are not enabled")
		opErr = problem
		return nil, grpcerr.ToStatus(problem)
	}
	wake, problem := sandboxStartFromProto(&vmmdpb.RunSandboxStart{Instance: req.GetInstance(), SandboxId: req.GetCaptureId(),
		AccountId: req.GetAccountId(), Plan: req.GetPlan(), BaseKey: req.GetBaseKey(), LayerKey: req.GetLayerKey(),
		ImageKey: req.GetImageKey(), VcpuCount: req.GetVcpuCount(), MemSizeMib: req.GetMemSizeMib(),
		CpuMillicores: req.GetCpuMillicores(), NetworkMode: req.GetNetworkMode(), Browser: req.GetBrowser()})
	if problem == nil {
		if req.GetNamespace() != storagekey.SandboxSnapshotNamespace(wake.AccountID, wake.ImageKey != "") {
			problem = api.NewProblem(int(codes.InvalidArgument), api.CodeValidation, "Invalid sandbox capture",
				"the namespace must be the session kind's own")
		}
	}
	if problem != nil {
		opErr = problem
		return nil, grpcerr.ToStatus(problem)
	}
	snap, info, err := capturer.CaptureSandboxSnapshot(ctx, fcvm.SandboxCapture{Wake: wake, Namespace: req.GetNamespace(), CaptureID: req.GetCaptureId()})
	if err != nil {
		opErr = err
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	return &vmmdpb.CaptureAgentSnapshotResponse{VmstateBytes: info.VMStateBytes, Snapshot: &vmmdpb.AgentRunSnapshot{
		StorageKey: snap.StorageKey, VmstateStorageKey: snap.VMStateStorageKey, FcVersion: snap.FCVersion, MemBytes: snap.MemBytes,
	}}, nil
}

// sandboxEgressPolicy validates a start frame's domain policy
// (ADR-20261117091026 §3) and puts it on the wake: rules only for a web-mode
// session, each a domain rule, within SandboxEgressRulesMax (deny may hold
// the account's and the session's).
func sandboxEgressPolicy(start *vmmdpb.RunSandboxStart, wake *fcvm.SandboxWakeRequest, invalid func(string) *api.Problem) *api.Problem {
	allow, deny := start.GetEgressAllow(), start.GetEgressDeny()
	if len(allow)+len(deny) == 0 {
		return nil
	}
	if wake.Network != api.SandboxNetworkWeb {
		return invalid("egress_allow and egress_deny need network_mode web")
	}
	if len(allow) > api.SandboxEgressRulesMax || len(deny) > 2*api.SandboxEgressRulesMax {
		return invalid("the egress policy holds too many rules")
	}
	for _, rule := range append(slices.Clone(allow), deny...) {
		if !api.ValidSandboxEgressRule(rule) {
			return invalid("egress policy rule " + strconv.Quote(rule) + " is not a domain rule")
		}
	}
	wake.EgressAllow, wake.EgressDeny = slices.Clone(allow), slices.Clone(deny)
	return nil
}

// sandboxTaskRunner runs a sandbox task's command in its session VM
// (ADR-20261116182615 §3); *fcvm.Manager implements it.
type sandboxTaskRunner interface {
	RunSandboxTask(ctx context.Context, instance, sandboxID string, argv []string, timeout time.Duration, maxOutput int64) (fcvm.SandboxTaskResult, error)
}

// sandboxTask is a validated sandbox task: the command vmmd runs and its
// bounds.
type sandboxTask struct {
	argv      []string
	timeout   time.Duration
	maxOutput int64
}

// sandboxTaskFromProto validates a start frame's task and builds its
// command. A kind is a fixed command; the caller's fields reach it only as
// validated arguments after "--", never as shell text.
func sandboxTaskFromProto(start *vmmdpb.RunSandboxStart, wake fcvm.SandboxWakeRequest) (*sandboxTask, *api.Problem) {
	t := start.GetTask()
	if t == nil {
		return nil, nil
	}
	invalid := func(detail string) *api.Problem {
		return api.NewProblem(int(codes.InvalidArgument), api.CodeValidation, "Invalid sandbox task", detail)
	}
	timeout := time.Duration(t.GetTimeoutMs()) * time.Millisecond
	idx := wake.Plan.PlanIndex()
	switch {
	case t.GetKind() != "browse":
		return nil, invalid("task kind must be browse")
	case !wake.Browser || wake.Network != api.SandboxNetworkWeb:
		return nil, invalid("a browse task needs a browser session in web mode")
	case !api.ValidSandboxBrowseURL(t.GetUrl()):
		return nil, invalid("url must be an http or https URL of a public host")
	case t.GetMaxChars() < 1 || int(t.GetMaxChars()) > api.SandboxBrowseMaxChars:
		return nil, invalid("max_chars is out of range")
	case timeout <= 0 || timeout > time.Duration(api.SandboxTaskTimeoutSec[idx])*time.Second:
		return nil, invalid("timeout_ms is outside the plan")
	case t.GetMaxOutputBytes() < 1 || t.GetMaxOutputBytes() > int64(api.SandboxTaskOutputMaxBytes):
		return nil, invalid("max_output_bytes is out of range")
	}
	argv := []string{"gregale-browser", "fetch", "--max-chars", strconv.Itoa(int(t.GetMaxChars())), "--timeout", strconv.Itoa(int(timeout.Seconds())),
		"--", t.GetUrl()}
	return &sandboxTask{argv: argv, timeout: timeout, maxOutput: t.GetMaxOutputBytes()}, nil
}

// runSandboxTask runs the task once the session is booted and puts its end
// on the session's stream. It counts as a call, so the session does not
// freeze under it.
func runSandboxTask(ctx context.Context, runner sandboxTaskRunner, wake fcvm.SandboxWakeRequest, task sandboxTask, act *sandboxActivity) {
	act.begin()
	res, err := runner.RunSandboxTask(ctx, wake.Instance, wake.SandboxID, task.argv, task.timeout, task.maxOutput)
	act.end(time.Now())
	done := &vmmdpb.RunSandboxTaskDone{ExitCode: int32(res.ExitCode), Output: res.Output, TimedOut: res.TimedOut, Truncated: res.Truncated}
	if err != nil {
		done = &vmmdpb.RunSandboxTaskDone{ExitCode: -1, Error: err.Error()}
	}
	select {
	case act.outbox <- &vmmdpb.RunSandboxEvent{Frame: &vmmdpb.RunSandboxEvent_TaskDone{TaskDone: done}}:
	case <-act.done:
	case <-ctx.Done():
	}
}

// sandboxActionCommand validates a browser action and builds its command
// (ADR-20261117065818 §2). The command is fixed; the caller's fields reach
// it only as validated flags and, after "--", one value.
func sandboxActionCommand(a *vmmdpb.RunSandboxAction, plan api.Plan) (argv []string, timeout time.Duration, maxOutput int64, problem string) {
	idx := plan.PlanIndex()
	timeout = time.Duration(a.GetTimeoutMs()) * time.Millisecond
	switch {
	case timeout <= 0 || timeout > time.Duration(api.SandboxBrowserActionTimeoutSec[idx])*time.Second:
		return nil, 0, 0, "timeout_ms is outside the plan"
	case a.GetMaxOutputBytes() < 1 || a.GetMaxOutputBytes() > int64(api.SandboxTaskOutputMaxBytes):
		return nil, 0, 0, "max_output_bytes is out of range"
	case a.GetMaxChars() < 1 || int(a.GetMaxChars()) > api.SandboxBrowseMaxChars:
		return nil, 0, 0, "max_chars is out of range"
	}
	argv = []string{"gregale-browser", "act", a.GetKind(), "--max-chars", strconv.Itoa(int(a.GetMaxChars())),
		"--timeout", strconv.Itoa(int(timeout.Seconds()))}
	if a.GetScreenshot() {
		path, ok := sandboxScreenshotPath(a.GetActionId())
		if !ok {
			return nil, 0, 0, "a screenshot needs an action_id that is a UUID"
		}
		argv = append(argv, "--screenshot", path)
	}
	ref := func() bool {
		if a.GetRef() < 1 || a.GetRef() > 10_000 {
			return false
		}
		argv = append(argv, "--ref", strconv.Itoa(int(a.GetRef())))
		return true
	}
	switch a.GetKind() {
	case "navigate":
		if !api.ValidSandboxBrowseURL(a.GetUrl()) {
			return nil, 0, 0, "url must be an http or https URL of a public host"
		}
		argv = append(argv, "--", a.GetUrl())
	case "click":
		if !ref() {
			return nil, 0, 0, "ref is out of range"
		}
	case "type":
		if !ref() {
			return nil, 0, 0, "ref is out of range"
		}
		if utf8.RuneCountInString(a.GetText()) > api.SandboxBrowserTypeMaxChars || !utf8.ValidString(a.GetText()) ||
			strings.ContainsRune(a.GetText(), 0) {
			return nil, 0, 0, "text is too long or not text"
		}
		if a.GetSubmit() {
			argv = append(argv, "--submit")
		}
		argv = append(argv, "--", a.GetText())
	case "press":
		if !sandboxActionKeys[a.GetKey()] {
			return nil, 0, 0, "key is not one press takes"
		}
		argv = append(argv, "--key", a.GetKey())
	case "scroll":
		if a.GetDirection() != "up" && a.GetDirection() != "down" {
			return nil, 0, 0, "direction must be up or down"
		}
		argv = append(argv, "--direction", a.GetDirection())
	case "back", "snapshot":
	default:
		return nil, 0, 0, "kind must be navigate, click, type, press, scroll, back or snapshot"
	}
	return argv, timeout, a.GetMaxOutputBytes(), ""
}

// sandboxActionKeys are the keys press takes, as gregale-browser's ACT_KEYS.
var sandboxActionKeys = map[string]bool{"Enter": true, "Tab": true, "Escape": true, "Backspace": true, "ArrowUp": true,
	"ArrowDown": true, "ArrowLeft": true, "ArrowRight": true, "PageUp": true, "PageDown": true, "Home": true, "End": true}

func actionDone(id, problem string) *vmmdpb.RunSandboxEvent {
	return &vmmdpb.RunSandboxEvent{Frame: &vmmdpb.RunSandboxEvent_ActionDone{ActionDone: &vmmdpb.RunSandboxActionDone{
		ActionId: id, ExitCode: -1, Error: problem}}}
}

// runSandboxAction runs one browser action and puts its end on the
// session's stream. It is a call: a frozen VM is thawed first, and the VM
// does not freeze under it.
func (s *Server) runSandboxAction(ctx context.Context, runner sandboxTaskRunner, wake fcvm.SandboxWakeRequest, act *sandboxActivity,
	a *vmmdpb.RunSandboxAction) {
	ev := s.sandboxActionEnd(ctx, runner, wake, act, a)
	select {
	case act.outbox <- ev:
	case <-act.done:
	case <-ctx.Done():
	}
}

func (s *Server) sandboxActionEnd(ctx context.Context, runner sandboxTaskRunner, wake fcvm.SandboxWakeRequest, act *sandboxActivity,
	a *vmmdpb.RunSandboxAction) *vmmdpb.RunSandboxEvent {
	argv, timeout, maxOutput, problem := sandboxActionCommand(a, wake.Plan)
	if problem != "" {
		return actionDone(a.GetActionId(), problem)
	}
	act.actionMu.Lock()
	defer act.actionMu.Unlock()
	if !act.tryBegin() {
		return actionDone(a.GetActionId(), "the session is pausing")
	}
	defer func() { act.end(time.Now()) }()
	s.beginActivity(wake.Instance)
	defer s.endActivity(wake.Instance)
	if err := s.thawSandbox(ctx, wake.Instance, act); err != nil {
		s.log.Warn("sandbox: thaw before an action failed", "instance", wake.Instance, "err", err)
		return actionDone(a.GetActionId(), "the session could not be thawed")
	}
	if err := s.prepareGuest(ctx, wake.Instance); err != nil {
		return actionDone(a.GetActionId(), "the session is resuming")
	}
	res, err := runner.RunSandboxTask(ctx, wake.Instance, wake.SandboxID, argv, timeout, maxOutput)
	if err != nil {
		return actionDone(a.GetActionId(), err.Error())
	}
	var shot int64
	if a.GetScreenshot() {
		if res.ExitCode == 0 {
			shot = s.storeScreenshot(ctx, wake, a.GetActionId())
		}
		s.removeScreenshot(ctx, wake, a.GetActionId())
	}
	return &vmmdpb.RunSandboxEvent{Frame: &vmmdpb.RunSandboxEvent_ActionDone{ActionDone: &vmmdpb.RunSandboxActionDone{
		ActionId: a.GetActionId(), ExitCode: int32(res.ExitCode), Output: res.Output, TimedOut: res.TimedOut, Truncated: res.Truncated,
		ScreenshotBytes: shot}}}
}

// sandboxScreenshotPath is where gregale-browser act writes an action's
// screenshot: in the session's workspace, which the guest's file operations
// are confined to (ADR-20261117065818 §5).
func sandboxScreenshotPath(actionID string) (string, bool) {
	id, err := uuid.Parse(actionID)
	if err != nil || id.String() != actionID {
		return "", false
	}
	return "/workspace/.gregale-screenshot-" + actionID + ".jpg", true
}

// storeScreenshot streams an action's screenshot from the guest to the
// artifact store and returns its size, or 0 when none was kept: no store,
// no file, a failure, or an image over SandboxScreenshotMaxBytes.
func (s *Server) storeScreenshot(ctx context.Context, wake fcvm.SandboxWakeRequest, actionID string) int64 {
	sessions, ok := s.vmm.(sandboxSessions)
	if !ok || s.sandboxStorage == nil {
		return 0
	}
	path, _ := sandboxScreenshotPath(actionID)
	key := storagekey.SandboxScreenshot(wake.AccountID, wake.SandboxID, actionID)
	conn, err := sessions.DialSandboxSession(ctx, wake.Instance, wake.SandboxID)
	if err != nil {
		return 0
	}
	pr, pw := io.Pipe()
	stored := make(chan error, 1)
	go func() {
		err := s.sandboxStorage.Put(ctx, key, pr)
		_ = pr.CloseWithError(err)
		stored <- err
	}()
	var result sandboxproto.TransferResult
	req := sandboxproto.Request{Op: sandboxproto.OpFileRead, Path: path, MaxBytes: int64(api.SandboxScreenshotMaxBytes)}
	guestErr := sandboxproto.Do(ctx, conn, req, func(_ uint32, body []byte) error {
		_, err := pw.Write(body)
		return err
	}, &result)
	_ = pw.CloseWithError(guestErr)
	storeErr := <-stored
	if guestErr == nil && storeErr == nil && !result.Truncated && result.Bytes > 0 {
		return result.Bytes
	}
	if storeErr != nil {
		s.log.Warn("sandbox: screenshot store failed", "instance", wake.Instance, "err", storeErr)
	}
	s.deleteWorkspaceArchive(ctx, key)
	return 0
}

// removeScreenshot deletes an action's screenshot from the guest, kept or
// not.
func (s *Server) removeScreenshot(ctx context.Context, wake fcvm.SandboxWakeRequest, actionID string) {
	sessions, ok := s.vmm.(sandboxSessions)
	if !ok {
		return
	}
	path, _ := sandboxScreenshotPath(actionID)
	conn, err := sessions.DialSandboxSession(ctx, wake.Instance, wake.SandboxID)
	if err != nil {
		return
	}
	var out struct{}
	_ = sandboxproto.Do(ctx, conn, sandboxproto.Request{Op: sandboxproto.OpFileRemove, Path: path},
		func(uint32, []byte) error { return nil }, &out)
}
