package gateway

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/requestjournal"
)

func openLocalJournal(t *testing.T) *requestjournal.Store {
	t.Helper()
	store, err := requestjournal.Open(t.TempDir(), requestjournal.Config{Span: 10 * time.Minute, Grace: time.Minute, Budget: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// scriptedLookupServer is apid's side of the lookup stream: it asks each
// question in turn and reports the gateway's node and answers.
type scriptedLookupServer struct {
	apidpb.UnimplementedRequestIDLookupsServer
	questions []*apidpb.RequestIDLookupQuestion
	node      chan string
	answers   chan *apidpb.RequestIDLookupAnswer
}

func (s *scriptedLookupServer) Serve(stream apidpb.RequestIDLookups_ServeServer) error {
	hello, err := stream.Recv()
	if err != nil {
		return err
	}
	s.node <- hello.GetNode()
	for _, question := range s.questions {
		if err := stream.Send(question); err != nil {
			return err
		}
		answer, err := stream.Recv()
		if err != nil {
			return err
		}
		s.answers <- answer
	}
	<-stream.Context().Done()
	return nil
}

func TestLocalRequestIDJournalAnswersApidLookups(t *testing.T) {
	store := openLocalJournal(t)
	account, app := uuid.New(), uuid.New()
	received := time.Now().UTC().Truncate(time.Millisecond)
	writer := LocalRequestIDJournalWriter(store)
	result, err := writer(context.Background(), []RequestIDJournalRecord{
		{AccountID: account.String(), AppID: app.String(), RequestID: "kept", TraceID: "trace-kept", ReceivedAt: received, ExpiresAt: received.Add(72 * time.Hour)},
		{AccountID: account.String(), AppID: app.String(), RequestID: "free-plan", ReceivedAt: received},
		{AccountID: "not-a-uuid", AppID: app.String(), RequestID: "bad", ReceivedAt: received, ExpiresAt: received.Add(time.Hour)},
	})
	if err != nil || result.Recorded != 1 || result.Rejected != 2 {
		t.Fatalf("writer = %+v, %v; want 1 recorded and 2 rejected (no debugger plan, bad account)", result, err)
	}

	window := func(id string) *apidpb.RequestIDLookupQuestion {
		return &apidpb.RequestIDLookupQuestion{QuestionId: id + "-q", AccountId: account.String(), AppId: app.String(), RequestId: id,
			ReceivedFromUnixMs: received.Add(-time.Hour).UnixMilli(), ReceivedUntilUnixMs: received.Add(time.Hour).UnixMilli()}
	}
	server := &scriptedLookupServer{
		questions: []*apidpb.RequestIDLookupQuestion{window("kept"), window("free-plan"), {QuestionId: "bad-q", AccountId: "x", AppId: "y", RequestId: "kept"}},
		node:      make(chan string, 1),
		answers:   make(chan *apidpb.RequestIDLookupAnswer, 3),
	}
	listener := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	apidpb.RegisterRequestIDLookupsServer(grpcServer, server)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ServeRequestIDLookups(ctx, apidpb.NewRequestIDLookupsClient(conn), "fsn-3", store, nil)

	if node := <-server.node; node != "fsn-3" {
		t.Fatalf("first message named node %q, want fsn-3", node)
	}
	kept := <-server.answers
	if !kept.GetFound() || kept.GetQuestionId() != "kept-q" || kept.GetTraceId() != "trace-kept" || kept.GetReceivedAtUnixMs() != received.UnixMilli() {
		t.Fatalf("answer for a kept record = %+v", kept)
	}
	if miss := <-server.answers; miss.GetFound() || miss.GetError() != "" {
		t.Fatalf("answer for a record the free plan never kept = %+v, want a clean miss", miss)
	}
	if bad := <-server.answers; bad.GetError() == "" {
		t.Fatalf("answer for malformed IDs = %+v, want an error", bad)
	}
}
