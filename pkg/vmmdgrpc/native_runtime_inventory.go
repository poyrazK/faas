package vmmdgrpc

// adr: 649
import (
	"context"
	"crypto/sha256"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type nativeRuntimeInventoryProvider interface {
	RegionalRuntimeHostEpoch() (runtimequota.HostEpoch, error)
	WithNativeRuntimeInventory(context.Context, func(context.Context, fcvm.NativeRuntimeInventory) error) error
}

func (s *Server) ObserveNativeRuntimeInventory(req *vmmdpb.ObserveNativeRuntimeInventoryRequest, stream vmmdpb.Vmmd_ObserveNativeRuntimeInventoryServer) (result error) {
	start := time.Now()
	defer func() { s.ops.Observe("ObserveNativeRuntimeInventory", time.Since(start), result) }()
	cn, err := wire.PeerCN(stream.Context())
	if err != nil || cn != "schedd.faas" {
		return status.Error(codes.Unauthenticated, "native inventory requires authenticated schedd")
	}
	deadline, bounded := stream.Context().Deadline()
	if !bounded || time.Until(deadline) > api.NativeRuntimeInventoryTimeout {
		return status.Error(codes.InvalidArgument, "native inventory requires a bounded transport deadline")
	}
	challenge, err := uuid.Parse(req.GetChallenge())
	if err != nil || challenge == uuid.Nil || challenge.String() != req.GetChallenge() || req.GetNodeId() == "" || req.GetNodeId() != s.nodeID {
		return status.Error(codes.InvalidArgument, "native inventory requires exact node and fresh challenge")
	}
	provider, ok := s.vmm.(nativeRuntimeInventoryProvider)
	if !ok {
		return status.Error(codes.Unimplemented, "native observation unavailable")
	}
	ctx, cancel := context.WithTimeout(stream.Context(), api.NativeRuntimeInventoryTimeout)
	defer cancel()
	return sendNativeRuntimeInventory(ctx, req, provider, stream.Send)
}

func sendNativeRuntimeInventory(ctx context.Context, req *vmmdpb.ObserveNativeRuntimeInventoryRequest, provider nativeRuntimeInventoryProvider, send func(*vmmdpb.NativeRuntimeInventoryFrame) error) error {
	host, err := provider.RegionalRuntimeHostEpoch()
	if err != nil || host.Validate() != nil || host.NodeID != req.GetNodeId() {
		return status.Error(codes.FailedPrecondition, "native inventory journal identity unavailable")
	}
	var terminal *vmmdpb.NativeRuntimeInventoryFrame
	err = provider.WithNativeRuntimeInventory(ctx, func(ctx context.Context, inventory fcvm.NativeRuntimeInventory) error {
		if terminal != nil {
			return status.Error(codes.FailedPrecondition, "native inventory consumer repeated")
		}
		raw, err := fcvm.EncodeNativeRuntimeInventorySample(fcvm.NativeRuntimeInventorySample{Version: 1, Host: host, Challenge: req.GetChallenge(), Inventory: inventory})
		if err != nil {
			return status.Error(codes.ResourceExhausted, "native inventory cannot be encoded completely within bounds")
		}
		digest := sha256.Sum256(raw)
		terminal = &vmmdpb.NativeRuntimeInventoryFrame{Offset: uint32(len(raw)), Complete: true, Sha256: digest[:]}
		for offset := 0; offset < len(raw); offset += api.NativeRuntimeInventoryChunkMaxBytes {
			if err := ctx.Err(); err != nil {
				return status.FromContextError(err).Err()
			}
			if err := send(&vmmdpb.NativeRuntimeInventoryFrame{Offset: uint32(offset), Payload: raw[offset:min(offset+api.NativeRuntimeInventoryChunkMaxBytes, len(raw))]}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if ctx.Err() != nil {
			return status.FromContextError(ctx.Err()).Err()
		}
		return status.Error(codes.FailedPrecondition, "native inventory observation did not finish")
	}
	after, err := provider.RegionalRuntimeHostEpoch()
	if err != nil || after != host || terminal == nil {
		return status.Error(codes.FailedPrecondition, "native inventory journal changed or observation is incomplete")
	}
	if err := ctx.Err(); err != nil {
		return status.FromContextError(err).Err()
	}
	return send(terminal) // Only after the producer's final pinned rechecks succeed.
}
