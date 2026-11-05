// adr: 570
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"google.golang.org/grpc"
)

type nodeAdmissionSendHTTP struct {
	grpc.ClientStream
	final           error
	buffered        int
	sends, receives int
}

func (s *nodeAdmissionSendHTTP) Send(*vmmdpb.ForwardHTTPStreamRequest) error {
	s.sends++
	return io.EOF
}
func (s *nodeAdmissionSendHTTP) Recv() (*vmmdpb.ForwardHTTPStreamResponse, error) {
	s.receives++
	if s.buffered > 0 {
		s.buffered--
		return &vmmdpb.ForwardHTTPStreamResponse{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{Status: http.StatusOK}}}, nil
	}
	return nil, s.final
}

type nodeAdmissionSendRaw struct {
	grpc.ClientStream
	final            error
	buffered, failAt int
	sends, receives  int
}

func (s *nodeAdmissionSendRaw) Send(*vmmdpb.ForwardRawRequest) error {
	s.sends++
	if s.sends == s.failAt {
		return io.EOF
	}
	return nil
}
func (s *nodeAdmissionSendRaw) Recv() (*vmmdpb.ForwardRawResponse, error) {
	s.receives++
	if s.buffered > 0 {
		s.buffered--
		return &vmmdpb.ForwardRawResponse{Frame: &vmmdpb.ForwardRawResponse_Init{Init: &vmmdpb.ForwardRawResponseInit{Status: http.StatusOK}}}, nil
	}
	return nil, s.final
}

type nodeAdmissionSendClient struct {
	vmmdpb.VmmdClient
	http  *nodeAdmissionSendHTTP
	raw   *nodeAdmissionSendRaw
	opens int
}

func (c *nodeAdmissionSendClient) ForwardHTTPStream(context.Context, ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse], error) {
	c.opens++
	return c.http, nil
}
func (c *nodeAdmissionSendClient) ForwardRawStream(context.Context, ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardRawRequest, vmmdpb.ForwardRawResponse], error) {
	c.opens++
	return c.raw, nil
}

type nodeAdmissionSendLookup struct {
	client *nodeAdmissionSendClient
	calls  int
}

func (l *nodeAdmissionSendLookup) ClientFor(context.Context, string) (vmmdpb.VmmdClient, io.Closer, bool) {
	l.calls++
	return l.client, io.NopCloser(strings.NewReader("")), true
}

func TestForwardSendEOFRecoversNodeAdmissionStatus(t *testing.T) {
	for _, phase := range []string{"http_init", "raw_init", "raw_request_head"} {
		for _, code := range []string{api.CodeConcurrencyThrottled, api.CodeHTTPAdmissionUnavailable} {
			for _, buffered := range []int{0, 1} {
				t.Run(phase+"/"+code+"/buffered_"+string(rune('0'+buffered)), func(t *testing.T) {
					problem := api.NewProblem(api.StatusForCode(code), code, "Refused", "Before guest execution")
					if code == api.CodeConcurrencyThrottled {
						problem = problem.WithLimit(4, 5)
					}
					failAt := 1
					if phase == "raw_request_head" {
						failAt = 2
					}
					client := &nodeAdmissionSendClient{
						http: &nodeAdmissionSendHTTP{final: grpcerr.ToStatus(problem), buffered: buffered},
						raw:  &nodeAdmissionSendRaw{final: grpcerr.ToStatus(problem), buffered: buffered, failAt: failAt},
					}
					lookup := &nodeAdmissionSendLookup{client: client}
					request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.test/work", nil)
					request.Header.Set("x-faas-instance", "vm")
					target := Target{NodeID: "node", InstanceID: "vm", Port: 8080}
					proxy := ForwardingReverseProxy(lookup, nil)(target)
					if phase != "http_init" {
						request.Header.Set("Connection", "Upgrade")
						request.Header.Set("Upgrade", "websocket")
						proxy = ForwardingRawReverseProxy(lookup, nil, nil)(target)
					}
					recorder := httptest.NewRecorder()
					proxy.ServeHTTP(recorder, request)
					var got api.Problem
					if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
						t.Fatal(err)
					}
					if recorder.Code != api.StatusForCode(code) || got.Code != code || recorder.Header().Get("Retry-After") != "1" {
						t.Fatalf("lost refusal after send EOF: status=%d problem=%+v", recorder.Code, got)
					}
					if code == api.CodeConcurrencyThrottled && (got.Limit == nil || *got.Limit != 4 || got.Observed == nil || *got.Observed != 5) {
						t.Fatalf("lost trusted cap: %+v", got)
					}
					sends, receives := client.http.sends, client.http.receives
					if phase != "http_init" {
						sends, receives = client.raw.sends, client.raw.receives
					}
					if client.opens != 1 || lookup.calls != 1 || sends != failAt || receives != buffered+1 {
						t.Fatalf("refusal replayed or missed terminal status: opens=%d lookups=%d sends=%d receives=%d", client.opens, lookup.calls, sends, receives)
					}
				})
			}
		}
	}
}

func TestForwardSendStatusPreservesOtherFailures(t *testing.T) {
	stream := &nodeAdmissionSendHTTP{final: io.EOF}
	if err := forwardingSendStatus(io.ErrClosedPipe, stream); !errors.Is(err, io.ErrClosedPipe) || stream.receives != 0 {
		t.Fatalf("non-EOF send error changed or consumed response: err=%v receives=%d", err, stream.receives)
	}
	if err := forwardingSendStatus(io.EOF, stream); !errors.Is(err, io.EOF) || stream.receives != 1 {
		t.Fatalf("successful RPC terminal replaced send failure: err=%v receives=%d", err, stream.receives)
	}
}
