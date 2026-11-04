package sched

// Sandbox sessions (ADR-20261027090013 §4). A session is admitted like an
// agent run, on the agent pool as KindAgentRun, and its VM lives as long as
// one RunSandbox call: schedd holds the reservation for the call and keeps no
// session state, so a restart leaves neither a VM nor a reservation behind.
// agentd owns the session, its idle timers and its end.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storagekey"
	"github.com/onebox-faas/faas/pkg/wire"
)

// SandboxLayerKeyForArch is where imaged publishes the platform sandbox
// layer (storagekey.SandboxLayerForArch).
func SandboxLayerKeyForArch(arch string) string {
	return storagekey.SandboxLayerForArch(arch)
}

// SandboxAdmission is one session's machine, already checked against the
// account's plan by apid and agentd.
type SandboxAdmission struct {
	SandboxID     string
	AccountID     string
	Plan          api.Plan
	RAMMB         int
	VCPU          int
	CPUMillicores int
	// Network is the session's network mode (ADR-20261027090013 §6); empty
	// is none, what an agentd from before network modes sends.
	Network string
	// WorkspaceKey is the persisted workspace the session starts from
	// (ADR-20261027090013 step 7).
	WorkspaceKey string
}

// network is the mode the session boots with.
func (a SandboxAdmission) network() string {
	if a.Network == "" {
		return api.SandboxNetworkNone
	}
	return a.Network
}

// SandboxSpec is the machine vmmd boots for one session.
type SandboxSpec struct {
	Instance        string
	SandboxID       string
	AccountID       string
	Plan            api.Plan
	BaseKey         string
	LayerKey        string
	VCPU            int
	MemSizeMiB      int
	CPUMillicores   int
	QuietPauseAfter time.Duration
	Network         string
	WorkspaceKey    string
}

// SandboxControl is a request agentd sends a live session's VM: a freeze,
// a close, or an answer to a workspace persist (ADR-20261027090013 step 7).
type SandboxControl struct {
	Op              SandboxOp
	PersistGrant    *SandboxPersistGrant
	PersistRecorded *SandboxPersistRecorded
}

// SandboxOp is what a SandboxControl asks.
type SandboxOp int

const (
	SandboxOpFreeze SandboxOp = iota + 1
	SandboxOpClose
	SandboxOpPersistGrant
	SandboxOpPersistRecorded
)

// SandboxFreeze and SandboxClose are the controls without a payload.
var (
	SandboxFreeze = SandboxControl{Op: SandboxOpFreeze}
	SandboxClose  = SandboxControl{Op: SandboxOpClose}
)

// SandboxPersistGrant is agentd's answer to a persist request: a reserved
// snapshot, or Refusal, a problem as JSON.
type SandboxPersistGrant struct {
	RequestID       string
	SnapshotID      string
	StorageKey      string
	MaxBytes        int64
	ExpiresAtUnixMs int64
	Refusal         []byte
}

// SandboxPersistDone is vmmd's report of a written archive, or Failed.
type SandboxPersistDone struct {
	RequestID  string
	SnapshotID string
	Bytes      int64
	SHA256     string
	Failed     bool
}

// SandboxPersistRecorded is agentd's record of a written archive.
type SandboxPersistRecorded struct {
	RequestID string
	Recorded  bool
}

// SandboxActivity is vmmd's report of a session's calls.
type SandboxActivity struct {
	CallsInFlight int
	LastCallEnded time.Time
}

// SandboxHooks observe a live session. Booted runs once; the others as vmmd
// reports them. A hook's error ends the session.
type SandboxHooks struct {
	Booted        func(ctx context.Context, method string, bootMs int64) error
	Activity      func(ctx context.Context, a SandboxActivity) error
	Frozen        func(ctx context.Context) error
	FreezeRefused func(ctx context.Context, reason string) error
	Thawed        func(ctx context.Context) error
	// PersistRequested and PersistDone relay a workspace persist's events.
	PersistRequested func(ctx context.Context, requestID string) error
	PersistDone      func(ctx context.Context, d SandboxPersistDone) error
}

type sandboxVMMClient interface {
	RunSandbox(context.Context, SandboxSpec, SandboxHooks, <-chan SandboxControl) error
}

// sandboxRouter is the VMMRouter surface sessions use.
type sandboxRouter interface {
	RunSandbox(ctx context.Context, nodeID string, spec SandboxSpec, hooks SandboxHooks, control <-chan SandboxControl) error
}

// SandboxInstanceID names a session's VM; the session ID is a UUID.
func SandboxInstanceID(sandboxID string) string { return "sandbox-" + sandboxID }

// SandboxesEnabled reports whether this runner boots sandbox sessions.
func (r *AgentRunner) SandboxesEnabled() bool { return r != nil && r.cfg.SandboxLayerKey != "" }

// RunSandbox admits one session, boots it on the chosen node's vmmd, and
// keeps it until close, ctx's end or a broken stream, then releases the
// reservation. placed runs once the reservation is held, before the boot;
// its error ends the session unbooted. A clean close returns nil.
func (r *AgentRunner) RunSandbox(ctx context.Context, a SandboxAdmission, placed func(AgentRunPlacement) error, hooks SandboxHooks, control <-chan SandboxControl) error {
	if r.cfg.SandboxLayerKey == "" {
		return ErrAgentRunsDisabled
	}
	if err := a.validate(); err != nil {
		return err
	}
	placement, release, err := r.admit(ctx, Request{Instance: SandboxInstanceID(a.SandboxID), Plan: a.Plan, RAMMB: a.RAMMB,
		VCPU: a.VCPU, CPUMillicores: a.CPUMillicores})
	if err != nil {
		return err
	}
	defer release()
	if placed != nil {
		if err := placed(placement); err != nil {
			return err
		}
	}
	router, ok := r.router.(sandboxRouter)
	if !ok {
		return api.NewProblem(501, api.CodeNotImplemented, "Sandbox sessions unavailable", "the vmmd router does not support sandbox sessions")
	}
	spec := SandboxSpec{Instance: placement.Instance, SandboxID: a.SandboxID, AccountID: a.AccountID, Plan: a.Plan,
		BaseKey: r.cfg.BaseKey, LayerKey: r.cfg.SandboxLayerKey, VCPU: a.VCPU, MemSizeMiB: a.RAMMB,
		CPUMillicores: a.CPUMillicores, QuietPauseAfter: api.QuietPauseAfterMillis * time.Millisecond, Network: a.network(),
		WorkspaceKey: a.WorkspaceKey}
	if err := router.RunSandbox(ctx, placement.NodeID, spec, hooks, control); err != nil {
		return fmt.Errorf("sched: sandbox %s on node %s: %w", a.SandboxID, placement.NodeID, err)
	}
	return nil
}

func (a SandboxAdmission) validate() error {
	idx := a.Plan.PlanIndex()
	switch {
	case a.SandboxID == "" || a.AccountID == "" || !a.Plan.Valid() || api.SandboxMaxLifetimeSec[idx] == 0:
		return &AgentRunInvalidError{Reason: "sandbox needs a session, an account and a plan with sessions"}
	case a.RAMMB < api.AgentRunRAMMBMin || a.RAMMB > api.AgentRunRAMMBMax[idx] || a.RAMMB%api.AgentRunRAMMBMin != 0:
		return &AgentRunInvalidError{Reason: fmt.Sprintf("sandbox RAM %d MB is outside the %s plan", a.RAMMB, a.Plan)}
	case a.VCPU < 1 || a.VCPU > api.AgentRunVCPUMax[idx] || a.CPUMillicores < 1:
		return &AgentRunInvalidError{Reason: fmt.Sprintf("sandbox vCPU %d is outside the %s plan", a.VCPU, a.Plan)}
	case a.network() != api.SandboxNetworkNone && a.network() != api.SandboxNetworkRegistries:
		return &AgentRunInvalidError{Reason: fmt.Sprintf("sandbox network mode %q is not supported", a.Network)}
	}
	return nil
}

// RunSandbox routes one session to its node's vmmd.
func (r *VMMRouter) RunSandbox(ctx context.Context, nodeID string, spec SandboxSpec, hooks SandboxHooks, control <-chan SandboxControl) error {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return err
	}
	sandboxClient, ok := cli.(sandboxVMMClient)
	if !ok {
		return api.NewProblem(501, api.CodeNotImplemented, "Sandbox sessions unavailable", "vmmd client does not support sandbox sessions")
	}
	return sandboxClient.RunSandbox(ctx, spec, hooks, control)
}

// RunSandbox boots one session VM on vmmd and relays control and reports
// until close or the stream's end; vmmd destroys the VM then.
func (c *VMMClient) RunSandbox(ctx context.Context, spec SandboxSpec, hooks SandboxHooks, control <-chan SandboxControl) error {
	if c == nil || c.cli == nil {
		return errors.New("sched: nil vmmd sandbox client")
	}
	fields, _ := wire.FromContext(ctx)
	streamCtx, stop := context.WithCancel(wire.WithCorrelationOutgoing(ctx, fields))
	defer stop()
	stream, err := c.cli.RunSandbox(streamCtx)
	if err != nil {
		return liftErr(err)
	}
	if err := stream.Send(&vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_Start{Start: &vmmdpb.RunSandboxStart{
		Instance: spec.Instance, SandboxId: spec.SandboxID, AccountId: spec.AccountID, Plan: string(spec.Plan),
		BaseKey: spec.BaseKey, LayerKey: spec.LayerKey, VcpuCount: int32(spec.VCPU), MemSizeMib: int32(spec.MemSizeMiB),
		CpuMillicores: int32(spec.CPUMillicores), QuietPauseAfterMs: spec.QuietPauseAfter.Milliseconds(), NetworkMode: spec.Network,
		WorkspaceKey: spec.WorkspaceKey,
	}}}); err != nil {
		return liftErr(err)
	}
	closed := make(chan struct{})
	go func() {
		for {
			select {
			case <-streamCtx.Done():
				return
			case req, ok := <-control:
				if !ok {
					return
				}
				msg := sandboxControlFrame(req)
				if msg == nil {
					continue
				}
				if stream.Send(msg) != nil {
					return
				}
				if req == SandboxClose {
					close(closed)
					return
				}
			}
		}
	}()
	err = receiveSandbox(streamCtx, stream, hooks)
	select {
	case <-closed:
		if errors.Is(err, io.EOF) {
			return nil
		}
	default:
	}
	if errors.Is(err, io.EOF) {
		return errors.New("sched: sandbox stream ended before close")
	}
	return err
}

func receiveSandbox(ctx context.Context, stream vmmdpb.Vmmd_RunSandboxClient, hooks SandboxHooks) error {
	call := func(hook func(context.Context) error) error {
		if hook == nil {
			return nil
		}
		return hook(ctx)
	}
	for {
		event, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return io.EOF
		}
		if err != nil {
			return liftErr(err)
		}
		switch {
		case event.GetBooted() != nil:
			if hooks.Booted != nil {
				err = hooks.Booted(ctx, event.GetBooted().GetMethod().String(), event.GetBooted().GetBootMs())
			}
		case event.GetActivity() != nil:
			if hooks.Activity != nil {
				a := SandboxActivity{CallsInFlight: int(event.GetActivity().GetCallsInFlight())}
				if ms := event.GetActivity().GetLastCallEndedUnixMs(); ms > 0 {
					a.LastCallEnded = time.UnixMilli(ms)
				}
				err = hooks.Activity(ctx, a)
			}
		case event.GetFrozen() != nil:
			err = call(hooks.Frozen)
		case event.GetFreezeRefused() != nil:
			if hooks.FreezeRefused != nil {
				err = hooks.FreezeRefused(ctx, event.GetFreezeRefused().GetReason())
			}
		case event.GetThawed() != nil:
			err = call(hooks.Thawed)
		case event.GetPersistRequested() != nil:
			if hooks.PersistRequested != nil {
				err = hooks.PersistRequested(ctx, event.GetPersistRequested().GetRequestId())
			}
		case event.GetPersistDone() != nil:
			if hooks.PersistDone != nil {
				d := event.GetPersistDone()
				err = hooks.PersistDone(ctx, SandboxPersistDone{RequestID: d.GetRequestId(), SnapshotID: d.GetSnapshotId(),
					Bytes: d.GetBytes(), SHA256: d.GetSha256(), Failed: d.GetFailed()})
			}
		default:
			return errors.New("sched: sandbox stream returned an empty event")
		}
		if err != nil {
			return err
		}
	}
}

// sandboxControlFrame is the vmmd frame for one control, or nil for one
// without a payload it needs.
func sandboxControlFrame(req SandboxControl) *vmmdpb.RunSandboxRequest {
	switch req.Op {
	case SandboxOpFreeze:
		return &vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_Freeze{Freeze: &vmmdpb.RunSandboxFreeze{}}}
	case SandboxOpClose:
		return &vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_Close{Close: &vmmdpb.RunSandboxClose{}}}
	case SandboxOpPersistGrant:
		if g := req.PersistGrant; g != nil {
			return &vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_PersistGrant{PersistGrant: &vmmdpb.RunSandboxPersistGrant{
				RequestId: g.RequestID, SnapshotId: g.SnapshotID, StorageKey: g.StorageKey, MaxBytes: g.MaxBytes,
				ExpiresAtUnixMs: g.ExpiresAtUnixMs, Refusal: g.Refusal}}}
		}
	case SandboxOpPersistRecorded:
		if r := req.PersistRecorded; r != nil {
			return &vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_PersistRecorded{PersistRecorded: &vmmdpb.RunSandboxPersistRecorded{
				RequestId: r.RequestID, Recorded: r.Recorded}}}
		}
	}
	return nil
}
