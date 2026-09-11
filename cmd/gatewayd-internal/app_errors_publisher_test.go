package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
)

type batchStreamClient struct {
	mu      sync.Mutex
	streams []*batchStream
	recvErr error
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
