// adr: 590
package vmmdgrpc

import (
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc/metadata"
)

func TestCheckpointWakeEnvelopeCannotUseGenericWake(t *testing.T) {
	for _, key := range []string{"x-faas-checkpoint-wake-version", "x-faas-checkpoint-wake-future-capability"} {
		ctx := metadata.NewIncomingContext(t.Context(), metadata.Pairs(key, "unknown"))
		// Nil payloads demonstrate that rejection precedes generic request parsing.
		if out, err := toColdBootRequest(ctx, (*vmmdpb.CreateColdBootRequest)(nil)); err == nil || out.Instance != "" {
			t.Fatal("tagged cold boot used ordinary parser", err)
		}
		if out, err := toWakeRequest(ctx, (*vmmdpb.CreateFromSnapshotRequest)(nil)); err == nil || out.Instance != "" {
			t.Fatal("tagged restore used ordinary parser", err)
		}
	}
}
