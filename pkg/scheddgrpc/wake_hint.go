package scheddgrpc

import (
	"context"

	"github.com/onebox-faas/faas/pkg/sched"
	"google.golang.org/grpc/metadata"
)

// preferredInstanceMetadataKey carries the gateway's wake hint (ADR-1151):
// the instance a session-affinity cookie names. It is a hint, never an
// authorization input: schedd only reorders the deployment's own frozen
// candidates by it.
const preferredInstanceMetadataKey = "x-faas-preferred-instance"

// maxPreferredInstanceLen bounds the hint; instance IDs are UUIDs.
const maxPreferredInstanceLen = 64

func withPreferredInstanceOutgoing(ctx context.Context) context.Context {
	if id := sched.PreferredInstanceFromContext(ctx); id != "" && len(id) <= maxPreferredInstanceLen {
		return metadata.AppendToOutgoingContext(ctx, preferredInstanceMetadataKey, id)
	}
	return ctx
}

func withPreferredInstanceIncoming(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx
	}
	if values := md.Get(preferredInstanceMetadataKey); len(values) == 1 && len(values[0]) <= maxPreferredInstanceLen {
		return sched.WithPreferredInstance(ctx, values[0])
	}
	return ctx
}
