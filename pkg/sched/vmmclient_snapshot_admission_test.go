// adr: 592
package sched_test

import (
	"context"
	"strings"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type admittedSnapshotWireServer struct {
	vmmdpb.UnimplementedVmmdServer
	requests int
	response *vmmdpb.CaptureAdmittedRuntimeResponse
	grant    runtimeadmission.SnapshotGrant
}

func (s *admittedSnapshotWireServer) CaptureAdmittedRuntime(_ context.Context, req *vmmdpb.CaptureAdmittedRuntimeRequest) (*vmmdpb.CaptureAdmittedRuntimeResponse, error) {
	s.requests++
	g, err := runtimeadmission.SnapshotGrantFromProto(req.Grant)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if !g.Equal(s.grant) {
		return nil, status.Error(codes.FailedPrecondition, "different grant")
	}
	return s.response, nil
}

func wireSnapshotGrantFixture(t *testing.T) (runtimeadmission.SnapshotGrant, *vmmdpb.CaptureAdmittedRuntimeResponse) {
	t.Helper()
	i := wireSnapshotFixture(t)
	c := i.Capture.Clone()
	parts := strings.Split(c.Memory.StorageKey, "/")
	now := time.Now()
	g := runtimeadmission.SnapshotGrant{Version: runtimeadmission.SnapshotGrantVersion, Token: parts[len(parts)-3], Parent: c.Parent.Clone(), MemoryKey: c.Memory.StorageKey, VMStateKey: c.VMState.StorageKey, PrivateDriveKey: c.PrivateDrive.StorageKey, FCVersion: "1.10.0", Mode: "warm", SourceStartedAtUnixNano: c.Parent.CompletedAtUnixNano, IssuedAtUnixNano: now.UnixNano(), ExpiresAtUnixNano: now.Add(time.Minute).UnixNano()}
	c.CapturedAtUnixNano = now.UnixNano()
	a := runtimeadmission.SnapshotAcknowledgment{Grant: g.Clone(), Capture: c.Clone(), CompletedAtUnixNano: now.UnixNano()}
	return g, &vmmdpb.CaptureAdmittedRuntimeResponse{Snapshot: &vmmdpb.SnapshotResponse{MemBytes: i.MemBytes, VmstateBytes: i.VMStateBytes, StoredBytes: i.StoredBytes, Capture: c.ToProto()}, Acknowledgment: a.ToProto()}
}

func TestVMMClientAdmittedSnapshotWireRequiresCompleteMatchingAcknowledgment(t *testing.T) {
	g, p := wireSnapshotGrantFixture(t)
	s := &admittedSnapshotWireServer{response: p, grant: g.Clone()}
	cli := newPolicyWireClient(t, s)
	i, a, err := cli.CaptureAdmittedRuntime(t.Context(), g)
	if err != nil || s.requests != 1 || !i.Capture.Equal(a.Capture) || i.CaptureToken != g.Token || a.Check(g, time.Now()) != nil {
		t.Fatal("admitted capture lost exact wire proof", err)
	}
	p.Acknowledgment.Capture.Parent.ArtifactConsumption.Drives[0].DriveId = "caller-edited"
	if a.Capture.Parent.ArtifactConsumption.Drives[0].DriveID == "caller-edited" {
		t.Fatal("wire response aliases returned acknowledgment")
	}
}

func TestVMMClientAdmittedSnapshotRefusesMalformedWireProof(t *testing.T) {
	for _, edit := range []func(*vmmdpb.CaptureAdmittedRuntimeResponse){
		func(p *vmmdpb.CaptureAdmittedRuntimeResponse) { p.Acknowledgment = nil },
		func(p *vmmdpb.CaptureAdmittedRuntimeResponse) { p.Acknowledgment.Capture.PrivateDrive = nil },
		func(p *vmmdpb.CaptureAdmittedRuntimeResponse) { p.Snapshot.MemBytes++ },
		func(p *vmmdpb.CaptureAdmittedRuntimeResponse) { p.Snapshot.BeforeCheckpointCompleted = true },
		func(p *vmmdpb.CaptureAdmittedRuntimeResponse) { p.Snapshot = nil },
		func(p *vmmdpb.CaptureAdmittedRuntimeResponse) {
			p.Acknowledgment.Capture.Memory.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
		},
	} {
		g, p := wireSnapshotGrantFixture(t)
		edit(p)
		s := &admittedSnapshotWireServer{response: p, grant: g.Clone()}
		cli := newPolicyWireClient(t, s)
		i, a, err := cli.CaptureAdmittedRuntime(t.Context(), g)
		if err == nil || !i.Capture.IsZero() || a.Grant.Token != "" || s.requests != 1 {
			t.Fatal("invalid wire proof accepted", err)
		}
	}
}

func TestVMMClientAdmittedSnapshotOldNodeHasNoLegacyFallback(t *testing.T) {
	g, _ := wireSnapshotGrantFixture(t)
	cli := newPolicyWireClient(t, &vmmdpb.UnimplementedVmmdServer{})
	i, a, err := cli.CaptureAdmittedRuntime(t.Context(), g)
	if err == nil || !i.Capture.IsZero() || a.Grant.Token != "" {
		t.Fatal("old node accepted admitted capture", err)
	}
}
