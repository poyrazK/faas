package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/agentrunproto"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
)

// agentRunMaxRecvBytes bounds one vmmd event: the result frame carries the
// inline result and output files (ADR-20261027090010 §3), base64 in JSON.
const agentRunMaxRecvBytes = (api.AgentRunOutputFilesMaxBytes+agentrunproto.MaxResultBytes)*4/3 + 1<<20

// AgentRunSpec is the machine for one agent run (ADR-20261027090010 §6).
type AgentRunSpec struct {
	Instance      string
	AccountID     string
	Plan          api.Plan
	BaseKey       string
	LayerKey      string
	VCPU          int
	MemSizeMiB    int
	CPUMillicores int
	Relay         *AgentRunRelay
	InputKey      string
	// Snapshot is the preset snapshot to restore; vmmd cold-boots when it
	// is nil, stale or fails (ADR-005).
	Snapshot *AgentRunSnapshot
}

// AgentRunSnapshot locates a preset snapshot vmmd captured (ADR-20261027090010 §2).
type AgentRunSnapshot struct {
	StorageKey        string
	VMStateStorageKey string
	FCVersion         string
	MemBytes          int64
}

// AgentRunHooks observe a run while it streams. Booted runs once, after the
// VM is up and before the run reaches the guest; Output runs per chunk.
type AgentRunHooks struct {
	Booted func(ctx context.Context, method string, bootMs int64) error
	Output agentrunproto.OutputReceiver
}

type agentRunVMMClient interface {
	RunAgent(context.Context, AgentRunSpec, agentrunproto.Request, AgentRunHooks, <-chan struct{}) (agentrunproto.Result, error)
	CaptureAgentSnapshot(ctx context.Context, spec AgentRunSpec, namespace, captureID string) (*AgentRunSnapshot, error)
}

// CaptureAgentSnapshot asks vmmd to capture a preset snapshot of spec's
// machine. spec carries no relay, input or snapshot.
func (c *VMMClient) CaptureAgentSnapshot(ctx context.Context, spec AgentRunSpec, namespace, captureID string) (*AgentRunSnapshot, error) {
	if c == nil || c.cli == nil {
		return nil, errors.New("sched: nil vmmd agent run client")
	}
	fields, _ := wire.FromContext(ctx)
	resp, err := c.cli.CaptureAgentSnapshot(wire.WithCorrelationOutgoing(ctx, fields), &vmmdpb.CaptureAgentSnapshotRequest{
		Instance: spec.Instance, AccountId: spec.AccountID, Plan: string(spec.Plan), BaseKey: spec.BaseKey, LayerKey: spec.LayerKey,
		VcpuCount: int32(spec.VCPU), MemSizeMib: int32(spec.MemSizeMiB), CpuMillicores: int32(spec.CPUMillicores),
		Namespace: namespace, CaptureId: captureID,
	})
	if err != nil {
		return nil, liftErr(err)
	}
	snap := resp.GetSnapshot()
	if snap.GetStorageKey() == "" || snap.GetVmstateStorageKey() == "" || snap.GetFcVersion() == "" || snap.GetMemBytes() < 1 {
		return nil, errors.New("sched: vmmd returned an incomplete agent run snapshot")
	}
	return &AgentRunSnapshot{StorageKey: snap.GetStorageKey(), VMStateStorageKey: snap.GetVmstateStorageKey(),
		FCVersion: snap.GetFcVersion(), MemBytes: snap.GetMemBytes()}, nil
}

// RunAgent boots one agent run VM on vmmd and streams the run. Closing cancel
// sends vmmd a cancel frame; the result still follows. vmmd destroys the VM
// before it answers and when the stream breaks.
func (c *VMMClient) RunAgent(ctx context.Context, spec AgentRunSpec, run agentrunproto.Request, hooks AgentRunHooks, cancel <-chan struct{}) (agentrunproto.Result, error) {
	if c == nil || c.cli == nil {
		return agentrunproto.Result{}, errors.New("sched: nil vmmd agent run client")
	}
	if err := run.Validate(); err != nil {
		return agentrunproto.Result{}, err
	}
	body, err := json.Marshal(run)
	if err != nil {
		return agentrunproto.Result{}, fmt.Errorf("sched: encode agent run: %w", err)
	}
	fields, _ := wire.FromContext(ctx)
	ctx = wire.WithCorrelationOutgoing(ctx, fields)
	streamCtx, stop := context.WithCancel(ctx)
	defer stop()
	stream, err := c.cli.RunAgent(streamCtx, grpc.MaxCallRecvMsgSize(agentRunMaxRecvBytes))
	if err != nil {
		return agentrunproto.Result{}, liftErr(err)
	}
	if err := stream.Send(&vmmdpb.RunAgentRequest{Frame: &vmmdpb.RunAgentRequest_Start{Start: &vmmdpb.RunAgentStart{
		Instance: spec.Instance, AccountId: spec.AccountID, Plan: string(spec.Plan), BaseKey: spec.BaseKey, LayerKey: spec.LayerKey,
		VcpuCount: int32(spec.VCPU), MemSizeMib: int32(spec.MemSizeMiB), CpuMillicores: int32(spec.CPUMillicores), Run: body,
		Relay: relayToProto(spec.Relay), InputKey: spec.InputKey, Snapshot: snapshotToProto(spec.Snapshot),
	}}}); err != nil {
		return agentrunproto.Result{}, liftErr(err)
	}
	if cancel != nil {
		go func() {
			select {
			case <-cancel:
				_ = stream.Send(&vmmdpb.RunAgentRequest{Frame: &vmmdpb.RunAgentRequest_Cancel{Cancel: &vmmdpb.RunAgentCancel{}}})
			case <-streamCtx.Done():
			}
		}()
	}
	return receiveAgentRun(streamCtx, stream, run, hooks)
}

func receiveAgentRun(ctx context.Context, stream vmmdpb.Vmmd_RunAgentClient, run agentrunproto.Request, hooks AgentRunHooks) (agentrunproto.Result, error) {
	var stdout, stderr []byte
	for {
		event, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return agentrunproto.Result{}, errors.New("sched: agent run stream ended before its result")
		}
		if err != nil {
			return agentrunproto.Result{}, liftErr(err)
		}
		switch {
		case event.GetBooted() != nil:
			if hooks.Booted != nil {
				if err := hooks.Booted(ctx, event.GetBooted().GetMethod().String(), event.GetBooted().GetBootMs()); err != nil {
					return agentrunproto.Result{}, err
				}
			}
		case event.GetOutput() != nil:
			name, chunk := event.GetOutput().GetStream(), event.GetOutput().GetChunk()
			if name != "stdout" && name != "stderr" {
				return agentrunproto.Result{}, errors.New("sched: agent run stream returned an invalid output stream")
			}
			if len(stdout)+len(stderr)+len(chunk) > run.MaxLogBytes {
				return agentrunproto.Result{}, agentrunproto.ErrOutputLimitExceeded
			}
			if name == "stdout" {
				stdout = append(stdout, chunk...)
			} else {
				stderr = append(stderr, chunk...)
			}
			if hooks.Output != nil {
				if err := hooks.Output(ctx, name, chunk); err != nil {
					return agentrunproto.Result{}, err
				}
			}
		case event.GetResult() != nil:
			var result agentrunproto.Result
			if err := json.Unmarshal(event.GetResult().GetResult(), &result); err != nil {
				return agentrunproto.Result{}, fmt.Errorf("sched: decode agent run result: %w", err)
			}
			if err := result.Validate(run); err != nil {
				return agentrunproto.Result{}, err
			}
			result.Stdout, result.Stderr = stdout, stderr
			return result, nil
		default:
			return agentrunproto.Result{}, errors.New("sched: agent run stream returned an empty event")
		}
	}
}

// RunAgent routes one agent run to its node's vmmd.
func (r *VMMRouter) RunAgent(ctx context.Context, nodeID string, spec AgentRunSpec, run agentrunproto.Request, hooks AgentRunHooks, cancel <-chan struct{}) (agentrunproto.Result, error) {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return agentrunproto.Result{}, err
	}
	agentClient, ok := cli.(agentRunVMMClient)
	if !ok {
		return agentrunproto.Result{}, api.NewProblem(501, api.CodeNotImplemented, "Agent runs unavailable", "vmmd client does not support agent runs")
	}
	return agentClient.RunAgent(ctx, spec, run, hooks, cancel)
}

// CaptureAgentSnapshot captures a preset snapshot on nodeID's vmmd.
func (r *VMMRouter) CaptureAgentSnapshot(ctx context.Context, nodeID string, spec AgentRunSpec, namespace, captureID string) (*AgentRunSnapshot, error) {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	agentClient, ok := cli.(agentRunVMMClient)
	if !ok {
		return nil, api.NewProblem(501, api.CodeNotImplemented, "Agent runs unavailable", "vmmd client does not support agent runs")
	}
	return agentClient.CaptureAgentSnapshot(ctx, spec, namespace, captureID)
}

func snapshotToProto(s *AgentRunSnapshot) *vmmdpb.AgentRunSnapshot {
	if s == nil {
		return nil
	}
	return &vmmdpb.AgentRunSnapshot{StorageKey: s.StorageKey, VmstateStorageKey: s.VMStateStorageKey, FcVersion: s.FCVersion, MemBytes: s.MemBytes}
}

func relayToProto(r *AgentRunRelay) *vmmdpb.RunAgentRelay {
	if r == nil {
		return nil
	}
	return &vmmdpb.RunAgentRelay{RunId: r.RunID, LeaseToken: r.LeaseToken, TenantId: r.TenantID, AgentId: r.AgentID,
		AgentVersion: int32(r.AgentVersion)}
}
