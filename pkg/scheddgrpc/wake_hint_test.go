// adr: 1151 — the gateway's session-affinity wake hint crosses the schedd
// RPC as metadata, bounded, and is absent when the gateway sets none.
package scheddgrpc

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
	"google.golang.org/grpc/metadata"
)

func incomingFromOutgoing(ctx context.Context) context.Context {
	md, _ := metadata.FromOutgoingContext(ctx)
	return metadata.NewIncomingContext(context.Background(), md)
}

func TestPreferredInstanceHintCrossesTheWire(t *testing.T) {
	out := withWakeCorrelation(sched.WithPreferredInstance(context.Background(), "inst-1"), "gateway")
	if got := sched.PreferredInstanceFromContext(withIncomingCorrelation(incomingFromOutgoing(out))); got != "inst-1" {
		t.Fatalf("hint after the wire = %q, want inst-1", got)
	}
	if got := sched.PreferredInstanceFromContext(withIncomingCorrelation(incomingFromOutgoing(withWakeCorrelation(context.Background(), "")))); got != "" {
		t.Fatalf("hint without one set = %q, want empty", got)
	}
	long := metadata.NewIncomingContext(context.Background(), metadata.Pairs(preferredInstanceMetadataKey, strings.Repeat("x", maxPreferredInstanceLen+1)))
	if got := sched.PreferredInstanceFromContext(withPreferredInstanceIncoming(long)); got != "" {
		t.Fatalf("over-long hint accepted as %q", got)
	}
}
