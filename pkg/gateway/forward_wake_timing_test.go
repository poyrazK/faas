package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type timingStream[Req, Resp any] struct {
	grpc.ClientStream
	init    *Resp
	blocked chan struct{}
	finish  chan struct{}
	err     error
}

func (*timingStream[Req, Resp]) Send(*Req) error  { return nil }
func (*timingStream[Req, Resp]) CloseSend() error { return nil }
func (s *timingStream[Req, Resp]) Recv() (*Resp, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.init != nil {
		init := s.init
		s.init = nil
		return init, nil
	}
	close(s.blocked)
	<-s.finish
	return nil, io.EOF
}

type timingVmmdClient struct {
	vmmdpb.VmmdClient
	httpStream *timingStream[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse]
	rawStream  *timingStream[vmmdpb.ForwardRawRequest, vmmdpb.ForwardRawResponse]
}

func (c *timingVmmdClient) ForwardHTTPStream(context.Context, ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse], error) {
	return c.httpStream, nil
}

func (c *timingVmmdClient) ForwardRawStream(context.Context, ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardRawRequest, vmmdpb.ForwardRawResponse], error) {
	return c.rawStream, nil
}

func TestForwardWakeTimingRecordsHeadersBeforeStreamEnds(t *testing.T) {
	for _, raw := range []bool{false, true} {
		for _, failed := range []bool{false, true} {
			name := "http"
			if raw {
				name = "raw"
			}
			if failed {
				name += "/failed_before_headers"
			}
			t.Run(name, func(t *testing.T) {
				blocked, finish := make(chan struct{}), make(chan struct{})
				client := &timingVmmdClient{
					httpStream: &timingStream[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse]{
						init:    &vmmdpb.ForwardHTTPStreamResponse{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{Status: 200}}},
						blocked: blocked, finish: finish,
					},
					rawStream: &timingStream[vmmdpb.ForwardRawRequest, vmmdpb.ForwardRawResponse]{
						init:    &vmmdpb.ForwardRawResponse{Frame: &vmmdpb.ForwardRawResponse_Init{Init: &vmmdpb.ForwardRawResponseInit{Status: 200}}},
						blocked: blocked, finish: finish,
					},
				}
				if failed {
					client.httpStream.err = status.Error(codes.Unavailable, "no response")
					client.rawStream.err = client.httpStream.err
				}
				r := httptest.NewRequest(http.MethodGet, "http://app.example/", nil)
				r = r.WithContext(WithFirstByteRecorder(r.Context(), &firstByteRecorder{}))
				w := httptest.NewRecorder()
				log := slog.New(slog.NewTextHandler(io.Discard, nil))
				done := make(chan struct{})
				defer func() {
					close(finish)
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Error("forwarder did not finish after the body stream closed")
					}
				}()
				go func() {
					defer close(done)
					if raw {
						rawStreamOnceWithEvents(w, r, client, log, Target{}, nil, nil)
					} else {
						fwdStreamOnceWithEvents(w, r, client, log, Target{}, nil)
					}
				}()
				if failed {
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Fatal("failed forwarder did not return")
					}
					if _, ok := FirstByteFrom(r); ok {
						t.Fatal("failed forwarder recorded an upstream first byte")
					}
					return
				}
				select {
				case <-blocked:
				case <-time.After(time.Second):
					t.Fatal("forwarder did not reach the body stream")
				}
				if _, ok := FirstByteFrom(r); !ok {
					t.Fatal("first byte was not recorded while the response body was still open")
				}
			})
		}
	}
}
