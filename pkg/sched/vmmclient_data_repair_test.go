package sched_test

// adr: 20261016
// Scheduler repair RPCs preserve the scoped plan and cleanup contract.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type repairRPCVMM struct {
	*fakeVMM
	frame          core.RehearsalFrame
	seen           core.RehearsalFrame
	runs           int
	missingCleanup bool
}

func (v *repairRPCVMM) DataRepairRehearsalIdentity() (string, string, string, error) {
	return v.frame.NodeID, v.frame.Incarnation, v.frame.Runner.Digest(), nil
}
func (v *repairRPCVMM) RunDataRepairRehearsal(_ context.Context, f core.RehearsalFrame, in core.RehearsalInput) (core.Evidence, error) {
	v.seen = f
	v.runs++
	at := time.Now().UTC()
	e := core.Evidence{Passed: true, PlanDigest: f.PlanDigest, SnapshotSHA256: in.SnapshotSHA256, ChecksSHA256: f.ChecksSHA256, Runner: core.MicroVMRunner, TaskID: f.TaskID, NodeID: f.NodeID, RunnerSHA256: f.Runner.Digest(), RetiredAt: &at}
	if v.missingCleanup {
		e.RetiredAt = nil
	}
	return e, nil
}
func (v *repairRPCVMM) RetireDataRepairRehearsal(_ context.Context, f core.RehearsalFrame) (time.Time, error) {
	v.seen = f
	return time.Now().UTC(), nil
}

func repairRPCInput() (core.RehearsalFrame, core.RehearsalInput) {
	dump := bytes.Repeat([]byte("snapshot"), 1<<20)
	h := sha256.Sum256(dump)
	check := sha256.Sum256([]byte("SELECT true"))
	p := core.Plan{ID: uuid.NewString(), Digest: strings.Repeat("a", 64), ChecksSHA256: hex.EncodeToString(check[:]), Candidates: 1, ExpiresAt: time.Now().UTC().Add(time.Minute), Scope: core.Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production"}}
	in := core.RehearsalInput{Plan: p, CheckSQL: "SELECT true", Database: "postgres", Dump: dump, SnapshotSHA256: hex.EncodeToString(h[:])}
	spec := core.RunnerSpec{BaseKey: "repair/base", LayerKey: "repair/layer", KernelSHA256: strings.Repeat("1", 64), BaseSHA256: strings.Repeat("2", 64), LayerSHA256: strings.Repeat("3", 64)}
	f := core.RehearsalFrame{TaskID: uuid.NewString(), OperationID: p.ID, Scope: p.Scope, Binding: strings.Repeat("b", 64), NodeID: uuid.NewString(), Incarnation: uuid.NewString(), PlanID: p.ID, PlanDigest: p.Digest, ChecksSHA256: p.ChecksSHA256, SnapshotSHA256: in.SnapshotSHA256, PlanExpiresAt: p.ExpiresAt, Deadline: p.ExpiresAt, Plan: api.PlanPro, Runner: spec}
	return f, in
}

func TestDataRepairRehearsalRPCStreamingAndCleanupProof(t *testing.T) {
	f, in := repairRPCInput()
	v := &repairRPCVMM{fakeVMM: &fakeVMM{}, frame: f}
	server := vmmdgrpc.New(v, wire.NewOpsMetrics("repair_rpc_test"), "test", nil).WithQualificationNodeID(f.NodeID)
	conn := qualificationRPCConnection(t, server)
	cli := sched.NewVMMClient(conn)
	inc, err := cli.DataRepairRehearsalIdentity(t.Context(), f.NodeID, f.Runner)
	if err != nil || inc != f.Incarnation {
		t.Fatal("runner identity", err)
	}
	e, err := cli.RunDataRepairRehearsal(t.Context(), f, in)
	if err != nil || !f.ValidEvidence(e) || !v.seen.Same(f) {
		t.Fatal("streamed snapshot/frame", err)
	}
	if err = cli.RetireDataRepairRehearsal(t.Context(), f); err != nil || !v.seen.Same(f) {
		t.Fatal("retirement", err)
	}
	v.missingCleanup = true
	if e, err = cli.RunDataRepairRehearsal(t.Context(), f, in); err == nil || e.Passed {
		t.Fatal("guest pass without host cleanup was accepted")
	}
}

func TestDataRepairRehearsalRPCRejectsInvalidEnvelope(t *testing.T) {
	f, _ := repairRPCInput()
	v := &repairRPCVMM{fakeVMM: &fakeVMM{}, frame: f}
	conn := qualificationRPCConnection(t, vmmdgrpc.New(v, wire.NewOpsMetrics("repair_rpc_invalid"), "test", nil).WithQualificationNodeID(f.NodeID))
	cli := vmmdpb.NewVmmdClient(conn)
	for _, mode := range []string{"identity", "node", "chunk", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			frame := f
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if mode == "node" {
				frame.NodeID = uuid.NewString()
			}
			if mode != "identity" {
				ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("x-faas-node-id", frame.NodeID, "x-faas-instance-id", frame.TaskID, "x-faas-app-id", frame.Scope.AppID, "x-faas-wake-id", frame.TaskID))
			}
			b, _ := json.Marshal(frame)
			if mode == "unknown" {
				b = append(b[:len(b)-1], []byte(`,"unreviewed":true}`)...)
			}
			stream, err := cli.RunDataRepairRehearsal(ctx)
			if err != nil {
				t.Fatal(err)
			}
			_ = stream.Send(&vmmdpb.DataRepairRehearsalChunk{FrameJson: b})
			if mode == "chunk" {
				_ = stream.Send(&vmmdpb.DataRepairRehearsalChunk{Chunk: make([]byte, api.DataRepairRehearsalChunkBytes+1)})
			}
			_, err = stream.CloseAndRecv()
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("invalid envelope %s: %v", mode, err)
			}
		})
	}
	if v.runs != 0 {
		t.Fatal("invalid envelope reached native runtime")
	}
}

func TestRepairRehearsalAdmissionChargesNodeCapacity(t *testing.T) {
	ledger := sched.NewNodeLedger()
	r := sched.Request{Instance: "repair", AppID: "app", Plan: api.PlanPro, RAMMB: api.DataRepairSandboxMemoryMB, VCPU: api.DataRepairSandboxVCPUs, CPUMillicores: api.DataRepairSandboxCPUMillicores, Kind: sched.KindDataRepairRehearsal, NodeID: "node", NodeCeilingMB: api.DataRepairSandboxMemoryMB + api.PerVMOverheadMB, VCPUBudget: 10, CPUBudgetMillicores: 10000}
	if err := ledger.Admit(r); err != nil {
		t.Fatal(err)
	}
	if ledger.ResidentRAM() != r.NodeCeilingMB {
		t.Fatal("repair reservation omitted memory overhead")
	}
	r.Instance = "other"
	if err := ledger.Admit(r); err == nil {
		t.Fatal("repair ignored node capacity")
	}
	ledger.Release("repair")
	if ledger.ResidentRAM() != 0 {
		t.Fatal("repair capacity was not released")
	}
}
