package main

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"google.golang.org/grpc"
)

func TestDeploymentResponseCheckRealForwardingFrame(t *testing.T) {
	for _, large := range []bool{false, true} {
		t.Run(map[bool]string{false: "small", true: "truncated stream"}[large], func(t *testing.T) {
			body := "ready"
			if large {
				body = strings.Repeat("x", api.DeploymentResponseCheckMaxBodyBytes+100)
			}
			stream := &responseCheckBridgeStream{body: []byte(body), waitForCancel: large}
			lookup := responseCheckBridgeLookup{client: &responseCheckBridgeClient{stream: stream}}
			checker := deploymentResponseChecker{appsDomain: "apps.example.test", forward: gateway.ForwardingReverseProxy(lookup, nil)}
			request := api.DeploymentResponseCheckRequest{AppID: uuid.NewString(), Scope: "default", Path: "/ready?ephemeral=1"}
			target := deploymentResponseTarget{Target: gateway.Target{AppID: request.AppID, DeploymentID: uuid.NewString(), InstanceID: uuid.NewString(), NodeID: uuid.NewString(), Port: 8087}, protocol: api.AppProtocolHTTP2}
			result, err := checker.check(t.Context(), target, "private-check", request)
			if err != nil || !result.Matches(request, target.DeploymentID) || result.BodyTruncated != large {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			frame := stream.init
			if frame == nil || frame.Instance != target.InstanceID || frame.Port != 8087 || frame.AppProtocol != api.AppProtocolHTTP2 || frame.Method != "GET" || frame.RequestUri != request.Path {
				t.Fatalf("bridge lost the exact target: %v", frame)
			}
			for _, header := range frame.Headers {
				if strings.EqualFold(header.Name, "Authorization") || strings.EqualFold(header.Name, "Cookie") {
					t.Fatal("account credentials reached the VMMD frame")
				}
			}
			if large && len(result.Body) != api.DeploymentResponseCheckMaxBodyBytes {
				t.Fatal("preview did not stop at the byte bound")
			}
		})
	}
}

type responseCheckBridgeLookup struct{ client vmmdpb.VmmdClient }

func (l responseCheckBridgeLookup) ClientFor(context.Context, string) (vmmdpb.VmmdClient, io.Closer, bool) {
	return l.client, io.NopCloser(strings.NewReader("")), true
}

type responseCheckBridgeClient struct {
	vmmdpb.VmmdClient
	stream *responseCheckBridgeStream
}

func (c *responseCheckBridgeClient) ForwardHTTPStream(ctx context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse], error) {
	c.stream.ctx = ctx
	return c.stream, nil
}

type responseCheckBridgeStream struct {
	grpc.ClientStream
	ctx           context.Context
	init          *vmmdpb.ForwardHTTPRequestInit
	body          []byte
	replies       int
	waitForCancel bool
}

func (s *responseCheckBridgeStream) CloseSend() error { return nil }

func (s *responseCheckBridgeStream) Send(request *vmmdpb.ForwardHTTPStreamRequest) error {
	if init := request.GetInit(); init != nil {
		s.init = init
	}
	return nil
}

func (s *responseCheckBridgeStream) Recv() (*vmmdpb.ForwardHTTPStreamResponse, error) {
	s.replies++
	switch s.replies {
	case 1:
		return &vmmdpb.ForwardHTTPStreamResponse{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{Status: 200}}}, nil
	case 2:
		return &vmmdpb.ForwardHTTPStreamResponse{Frame: &vmmdpb.ForwardHTTPStreamResponse_BodyChunk{BodyChunk: s.body}}, nil
	default:
		if s.waitForCancel {
			<-s.ctx.Done()
			return nil, s.ctx.Err()
		}
		return nil, io.EOF
	}
}
