package sched

// adr: 649
import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
)

func (r *VMMRouter) NativeRuntimeInventory(ctx context.Context, nodeID string) (fcvm.NativeRuntimeInventorySample, error) {
	if (runtimequota.HostEpoch{NodeID: nodeID, HostEpochID: uuid.NewString()}).Validate() != nil {
		return fcvm.NativeRuntimeInventorySample{}, runtimequota.ErrBinding
	}
	if r.regionalRuntimePeers == nil {
		return fcvm.NativeRuntimeInventorySample{}, runtimequota.ErrTrust
	}
	ctx, cancel := context.WithTimeout(ctx, api.NativeRuntimeInventoryTimeout)
	defer cancel()
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return fcvm.NativeRuntimeInventorySample{}, err
	}
	native, ok := cli.(*VMMClient)
	if !ok {
		return fcvm.NativeRuntimeInventorySample{}, runtimequota.ErrTrust
	}
	return native.nativeRuntimeInventory(ctx, nodeID, uuid.NewString(), r.regionalRuntimePeers)
}

func (c *VMMClient) nativeRuntimeInventory(ctx context.Context, nodeID, challenge string, resolver wire.NodeIdentityResolver) (fcvm.NativeRuntimeInventorySample, error) {
	stream, err := c.cli.ObserveNativeRuntimeInventory(ctx, &vmmdpb.ObserveNativeRuntimeInventoryRequest{NodeId: nodeID, Challenge: challenge}, grpc.MaxCallRecvMsgSize(api.NativeRuntimeInventoryFrameMaxBytes))
	if err != nil {
		return fcvm.NativeRuntimeInventorySample{}, liftErr(err)
	}
	// Commit the actual transport attempt and authenticate its TLS peer before
	// receiving payload. A certificate for another node cannot supply a sample.
	if _, err := stream.Header(); err != nil {
		return fcvm.NativeRuntimeInventorySample{}, liftErr(err)
	}
	cn, err := wire.PeerCN(stream.Context()) //nolint:contextcheck // TLS peer identity belongs to the authenticated transport context.
	if err != nil {
		return fcvm.NativeRuntimeInventorySample{}, runtimequota.ErrTrust
	}
	node, err := resolver.NodeIDByCN(cn)
	if err != nil || node != nodeID {
		return fcvm.NativeRuntimeInventorySample{}, runtimequota.ErrTrust
	}
	raw, err := receiveNativeRuntimeInventory(stream.Recv)
	if err != nil {
		return fcvm.NativeRuntimeInventorySample{}, err
	}
	sample, err := fcvm.DecodeNativeRuntimeInventorySample(raw)
	if err != nil || sample.Host.NodeID != nodeID || sample.Challenge != challenge {
		return fcvm.NativeRuntimeInventorySample{}, runtimequota.ErrTrust
	}
	if err := ctx.Err(); err != nil {
		return fcvm.NativeRuntimeInventorySample{}, err
	}
	return sample, nil
}

func receiveNativeRuntimeInventory(recv func() (*vmmdpb.NativeRuntimeInventoryFrame, error)) ([]byte, error) {
	var raw []byte
	complete := false
	for {
		frame, err := recv()
		if errors.Is(err, io.EOF) && complete {
			return raw, nil
		}
		if err != nil {
			return nil, err
		}
		if frame == nil || complete || len(frame.ProtoReflect().GetUnknown()) != 0 || frame.GetOffset() != uint32(len(raw)) {
			return nil, runtimequota.ErrTrust
		}
		if frame.GetComplete() {
			digest := sha256.Sum256(raw)
			if len(raw) == 0 || len(frame.GetPayload()) != 0 || !bytes.Equal(frame.GetSha256(), digest[:]) {
				return nil, runtimequota.ErrTrust
			}
			complete = true
			continue
		}
		chunk := frame.GetPayload()
		if len(chunk) == 0 || len(chunk) > api.NativeRuntimeInventoryChunkMaxBytes || len(raw) > api.NativeRuntimeInventoryMaxBytes-len(chunk) || len(frame.GetSha256()) != 0 {
			return nil, runtimequota.ErrTrust
		}
		raw = append(raw, chunk...)
	}
}
