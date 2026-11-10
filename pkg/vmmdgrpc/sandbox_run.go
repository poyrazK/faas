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
	"sync"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/storagekey"
	"google.golang.org/grpc/codes"
)

// SandboxVMMAPI is the sandbox session capability. *fcvm.Manager implements
// it.
type SandboxVMMAPI interface {
	WakeSandbox(context.Context, fcvm.SandboxWakeRequest) (*fcvm.Instance, error)
	Destroy(context.Context, string) error
}

// sandboxMeter measures a session VM's usage (ADR-20261027090013 step 11).
// *fcvm.Manager implements it; a VMM without it reports no usage.
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
	plan      api.Plan

	mu        sync.Mutex
	inFlight  int
	lastEnded time.Time
	dirty     bool
	thawMu    sync.Mutex
	thawed    chan struct{}

	outbox   chan *vmmdpb.RunSandboxEvent
	done     chan struct{}
	persists sandboxPersists
	// usage measures the VM, or is nil when the VMM cannot.
	usage fcvm.SandboxUsageMeter
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
	ctx := stream.Context()
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
	if meter, ok := s.vmm.(sandboxMeter); ok {
		act.usage = meter.MeterSandbox(wake.Instance)
	}
	s.sandboxRuns.Store(wake.Instance, act)
	defer func() {
		close(act.done)
		s.sandboxRuns.Delete(wake.Instance)
	}()
	if err := stream.Send(&vmmdpb.RunSandboxEvent{Frame: &vmmdpb.RunSandboxEvent_Booted{Booted: &vmmdpb.RunSandboxBooted{
		Method: wakeMethodFrom(inst.Method), BootMs: time.Since(started).Milliseconds(),
	}}}); err != nil {
		opErr = err
		return err
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
				// The last record goes out before the stream ends, so a
				// closed session's usage is complete.
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
			case msg.GetPersistGrant() != nil:
				act.persists.deliver(msg.GetPersistGrant().GetRequestId(), msg)
			case msg.GetPersistRecorded() != nil:
				act.persists.deliver(msg.GetPersistRecorded().GetRequestId(), msg)
			}
		case ev = <-act.outbox:
		case <-act.thawed:
			ev = &vmmdpb.RunSandboxEvent{Frame: &vmmdpb.RunSandboxEvent_Thawed{Thawed: &vmmdpb.RunSandboxThawed{}}}
		case <-ticker.C:
			if report, changed := act.report(); changed {
				ev = &vmmdpb.RunSandboxEvent{Frame: &vmmdpb.RunSandboxEvent_Activity{Activity: report}}
			}
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
		ReadyTimeout: time.Duration(start.GetReadyTimeoutMs()) * time.Millisecond,
	}
	// A schedd from before network modes sends none.
	if wake.Network == "" {
		wake.Network = api.SandboxNetworkNone
	}
	if wake.Instance == "" || wake.SandboxID == "" || wake.AccountID == "" || !wake.Plan.Valid() || wake.BaseKey == "" ||
		wake.LayerKey == "" || wake.VcpuCount < 1 || wake.MemSizeMiB < 1 || wake.CPUMillicores < 1 {
		return fcvm.SandboxWakeRequest{}, invalid("instance, sandbox, account, plan, base, layer and a positive machine shape are required")
	}
	if wake.Network != api.SandboxNetworkNone && wake.Network != api.SandboxNetworkRegistries {
		return fcvm.SandboxWakeRequest{}, invalid("network_mode must be none or registries")
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
	if wake.ReadyTimeout < 0 || wake.ReadyTimeout > time.Duration(api.SandboxTemplateReadyTimeoutSecMax)*time.Second ||
		wake.ReadyCmd != "" && wake.ReadyTimeout == 0 {
		return fcvm.SandboxWakeRequest{}, invalid("ready_timeout_ms is out of range")
	}
	ms := start.GetQuietPauseAfterMs()
	if ms < 0 || ms > api.AgentWaitPauseAfterMaxMillis {
		return fcvm.SandboxWakeRequest{}, invalid("quiet_pause_after_ms is out of range")
	}
	wake.QuietPauseAfter = time.Duration(ms) * time.Millisecond
	return wake, nil
}
