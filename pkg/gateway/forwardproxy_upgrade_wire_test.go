// adr: 386
// adr: 610
package gateway

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/gateway/activity"
	"github.com/onebox-faas/faas/pkg/reqbudget"
	"google.golang.org/grpc"
)

func TestRawUpgradeRealSocketCarriesBothDirections(t *testing.T) {
	for _, early := range []bool{false, true} {
		t.Run(map[bool]string{false: "after handshake", true: "buffered with handshake"}[early], func(t *testing.T) {
			tracker, err := activity.New(uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			target := Target{Port: 3000, AppID: uuid.NewString(), DeploymentID: uuid.NewString()}
			forward := WithDeploymentActivity(func(target Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					rawStreamOnceWithEvents(w, r, &upgradeWireClient{}, slog.Default(), target, nil, nil)
				})
			}, tracker)(target)
			finished := make(chan struct{})
			observed := make(chan struct {
				status int
				bytes  int64
			}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(finished)
				ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
				defer cancel()
				ctx, cancelBudget, _ := reqbudget.WithRemaining(ctx, 100*time.Millisecond, 100*time.Millisecond, "upgrade", "GET:/ws")
				defer cancelBudget()
				r = r.WithContext(ctx)
				r.Header.Set("x-faas-instance", "wire-instance")
				recorder := &statusRecorder{ResponseWriter: w, status: 200}
				forward.ServeHTTP(recorder, r)
				observed <- struct {
					status int
					bytes  int64
				}{recorder.status, recorder.Bytes}
			}))
			defer server.Close()
			conn, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
				t.Fatal(err)
			}
			head := []byte("GET /ws HTTP/1.1\r\nHost: wire.example\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n")
			payload := bytes.Repeat([]byte{0x82, 0xfe, 0, 0, 0xff, 0x80, 0x0d, 0x0a}, 9600)
			initial := head
			if early {
				initial = append(append([]byte(nil), head...), payload...)
			}
			if _, err := conn.Write(initial); err != nil {
				t.Fatal(err)
			}
			reader := bufio.NewReader(conn)
			response, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
			if err != nil {
				t.Fatalf("upgrade response never reached the real socket: %v", err)
			}
			defer func() { _ = response.Body.Close() }()
			if response.StatusCode != 101 || response.Header.Get("Upgrade") != "websocket" {
				t.Fatalf("upgrade response: %d %v", response.StatusCode, response.Header)
			}
			if got := tracker.Observe(target.AppID, target.DeploymentID); !got.CoverageKnown || got.ActiveForwards != 1 {
				t.Fatalf("101 handshake ended activity before socket close: %+v", got)
			}
			if !early {
				time.Sleep(150 * time.Millisecond)
				if _, err := conn.Write(payload); err != nil {
					t.Fatal(err)
				}
			}
			got := make([]byte, len(payload))
			if _, err := io.ReadFull(reader, got); err != nil {
				t.Fatalf("upgraded bytes did not round trip: %v", err)
			}
			if !bytes.Equal(got, payload) {
				t.Fatal("upgraded binary bytes changed")
			}
			if got := tracker.Observe(target.AppID, target.DeploymentID); !got.CoverageKnown || got.ActiveForwards != 1 {
				t.Fatalf("long-lived raw pump dropped activity: %+v", got)
			}
			_ = conn.Close()
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("client disconnect did not cancel the RPC and handler")
			}
			if got := <-observed; got.status != 101 || got.bytes != int64(len(payload)) {
				t.Fatalf("recorded upgrade status/bytes = %+v", got)
			}
			if got := tracker.Observe(target.AppID, target.DeploymentID); !got.CoverageKnown || got.ActiveForwards != 0 || got.ActivityVersion != 3 {
				t.Fatalf("socket disconnect retained activity: %+v", got)
			}
		})
	}
}

func TestRawUpgradeRequiresSocketOwnership(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/ws", nil)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	writer := httptest.NewRecorder()
	rawStreamOnceWithEvents(writer, request, &upgradeWireClient{}, slog.Default(), Target{Port: 3000}, nil, nil)
	if writer.Code != http.StatusBadGateway || writer.Header().Get("Upgrade") != "" {
		t.Fatalf("unsupported writer must refuse an upgrade: %d %v", writer.Code, writer.Header())
	}
}

type upgradeWireClient struct{ stubVmmdClient }

func (*upgradeWireClient) ForwardRawStream(ctx context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardRawRequest, vmmdpb.ForwardRawResponse], error) {
	return &upgradeWireStream{ctx: ctx, responses: make(chan *vmmdpb.ForwardRawResponse, 16)}, nil
}

type upgradeWireStream struct {
	grpc.ClientStream
	ctx       context.Context
	responses chan *vmmdpb.ForwardRawResponse
	headSent  bool
}

func (s *upgradeWireStream) Context() context.Context { return s.ctx }
func (*upgradeWireStream) CloseSend() error           { return nil }
func (s *upgradeWireStream) Send(request *vmmdpb.ForwardRawRequest) error {
	if request.GetInit() != nil {
		return nil
	}
	var response *vmmdpb.ForwardRawResponse
	if !s.headSent {
		s.headSent = true
		response = &vmmdpb.ForwardRawResponse{Frame: &vmmdpb.ForwardRawResponse_Init{Init: &vmmdpb.ForwardRawResponseInit{
			Status: 101, Headers: []*vmmdpb.Header{{Name: "Connection", Value: "Upgrade"}, {Name: "Upgrade", Value: "websocket"}},
		}}}
	} else {
		response = &vmmdpb.ForwardRawResponse{Frame: &vmmdpb.ForwardRawResponse_BodyChunk{BodyChunk: append([]byte(nil), request.GetBodyChunk()...)}}
	}
	select {
	case s.responses <- response:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}
func (s *upgradeWireStream) Recv() (*vmmdpb.ForwardRawResponse, error) {
	select {
	case response := <-s.responses:
		return response, nil
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}
