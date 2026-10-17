package functionmigration

// adr: 1547
// An in-memory gRPC transport verifies fixture wiring only. It supplies no VM,
// PostgreSQL, packet-loss or native acceptance evidence.

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

type adoptionReplyServer struct {
	vmmdpb.UnimplementedVmmdServer
	call func(context.Context, *vmmdpb.AdoptMigratedInstanceRequest) (*vmmdpb.AdoptMigratedInstanceResponse, error)
}

func (s *adoptionReplyServer) AdoptMigratedInstance(ctx context.Context, req *vmmdpb.AdoptMigratedInstanceRequest) (*vmmdpb.AdoptMigratedInstanceResponse, error) {
	return s.call(ctx, req)
}

func adoptionTransport(t *testing.T, call func(context.Context, *vmmdpb.AdoptMigratedInstanceRequest) (*vmmdpb.AdoptMigratedInstanceResponse, error)) vmmdpb.VmmdClient {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(server, &adoptionReplyServer{call: call})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	conn, err := grpc.NewClient("passthrough:///acceptance-replies", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return vmmdpb.NewVmmdClient(conn)
}

func adoptionMessages() (*vmmdpb.AdoptMigratedInstanceRequest, *vmmdpb.AdoptMigratedInstanceResponse) {
	return &vmmdpb.AdoptMigratedInstanceRequest{InstanceId: "instance", WakeId: "wake", LeaseToken: "fixture-only-token", Plan: "fixture-only-plan"},
		&vmmdpb.AdoptMigratedInstanceResponse{WakeId: "wake", Method: vmmdpb.WakeMethod_WAKE_RESTORE, GuestUid: 20001, HostIp: "10.100.0.1", Netns: "fc-fixture"}
}

func TestReplayAdoptionSendsConcurrentIndependentIdenticalRPCs(t *testing.T) {
	request, reply := adoptionMessages()
	before := proto.Clone(request)
	var mu sync.Mutex
	active, maximum, calls := 0, 0, 0
	joined := make(chan struct{})
	client := adoptionTransport(t, func(ctx context.Context, req *vmmdpb.AdoptMigratedInstanceRequest) (*vmmdpb.AdoptMigratedInstanceResponse, error) {
		if !proto.Equal(req, request) {
			return nil, status.Error(codes.InvalidArgument, "fixture request changed")
		}
		mu.Lock()
		active++
		calls++
		maximum = max(maximum, active)
		if active == 3 {
			close(joined)
		}
		mu.Unlock()
		defer func() { mu.Lock(); active--; mu.Unlock() }()
		select {
		case <-joined:
			return proto.Clone(reply).(*vmmdpb.AdoptMigratedInstanceResponse), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	evidence, err := ReplayAdoption(ctx, request, reply, func(ctx context.Context, req *vmmdpb.AdoptMigratedInstanceRequest) (*vmmdpb.AdoptMigratedInstanceResponse, error) {
		return client.AdoptMigratedInstance(ctx, req)
	}, 3)
	if err != nil || !proto.Equal(before, request) || len(evidence.Requests) != 3 || len(evidence.Replies) != 3 {
		t.Fatal("concurrent retry did not preserve its request/reply evidence", err)
	}
	for i := range 3 {
		if evidence.Requests[i] != evidence.RequestSHA || evidence.Replies[i] != evidence.ReplySHA {
			t.Fatal("concurrent retry changed request or completed response")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 3 || maximum != 3 {
		t.Fatal("fixture did not execute three overlapping RPCs")
	}
}

func TestReplayAdoptionRejectsChangedOrFailedReplies(t *testing.T) {
	for _, failed := range []bool{false, true} {
		request, reply := adoptionMessages()
		var calls atomic.Int32
		client := adoptionTransport(t, func(context.Context, *vmmdpb.AdoptMigratedInstanceRequest) (*vmmdpb.AdoptMigratedInstanceResponse, error) {
			calls.Add(1)
			if failed {
				return nil, status.Error(codes.Unavailable, "fixture transport failure")
			}
			other := proto.Clone(reply).(*vmmdpb.AdoptMigratedInstanceResponse)
			other.WakeId = "another-wake"
			return other, nil
		})
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		_, err := ReplayAdoption(ctx, request, reply, func(ctx context.Context, req *vmmdpb.AdoptMigratedInstanceRequest) (*vmmdpb.AdoptMigratedInstanceResponse, error) {
			return client.AdoptMigratedInstance(ctx, req)
		}, 1)
		cancel()
		if err == nil || calls.Load() != 1 {
			t.Fatal("retry hid a changed response or retried an error implicitly")
		}
	}
}

func TestReplayAdoptionCancellationPreventsDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	request, reply := adoptionMessages()
	var calls atomic.Int32
	_, err := ReplayAdoption(ctx, request, reply, func(context.Context, *vmmdpb.AdoptMigratedInstanceRequest) (*vmmdpb.AdoptMigratedInstanceResponse, error) {
		calls.Add(1)
		return reply, nil
	}, 1)
	if !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatal("cancelled observation dispatched another native attempt")
	}
}
