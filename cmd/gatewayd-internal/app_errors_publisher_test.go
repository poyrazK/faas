package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type batchStreamClient struct {
	mu      sync.Mutex
	streams []*batchStream
	recvErr error
}

type retryIssueClient struct {
	batchStreamClient
	retried []*apidpb.IncrementAppErrorRequest
	err     error
}

func (c *retryIssueClient) RecordHTTPIssue(_ context.Context, req *apidpb.IncrementAppErrorRequest) (*apidpb.IncrementAppErrorResponse, error) {
	c.retried = append(c.retried, req)
	if c.err != nil {
		return nil, c.err
	}
	return &apidpb.IncrementAppErrorResponse{Outcome: "accepted"}, nil
}

func TestAppErrorsPublisherWaitsForCompatibleApid(t *testing.T) {
	client := &retryIssueClient{err: status.Error(codes.Unimplemented, "older apid")}
	p := newAppErrorsPublisher(&appErrorsRecorder{}, client, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.queueHTTPIssueRetries([]appErrorRow{{AppID: "app", DeploymentID: "deployment", RequestID: "request", IssueEventID: "event", HTTPStatus: 500}})
	p.retryHTTPIssues(context.Background())
	if len(p.issueQueue) != 1 {
		t.Fatal("an older apid discarded the retry")
	}
	client.err = nil
	p.issueQueue[0].next = time.Time{}
	p.retryHTTPIssues(context.Background())
	if len(p.issueQueue) != 0 || len(client.retried) != 2 || client.retried[1].GetIssueEventId() != "event" {
		t.Fatal("retry did not recover after apid upgraded")
	}
}

func TestAppErrorsPublisherRetriesOnlyIssuesAfterAmbiguousStreamFailure(t *testing.T) {
	client := &retryIssueClient{batchStreamClient: batchStreamClient{recvErr: io.ErrUnexpectedEOF}}
	rec := newAppErrorsRecorder(appErrorsRecorderConfig{Enabled: true}, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	row := appErrorRow{AccountID: "account", AppID: "app", DeploymentID: "deployment", RequestID: "request", IssueEventID: "event", HTTPStatus: 500, ReceivedAt: time.Now(), Count: 7}
	rec.enqueue("app\x00fingerprint", row)
	wantEventID := rec.queue[0].IssueEventID
	p := newAppErrorsPublisher(rec, client, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.tryFlush(context.Background())
	if len(client.streams) != 1 || len(client.retried) != 0 {
		t.Fatal("expected one ambiguous legacy stream and a queued issue retry")
	}
	p.retryHTTPIssues(context.Background())
	if len(client.streams) != 1 || len(client.retried) != 1 || client.retried[0].GetIssueEventId() != wantEventID {
		t.Fatalf("retry did not preserve issue identity without replaying the legacy stream: %+v", client.retried)
	}
}

func (c *batchStreamClient) IncrementAppError(context.Context) (apidgrpc.AppErrorStream, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := &batchStream{recvErr: c.recvErr}
	c.streams = append(c.streams, s)
	return s, nil
}

func (*batchStreamClient) Close() error { return nil }

type batchStream struct {
	sent    int
	read    int
	closed  bool
	recvErr error
}

type earlyEOFStream struct{ batchStream }

func (*earlyEOFStream) Recv() (*apidpb.IncrementAppErrorResponse, error) { return nil, io.EOF }

type earlyEOFClient struct{}

func (earlyEOFClient) IncrementAppError(context.Context) (apidgrpc.AppErrorStream, error) {
	return &earlyEOFStream{}, nil
}
func (earlyEOFClient) Close() error { return nil }

func TestAppErrorsPublisherRejectsIncompleteAcknowledgements(t *testing.T) {
	p := newAppErrorsPublisher(&appErrorsRecorder{}, earlyEOFClient{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := p.flushBatch(context.Background(), []appErrorRow{{AccountID: "account", AppID: "app"}}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("incomplete stream = %v, want unexpected EOF", err)
	}
}

func (s *batchStream) Send(*apidpb.IncrementAppErrorRequest) error {
	if s.closed {
		return io.ErrClosedPipe
	}
	s.sent++
	return nil
}

func (s *batchStream) CloseSend() error {
	s.closed = true
	return nil
}

func (s *batchStream) Recv() (*apidpb.IncrementAppErrorResponse, error) {
	if s.read >= s.sent {
		if s.recvErr != nil {
			return nil, s.recvErr
		}
		return nil, io.EOF
	}
	s.read++
	return &apidpb.IncrementAppErrorResponse{Outcome: "inserted"}, nil
}

func TestAppErrorsPublisherSurfacesReceiveFailure(t *testing.T) {
	want := io.ErrUnexpectedEOF
	client := &batchStreamClient{recvErr: want}
	p := newAppErrorsPublisher(&appErrorsRecorder{}, client, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := p.flushBatch(context.Background(), []appErrorRow{{AccountID: "account", AppID: "app"}})
	if !errors.Is(err, want) {
		t.Fatalf("flushBatch error = %v, want %v", err, want)
	}
}

func TestAppErrorsPublisherOpensFreshStreamForEveryBatch(t *testing.T) {
	client := &batchStreamClient{}
	p := newAppErrorsPublisher(&appErrorsRecorder{}, client, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	batch := []appErrorRow{{AccountID: "account", AppID: "app"}}

	if err := p.flushBatch(context.Background(), batch); err != nil {
		t.Fatalf("first flushBatch: %v", err)
	}
	if err := p.flushBatch(context.Background(), batch); err != nil {
		t.Fatalf("second flushBatch reused a half-closed stream: %v", err)
	}
	if len(client.streams) != 2 {
		t.Fatalf("IncrementAppError calls = %d, want one fresh stream per batch", len(client.streams))
	}
}
