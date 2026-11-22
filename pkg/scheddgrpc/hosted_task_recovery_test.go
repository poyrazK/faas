package scheddgrpc

import (
	"net"
	"testing"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestHostedTaskRehearsalInterruptionRequiresLocalUnixSocket(t *testing.T) {
	s := &Server{}
	req := &scheddpb.InterruptHostedTaskWorkerRequest{ObservedAt: timestamppb.Now()}
	for _, addr := range []net.Addr{nil, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}} {
		ctx := peer.NewContext(t.Context(), &peer.Peer{LocalAddr: addr})
		if _, err := s.InterruptHostedTaskWorker(ctx, req); status.Code(err) != codes.PermissionDenied {
			t.Errorf("interruption via %v returned %v", addr, err)
		}
	}
}
