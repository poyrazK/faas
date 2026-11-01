package receivers

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
)

func startLookupHub(t *testing.T) (*RequestIDLookupHub, apidpb.RequestIDLookupsClient) {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	hub := NewRequestIDLookupHub(nil)
	RegisterRequestIDLookups(server, hub)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return hub, apidpb.NewRequestIDLookupsClient(conn)
}

// fakeLookupGateway serves lookups from a fixed table; silent never answers.
func fakeLookupGateway(t *testing.T, ctx context.Context, client apidpb.RequestIDLookupsClient, node string, known map[string]string, silent bool) {
	t.Helper()
	stream, err := client.Serve(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&apidpb.RequestIDLookupAnswer{Node: node}); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			question, err := stream.Recv()
			if err != nil {
				return
			}
			if silent {
				continue
			}
			answer := &apidpb.RequestIDLookupAnswer{QuestionId: question.GetQuestionId(), Node: node}
			if trace, ok := known[question.GetRequestId()]; ok {
				answer.Found, answer.RequestId, answer.TraceId = true, question.GetRequestId(), trace
				answer.ReceivedAtUnixMs = question.GetReceivedFromUnixMs() + 1
			}
			_ = stream.Send(answer)
		}
	}()
}

func waitForGateways(t *testing.T, hub *RequestIDLookupHub, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for hub.Gateways() != want {
		if time.Now().After(deadline) {
			t.Fatalf("gateways = %d, want %d", hub.Gateways(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRequestIDLookupHubFansOutAcrossGateways(t *testing.T) {
	hub, client := startLookupHub(t)
	from, until := time.Now().Add(-time.Hour), time.Now()
	if result := hub.Lookup(context.Background(), "acct", "app", "r1", from, until, time.Second); result.Asked != 0 || result.Found {
		t.Fatalf("no gateways: %+v, want nothing asked", result)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fakeLookupGateway(t, ctx, client, "fsn-2", map[string]string{"r1": "trace-1"}, false)
	fakeLookupGateway(t, ctx, client, "fsn-3", map[string]string{}, false)
	waitForGateways(t, hub, 2)

	result := hub.Lookup(context.Background(), "acct", "app", "r1", from, until, time.Second)
	if !result.Found || result.TraceID != "trace-1" || result.Node != "fsn-2" || result.Asked != 2 || result.Incomplete {
		t.Fatalf("lookup = %+v, want trace-1 from fsn-2 with both gateways answering", result)
	}
	if result := hub.Lookup(context.Background(), "acct", "app", "missing", from, until, time.Second); result.Found || result.Incomplete {
		t.Fatalf("miss = %+v, want a complete miss", result)
	}

	// A gateway that never answers makes a miss incomplete but does not hide
	// another gateway's hit.
	fakeLookupGateway(t, ctx, client, "fsn-4", nil, true)
	waitForGateways(t, hub, 3)
	result = hub.Lookup(context.Background(), "acct", "app", "r1", from, until, 100*time.Millisecond)
	if !result.Found || !result.Incomplete || result.Asked != 3 {
		t.Fatalf("with a silent gateway: %+v, want found and incomplete", result)
	}

	cancel()
	waitForGateways(t, hub, 0)
}

func TestRequestIDLookupHubRequiresNodeName(t *testing.T) {
	_, client := startLookupHub(t)
	stream, err := client.Serve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&apidpb.RequestIDLookupAnswer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err == nil {
		t.Fatal("a stream whose first message names no node was accepted")
	}
}
