package vmmdgrpc

import (
	"context"
	"encoding/json"
	"io"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type dataRepairVMM interface {
	DataRepairRehearsalIdentity() (string, string, string, error)
	RunDataRepairRehearsal(context.Context, core.RehearsalFrame, core.RehearsalInput) (core.Evidence, error)
	RetireDataRepairRehearsal(context.Context, core.RehearsalFrame) (time.Time, error)
}

func (s *Server) DataRepairRehearsalIdentity(_ context.Context, _ *vmmdpb.DataRepairRehearsalIdentityRequest) (*vmmdpb.DataRepairRehearsalIdentityResponse, error) {
	v, ok := s.vmm.(dataRepairVMM)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "repair rehearsal unavailable")
	}
	node, incarnation, runner, err := v.DataRepairRehearsalIdentity()
	if err != nil || node != s.qualificationNodeID || node == "" {
		return nil, status.Error(codes.FailedPrecondition, "repair rehearsal unavailable")
	}
	return &vmmdpb.DataRepairRehearsalIdentityResponse{NodeId: node, Incarnation: incarnation, RunnerSha256: runner}, nil
}

func (s *Server) repairFrame(ctx context.Context, b []byte) (core.RehearsalFrame, error) {
	f, err := core.DecodeRehearsalFrame(b)
	if err != nil || f.NodeID != s.qualificationNodeID || f.SnapshotSHA256 == "" {
		return f, status.Error(codes.InvalidArgument, "invalid repair rehearsal frame")
	}
	md, _ := metadata.FromIncomingContext(ctx)
	for key, want := range map[string]string{"x-faas-node-id": f.NodeID, "x-faas-instance-id": f.TaskID, "x-faas-app-id": f.Scope.AppID, "x-faas-wake-id": f.TaskID} {
		v := md.Get(key)
		if len(v) != 1 || v[0] != want {
			return f, status.Error(codes.InvalidArgument, "invalid repair rehearsal identity")
		}
	}
	return f, nil
}

type repairChunkReader struct {
	stream  vmmdpb.Vmmd_RunDataRepairRehearsalServer
	pending []byte
}

func (r *repairChunkReader) Read(b []byte) (int, error) {
	if len(r.pending) == 0 {
		m, err := r.stream.Recv()
		if err != nil {
			return 0, err
		}
		if m == nil || len(m.ProtoReflect().GetUnknown()) != 0 || len(m.FrameJson) != 0 || len(m.Chunk) == 0 || len(m.Chunk) > api.DataRepairRehearsalChunkBytes {
			return 0, core.ErrInvalid
		}
		r.pending = m.Chunk
	}
	n := copy(b, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func (s *Server) RunDataRepairRehearsal(stream vmmdpb.Vmmd_RunDataRepairRehearsalServer) error {
	v, ok := s.vmm.(dataRepairVMM)
	if !ok {
		return status.Error(codes.Unimplemented, "repair rehearsal unavailable")
	}
	m, err := stream.Recv()
	if err != nil {
		return status.Error(codes.InvalidArgument, "missing repair rehearsal frame")
	}
	if m == nil || len(m.ProtoReflect().GetUnknown()) != 0 || len(m.Chunk) != 0 {
		return status.Error(codes.InvalidArgument, "invalid repair rehearsal envelope")
	}
	f, err := s.repairFrame(stream.Context(), m.FrameJson)
	if err != nil {
		return err
	}
	if !time.Now().Before(f.Deadline) || f.Deadline.After(time.Now().Add(time.Duration(api.DataRepairTimeoutSeconds)*time.Second)) {
		return status.Error(codes.InvalidArgument, "invalid repair rehearsal deadline")
	}
	ctx, cancel := context.WithDeadline(stream.Context(), f.Deadline)
	defer cancel()
	in, err := readRepairStream(ctx, stream)
	if err != nil || !f.Matches(in.Plan) || in.SnapshotSHA256 != f.SnapshotSHA256 {
		return status.Error(codes.InvalidArgument, "invalid repair rehearsal input")
	}
	e, err := v.RunDataRepairRehearsal(ctx, f, in)
	if err != nil || !f.ValidOutcome(e) || !in.Plan.ValidCheckResults(e) {
		return status.Error(codes.FailedPrecondition, "repair rehearsal or cleanup unconfirmed")
	}
	b, _ := json.Marshal(e)
	return stream.SendAndClose(&vmmdpb.DataRepairRehearsalResult{EvidenceJson: b})
}

func readRepairStream(ctx context.Context, stream vmmdpb.Vmmd_RunDataRepairRehearsalServer) (core.RehearsalInput, error) {
	type decoded struct {
		in  core.RehearsalInput
		err error
	}
	done := make(chan decoded, 1)
	go func() {
		in, err := core.ReadRehearsalInput(&repairChunkReader{stream: stream})
		done <- decoded{in, err}
	}()
	select {
	case r := <-done:
		return r.in, r.err
	case <-ctx.Done():
		return core.RehearsalInput{}, ctx.Err()
	}
}

func (s *Server) RetireDataRepairRehearsal(ctx context.Context, in *vmmdpb.RetireDataRepairRehearsalRequest) (*vmmdpb.RetireDataRepairRehearsalResponse, error) {
	v, ok := s.vmm.(dataRepairVMM)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "repair rehearsal unavailable")
	}
	if in == nil || len(in.ProtoReflect().GetUnknown()) != 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid repair retirement")
	}
	f, err := s.repairFrame(ctx, in.FrameJson)
	if err != nil {
		return nil, err
	}
	at, err := v.RetireDataRepairRehearsal(ctx, f)
	if err != nil || at.IsZero() {
		return nil, status.Error(codes.FailedPrecondition, "repair rehearsal cleanup unconfirmed")
	}
	return &vmmdpb.RetireDataRepairRehearsalResponse{FrameJson: in.FrameJson, RetiredAt: timestamppb.New(at)}, nil
}

var _ io.Reader = (*repairChunkReader)(nil)
