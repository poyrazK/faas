package sched

import (
	"context"
	"encoding/json"
	"io"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	"github.com/onebox-faas/faas/pkg/wire"
)

func repairOutgoing(ctx context.Context, f core.RehearsalFrame) context.Context {
	fields := wire.CorrelationFields{NodeID: f.NodeID, AppID: f.Scope.AppID, InstanceID: f.TaskID, WakeID: f.TaskID}
	return wire.WithCorrelationOutgoing(wire.WithContext(ctx, fields), fields)
}

func (c *VMMClient) DataRepairRehearsalIdentity(ctx context.Context, node string, spec core.RunnerSpec) (string, error) {
	r, err := c.cli.DataRepairRehearsalIdentity(ctx, &vmmdpb.DataRepairRehearsalIdentityRequest{})
	if err != nil {
		return "", liftErr(err)
	}
	id, e := uuid.Parse(r.GetIncarnation())
	if len(r.ProtoReflect().GetUnknown()) != 0 || r.NodeId != node || r.RunnerSha256 != spec.Digest() || e != nil || id == uuid.Nil || id.String() != r.Incarnation {
		return "", core.ErrConflict
	}
	return r.Incarnation, nil
}

type repairChunkWriter struct {
	stream vmmdpb.Vmmd_RunDataRepairRehearsalClient
}

func (w repairChunkWriter) Write(b []byte) (int, error) {
	total := 0
	for len(b) > 0 {
		n := min(len(b), api.DataRepairRehearsalChunkBytes)
		if err := w.stream.Send(&vmmdpb.DataRepairRehearsalChunk{Chunk: b[:n]}); err != nil {
			return total, err
		}
		b = b[n:]
		total += n
	}
	return total, nil
}

func (c *VMMClient) RunDataRepairRehearsal(ctx context.Context, f core.RehearsalFrame, in core.RehearsalInput) (core.Evidence, error) {
	var e core.Evidence
	if f.Validate() != nil || !f.Matches(in.Plan) || in.SnapshotSHA256 != f.SnapshotSHA256 {
		return e, core.ErrInvalid
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := c.cli.RunDataRepairRehearsal(repairOutgoing(ctx, f))
	if err != nil {
		return e, liftErr(err)
	}
	b, _ := json.Marshal(f)
	if err = stream.Send(&vmmdpb.DataRepairRehearsalChunk{FrameJson: b}); err != nil {
		return e, liftErr(err)
	}
	if err = core.WriteRehearsalInput(repairChunkWriter{stream}, in); err != nil {
		return e, liftErr(err)
	}
	response, err := stream.CloseAndRecv()
	if err != nil {
		return e, liftErr(err)
	}
	if len(response.ProtoReflect().GetUnknown()) != 0 || len(response.EvidenceJson) > api.DataRepairMaxRequestBytes || json.Unmarshal(response.EvidenceJson, &e) != nil || !f.ValidOutcome(e) || !in.Plan.ValidCheckResults(e) {
		return core.Evidence{}, core.ErrConflict
	}
	return e, nil
}

func (c *VMMClient) RetireDataRepairRehearsal(ctx context.Context, f core.RehearsalFrame) error {
	b, _ := json.Marshal(f)
	r, err := c.cli.RetireDataRepairRehearsal(repairOutgoing(ctx, f), &vmmdpb.RetireDataRepairRehearsalRequest{FrameJson: b})
	if err != nil {
		return liftErr(err)
	}
	returned, err := core.DecodeRehearsalFrame(r.GetFrameJson())
	if err != nil || !returned.Same(f) || len(r.ProtoReflect().GetUnknown()) != 0 || r.RetiredAt == nil || r.RetiredAt.CheckValid() != nil || r.RetiredAt.AsTime().IsZero() {
		return core.ErrConflict
	}
	return nil
}

func (r *VMMRouter) DataRepairRehearsalIdentity(ctx context.Context, node string, spec core.RunnerSpec) (string, error) {
	cli, err := r.resolveFor(ctx, node)
	if err != nil {
		return "", err
	}
	v, ok := cli.(DataRepairRehearsalVMM)
	if !ok {
		return "", core.ErrUnavailable
	}
	return v.DataRepairRehearsalIdentity(ctx, node, spec)
}
func (r *VMMRouter) RunDataRepairRehearsal(ctx context.Context, f core.RehearsalFrame, in core.RehearsalInput) (core.Evidence, error) {
	cli, err := r.resolveFor(ctx, f.NodeID)
	if err != nil {
		return core.Evidence{}, err
	}
	v, ok := cli.(DataRepairRehearsalVMM)
	if !ok {
		return core.Evidence{}, core.ErrUnavailable
	}
	return v.RunDataRepairRehearsal(ctx, f, in)
}
func (r *VMMRouter) RetireDataRepairRehearsal(ctx context.Context, f core.RehearsalFrame) error {
	cli, err := r.resolveFor(ctx, f.NodeID)
	if err != nil {
		return err
	}
	v, ok := cli.(DataRepairRehearsalVMM)
	if !ok {
		return core.ErrUnavailable
	}
	return v.RetireDataRepairRehearsal(ctx, f)
}

var _ io.Writer = repairChunkWriter{}
