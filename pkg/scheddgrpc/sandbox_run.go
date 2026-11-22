package scheddgrpc

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SandboxRunner is the sched.AgentRunner surface RunSandbox uses.
type SandboxRunner interface {
	RunSandbox(ctx context.Context, a sched.SandboxAdmission, placed func(sched.AgentRunPlacement) error, hooks sched.SandboxHooks, control <-chan sched.SandboxControl) error
	// DeleteSandboxVolume removes a session's volume from one node
	// (ADR-20261122132055 §4).
	DeleteSandboxVolume(ctx context.Context, nodeID, accountID, sandboxID string) (bool, error)
}

// WithSandboxRunner serves RunSandbox (ADR-20261027090013 §4). Without it,
// or with a nil runner, RunSandbox answers Unimplemented.
func (s *Server) WithSandboxRunner(runner SandboxRunner) *Server {
	s.sandboxRuns = runner
	return s
}

// RunSandbox admits one sandbox session and keeps its VM for the stream's
// life. A placed session boots only after agentd's proceed frame; an error
// before proceed means nothing booted, so agentd may queue it again.
func (s *Server) RunSandbox(stream scheddpb.Schedd_RunSandboxServer) error {
	const op = "RunSandbox"
	started := time.Now()
	var opErr error
	defer func() { s.ops.Observe(op, time.Since(started), opErr) }()
	if s.sandboxRuns == nil {
		opErr = status.Error(codes.Unimplemented, "schedd sandbox sessions are not enabled")
		return opErr
	}
	first, err := stream.Recv()
	if err != nil {
		opErr = err
		return err
	}
	start := first.GetStart()
	if start == nil {
		opErr = status.Error(codes.InvalidArgument, "the first frame must be start")
		return opErr
	}
	admission := sched.SandboxAdmission{SandboxID: start.GetSandboxId(), AccountID: start.GetAccountId(), Plan: api.Plan(start.GetPlan()),
		RAMMB: int(start.GetRamMb()), VCPU: int(start.GetVcpu()), CPUMillicores: int(start.GetCpuMillicores()),
		Network: start.GetNetworkMode(), WorkspaceKey: start.GetWorkspaceKey(), ImageKey: start.GetImageKey(),
		StartCmd: start.GetStartCmd(), ReadyCmd: start.GetReadyCmd(), ReadyTimeout: time.Duration(start.GetReadyTimeoutMs()) * time.Millisecond,
		Browser: start.GetBrowser(), Task: sandboxTaskFromProto(start.GetTask()),
		EgressAllow: start.GetEgressAllow(), EgressDeny: start.GetEgressDeny(), WebDomainLog: start.GetWebDomainLog(),
		Persistent: start.GetPersistent(),
		Volume: sched.SandboxVolume{GB: int(start.GetVolumeGb()), BackupID: start.GetVolumeBackupId(),
			BackupEvery: time.Duration(start.GetVolumeBackupIntervalMs()) * time.Millisecond,
			NodeID:      start.GetVolumeNodeId(), StoredBackupID: start.GetVolumeStoredBackupId(),
			SourceSandboxID: start.GetVolumeSourceSandboxId()}}
	if r := start.GetResume(); r != nil {
		admission.Resume = &sched.SandboxResume{NodeID: r.GetNodeId(), RequireNode: r.GetRequireNode(), Fork: r.GetFork(),
			ForkedFrom: r.GetForkedFrom(), Snapshot: sched.AgentRunSnapshot{StorageKey: r.GetStorageKey(),
				VMStateStorageKey: r.GetVmstateStorageKey(), FCVersion: r.GetFcVersion(), MemBytes: r.GetMemBytes()}}
	}
	ctx := stream.Context()
	var sendMu sync.Mutex
	send := func(ev *scheddpb.SandboxRunEvent) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(ev)
	}
	frames := watchSandboxFrames(ctx, stream)
	placed := func(p sched.AgentRunPlacement) error {
		if err := send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_Placed{Placed: &scheddpb.AgentRunPlaced{Instance: p.Instance, NodeId: p.NodeID}}}); err != nil {
			return err
		}
		// Nothing boots until agentd has recorded where the session runs.
		select {
		case <-frames.proceed:
			return nil
		case <-frames.ended:
			return errAgentRunNotProceeded
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	hooks := sched.SandboxHooks{
		Booted: func(_ context.Context, b sched.SandboxBooted) error {
			return send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_Booted{Booted: &scheddpb.AgentRunBooted{Method: b.Method,
				BootMs: b.BootMs, Actions: b.Actions, EgressPolicy: b.EgressPolicy}}})
		},
		Activity: func(_ context.Context, a sched.SandboxActivity) error {
			ev := &scheddpb.SandboxRunActivity{CallsInFlight: int32(a.CallsInFlight)}
			if !a.LastCallEnded.IsZero() {
				ev.LastCallEndedUnixMs = a.LastCallEnded.UnixMilli()
			}
			return send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_Activity{Activity: ev}})
		},
		Frozen: func(context.Context) error {
			return send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_Frozen{Frozen: &scheddpb.SandboxRunFrozen{}}})
		},
		FreezeRefused: func(_ context.Context, reason string) error {
			return send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_FreezeRefused{FreezeRefused: &scheddpb.SandboxRunFreezeRefused{Reason: reason}}})
		},
		Thawed: func(context.Context) error {
			return send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_Thawed{Thawed: &scheddpb.SandboxRunThawed{}}})
		},
		PersistRequested: func(_ context.Context, requestID string) error {
			return send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_PersistRequested{
				PersistRequested: &scheddpb.SandboxRunPersistRequested{RequestId: requestID}}})
		},
		PersistDone: func(_ context.Context, d sched.SandboxPersistDone) error {
			return send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_PersistDone{PersistDone: &scheddpb.SandboxRunPersistDone{
				RequestId: d.RequestID, SnapshotId: d.SnapshotID, Bytes: d.Bytes, Sha256: d.SHA256, Failed: d.Failed}}})
		},
		Usage: func(_ context.Context, u sched.AgentRunUsage) error {
			return send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_Usage{Usage: agentRunUsageToProto(u)}})
		},
		ActionDone: func(_ context.Context, d sched.SandboxActionDone) error {
			return send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_ActionDone{ActionDone: &scheddpb.SandboxRunActionDone{
				ActionId: d.ActionID, ExitCode: int32(d.ExitCode), Output: d.Output, TimedOut: d.TimedOut, Truncated: d.Truncated,
				Error: d.Error, ScreenshotBytes: d.ScreenshotBytes}}})
		},
		TaskDone: func(_ context.Context, d sched.SandboxTaskDone) error {
			return send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_TaskDone{TaskDone: &scheddpb.SandboxRunTaskDone{
				ExitCode: int32(d.ExitCode), Output: d.Output, TimedOut: d.TimedOut, Truncated: d.Truncated, Error: d.Error}}})
		},
		Paused: func(_ context.Context, p sched.SandboxPaused) error {
			return send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_Paused{Paused: &scheddpb.SandboxRunPaused{
				StorageKey: p.Snapshot.StorageKey, VmstateStorageKey: p.Snapshot.VMStateStorageKey, FcVersion: p.Snapshot.FCVersion,
				MemBytes: p.Snapshot.MemBytes, VmstateBytes: p.VMStateBytes, PauseMs: p.PauseMs, HasBacking: p.HasBacking,
				VolumeBackupId: p.VolumeBackupID}}})
		},
		VolumeBackup: func(_ context.Context, b sched.SandboxVolumeBackup) error {
			return send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_VolumeBackup{VolumeBackup: &scheddpb.SandboxRunVolumeBackup{
				BackupId: b.ID, SizeBytes: b.SizeBytes, DataChunks: int32(b.DataChunks), UploadedBytes: b.UploadedBytes, //nolint:gosec // G115: a volume's chunk count.
				BackupMs: b.BackupMs, Incremental: b.Incremental}}})
		},
		PauseRefused: func(_ context.Context, reason string) error {
			return send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_PauseRefused{PauseRefused: &scheddpb.SandboxRunPauseRefused{Reason: reason}}})
		},
		Forked: func(_ context.Context, f sched.SandboxForked) error {
			return send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_Forked{Forked: &scheddpb.SandboxRunForked{
				StorageKey: f.Snapshot.StorageKey, VmstateStorageKey: f.Snapshot.VMStateStorageKey, FcVersion: f.Snapshot.FCVersion,
				MemBytes: f.Snapshot.MemBytes, VmstateBytes: f.VMStateBytes, ForkMs: f.ForkMs, HasBacking: f.HasBacking,
				VolumeBackupId: f.VolumeBackupID}}})
		},
		ForkRefused: func(_ context.Context, reason string) error {
			return send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_ForkRefused{ForkRefused: &scheddpb.SandboxRunForkRefused{Reason: reason}}})
		},
		Evacuate: func(_ context.Context, reason string) error {
			return send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_Evacuate{Evacuate: &scheddpb.SandboxRunEvacuate{Reason: reason}}})
		},
		WebDomains: func(_ context.Context, d []sched.SandboxWebDomain) error {
			frame := &scheddpb.SandboxRunWebDomains{Domains: make([]*scheddpb.SandboxRunWebDomain, 0, len(d))}
			for _, x := range d {
				frame.Domains = append(frame.Domains, &scheddpb.SandboxRunWebDomain{Domain: x.Domain, Refused: x.Refused, AtUnixMs: x.At.UnixMilli()})
			}
			return send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_WebDomains{WebDomains: frame}})
		},
	}
	opErr = s.sandboxRuns.RunSandbox(ctx, admission, placed, hooks, frames.control)
	if opErr != nil {
		return agentRunStatus(opErr)
	}
	return nil
}

type sandboxFrames struct {
	proceed <-chan struct{}
	// ended closes when agentd's side of the stream ends before close.
	ended   <-chan struct{}
	control <-chan sched.SandboxControl
}

// watchSandboxFrames reads agentd's frames after start: proceed lets the
// placed session boot; freeze and close go to the runner. A stream that
// ends, cleanly or not, closes the session.
func watchSandboxFrames(ctx context.Context, stream scheddpb.Schedd_RunSandboxServer) sandboxFrames {
	proceed, ended := make(chan struct{}), make(chan struct{})
	control := make(chan sched.SandboxControl, 4)
	var proceedOnce sync.Once
	go func() {
		defer close(ended)
		for {
			frame, err := stream.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					select {
					case control <- sched.SandboxClose:
					case <-ctx.Done():
					}
				}
				return
			}
			var req sched.SandboxControl
			switch {
			case frame.GetProceed() != nil:
				proceedOnce.Do(func() { close(proceed) })
				continue
			case frame.GetFreeze() != nil:
				req = sched.SandboxFreeze
			case frame.GetClose() != nil:
				req = sched.SandboxClose
			case frame.GetPersistGrant() != nil:
				g := frame.GetPersistGrant()
				req = sched.SandboxControl{Op: sched.SandboxOpPersistGrant, PersistGrant: &sched.SandboxPersistGrant{
					RequestID: g.GetRequestId(), SnapshotID: g.GetSnapshotId(), StorageKey: g.GetStorageKey(), MaxBytes: g.GetMaxBytes(),
					ExpiresAtUnixMs: g.GetExpiresAtUnixMs(), Refusal: g.GetRefusal()}}
			case frame.GetPersistRecorded() != nil:
				r := frame.GetPersistRecorded()
				req = sched.SandboxControl{Op: sched.SandboxOpPersistRecorded, PersistRecorded: &sched.SandboxPersistRecorded{
					RequestID: r.GetRequestId(), Recorded: r.GetRecorded()}}
			case frame.GetAction() != nil:
				req = sched.SandboxControl{Op: sched.SandboxOpAction, Action: sandboxActionFromProto(frame.GetAction())}
			case frame.GetPause() != nil:
				req = sched.SandboxControl{Op: sched.SandboxOpPause, CaptureID: frame.GetPause().GetCaptureId()}
			case frame.GetFork() != nil:
				req = sched.SandboxControl{Op: sched.SandboxOpFork, CaptureID: frame.GetFork().GetCaptureId()}
			default:
				continue
			}
			select {
			case control <- req:
			case <-ctx.Done():
				return
			}
		}
	}()
	return sandboxFrames{proceed: proceed, ended: ended, control: control}
}

// sandboxTaskFromProto is agentd's task as schedd passes it to vmmd
// (ADR-20261116182615 §3); vmmd validates it.
func sandboxTaskFromProto(t *scheddpb.SandboxRunTask) *sched.SandboxTask {
	if t == nil {
		return nil
	}
	return &sched.SandboxTask{Kind: t.GetKind(), URL: t.GetUrl(), MaxChars: int(t.GetMaxChars()),
		Timeout: time.Duration(t.GetTimeoutMs()) * time.Millisecond, MaxOutputBytes: t.GetMaxOutputBytes()}
}

// sandboxActionFromProto is agentd's browser action as schedd relays it to
// vmmd (ADR-20261117065818 §2); vmmd validates it.
func sandboxActionFromProto(a *scheddpb.SandboxRunAction) *sched.SandboxAction {
	return &sched.SandboxAction{ID: a.GetActionId(), Kind: a.GetKind(), URL: a.GetUrl(), Ref: int(a.GetRef()), Text: a.GetText(),
		Submit: a.GetSubmit(), Key: a.GetKey(), Direction: a.GetDirection(), MaxChars: int(a.GetMaxChars()),
		Timeout: time.Duration(a.GetTimeoutMs()) * time.Millisecond, MaxOutputBytes: a.GetMaxOutputBytes(), Screenshot: a.GetScreenshot()}
}

// DeleteSandboxVolume removes a persistent session's workspace volume from
// one node for agentd (ADR-20261122132055 §4).
func (s *Server) DeleteSandboxVolume(ctx context.Context, req *scheddpb.DeleteSandboxVolumeRequest) (*scheddpb.DeleteSandboxVolumeResponse, error) {
	if s.sandboxRuns == nil {
		return nil, status.Error(codes.Unimplemented, "schedd sandbox sessions are not enabled")
	}
	if req.GetNodeId() == "" || uuid.Validate(req.GetAccountId()) != nil || uuid.Validate(req.GetSandboxId()) != nil {
		return nil, status.Error(codes.InvalidArgument, "node_id, and account_id and sandbox_id as UUIDs, are required")
	}
	deleted, err := s.sandboxRuns.DeleteSandboxVolume(ctx, req.GetNodeId(), req.GetAccountId(), req.GetSandboxId())
	if err != nil {
		return nil, agentRunStatus(err)
	}
	return &scheddpb.DeleteSandboxVolumeResponse{Deleted: deleted}, nil
}
