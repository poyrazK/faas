package main

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type fakeRealtimeHistoryStore struct {
	endpoint state.ManagedRealtimeEndpoint
	history  state.ManagedRealtimeChannelHistory
	err      error
}

func (f *fakeRealtimeHistoryStore) ManagedRealtimeEndpointByID(_ context.Context, id string) (state.ManagedRealtimeEndpoint, error) {
	if id != f.endpoint.ID {
		return state.ManagedRealtimeEndpoint{}, state.ErrNotFound
	}
	return f.endpoint, nil
}

func (f *fakeRealtimeHistoryStore) AppendManagedRealtimeChannelMessage(context.Context, string, string, []byte, bool, string) (state.ManagedRealtimeChannelMessage, error) {
	panic("read-only RPC must not append")
}

func (f *fakeRealtimeHistoryStore) ReadManagedRealtimeChannelHistory(_ context.Context, endpointID, channel string, after int64, limit int) (state.ManagedRealtimeChannelHistory, error) {
	if endpointID != f.endpoint.ID || channel != "updates" || after != 812 || limit != 100 {
		panic("unexpected history query")
	}
	return f.history, f.err
}

func TestRealtimeHistoryRPC(t *testing.T) {
	stamp := time.Date(2026, time.September, 28, 12, 0, 0, 123, time.UTC)
	store := &fakeRealtimeHistoryStore{
		endpoint: state.ManagedRealtimeEndpoint{ID: "endpoint-1", Enabled: true},
		history: state.ManagedRealtimeChannelHistory{
			OldestSequence: 810, LatestSequence: 820,
			Messages: []state.ManagedRealtimeChannelMessage{{Sequence: 813, Data: []byte("hello"), Binary: true, CreatedAt: stamp}},
		},
	}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	apidpb.RegisterRealtimeHistoryServer(server, &realtimeHistoryReceiver{store: store})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	ctx := context.Background()
	conn, err := grpc.NewClient("passthrough://bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := apidgrpc.NewRealtimeHistoryClient(conn)
	page, err := client.ReadChannelHistory(ctx, "endpoint-1", "updates", 812, 100)
	if err != nil {
		t.Fatal(err)
	}
	if page.OldestSequence != 810 || page.LatestSequence != 820 || len(page.Messages) != 1 ||
		page.Messages[0].Sequence != 813 || string(page.Messages[0].Data) != "hello" ||
		!page.Messages[0].Binary || !page.Messages[0].CreatedAt.Equal(stamp) {
		t.Fatalf("round trip page = %+v", page)
	}
	store.history = state.ManagedRealtimeChannelHistory{OldestSequence: 815, LatestSequence: 820, HistoryUnavailable: true}
	page, err = client.ReadChannelHistory(ctx, "endpoint-1", "updates", 812, 100)
	if err != nil || !page.HistoryUnavailable || len(page.Messages) != 0 || page.OldestSequence != 815 {
		t.Fatalf("expired cursor page = %+v, err = %v", page, err)
	}
	store.endpoint.Enabled = false
	_, err = client.ReadChannelHistory(ctx, "endpoint-1", "updates", 812, 100)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("disabled endpoint error = %v", err)
	}
	store.endpoint.Enabled = true
	_, err = client.ReadChannelHistory(ctx, "other-endpoint", "updates", 812, 100)
	if status.Code(err) != codes.NotFound {
		t.Fatalf("unknown endpoint error = %v", err)
	}
	_, err = client.ReadChannelHistory(ctx, "endpoint-1", "updates", -1, 100)
	if !errors.Is(err, state.ErrManagedRealtimeHistoryInvalid) {
		t.Fatalf("local invalid cursor error = %v", err)
	}
	_, err = apidpb.NewRealtimeHistoryClient(conn).ReadChannelHistory(ctx, &apidpb.ReadChannelHistoryRequest{EndpointId: "endpoint-1", Channel: "updates", AfterSequence: -1, Limit: 100})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("server invalid cursor error = %v", err)
	}
}
