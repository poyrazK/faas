// adr: 125
// adr: 133
// adr: 696
package gateway

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/gateway/activity"
	"github.com/onebox-faas/faas/pkg/reqbudget"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// Exercise the production HTTP-to-vmmd gRPC bridge, rather than a mirror
// RoundTripper stub which never constructs a request-budget stream session.
func TestDispatchMirrorDetachedRequestThroughGRPC(t *testing.T) {
	for _, cancelSource := range []string{"request finished", "budget released", "budget expired"} {
		t.Run(cancelSource, func(t *testing.T) {
			listener := bufconn.Listen(1 << 20)
			server := grpc.NewServer()
			bridge := &mirrorDetachedWireServer{received: make(chan *vmmdpb.ForwardHTTPRequestInit, 1)}
			vmmdpb.RegisterVmmdServer(server, bridge)
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(server.Stop)
			t.Cleanup(func() { _ = listener.Close() })
			conn, err := grpc.NewClient("passthrough:///mirror-test",
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close() })

			root, finishRequest := context.WithCancel(context.Background())
			defer finishRequest()
			lifetime := time.Minute
			if cancelSource == "budget expired" {
				lifetime = time.Millisecond
			}
			budget, releaseBudget, _ := reqbudget.WithRemaining(root, lifetime, lifetime, "forward", "GET:/echo")
			defer releaseBudget()
			switch cancelSource {
			case "request finished":
				finishRequest()
			case "budget released":
				releaseBudget()
			case "budget expired":
				<-budget.Done()
			}

			tracker, err := activity.New(uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			appID, deploymentID := uuid.NewString(), uuid.NewString()
			backend := &mirrorParkerBackend{mirrorTargetFakeBackend: &mirrorTargetFakeBackend{
				mirrorFakeBackend: &mirrorFakeBackend{},
				target:            Target{AppID: appID, NodeID: "shadow-node", InstanceID: "shadow", DeploymentID: deploymentID, Port: 3000},
			}}
			ledger := &mirrorResultStoreFake{results: make(chan state.MirrorInvocationResult, 1)}
			h := &Handler{backend: backend, log: slog.New(slog.NewTextHandler(io.Discard, nil)), mirrorResultStore: ledger}
			h.proxyByNode = WithDeploymentActivity(func(target Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if got := tracker.Observe(appID, deploymentID); !got.CoverageKnown || got.ActiveForwards != 1 {
						t.Errorf("detached mirror was not counted: %+v", got)
					}
					fwdStreamOnceWithEvents(w, r, vmmdpb.NewVmmdClient(conn), h.log, target, nil)
				})
			}, tracker)
			source := newMirrorSourceCapture()
			source.writeHeader(http.StatusOK)
			source.write([]byte(`{"ok":true}`))
			source.complete()
			request := httptest.NewRequest(http.MethodGet, "http://example.test/echo", nil).WithContext(budget)
			request.Header.Set("Authorization", "Bearer test-only-secret")
			h.dispatchMirror(budget, "primary", nil, MirrorRuleRow{ID: "rule", AppID: appID, MirrorDeploymentID: deploymentID, IncludeBody: true}, request, nil, "request", source)
			if got := tracker.Observe(appID, deploymentID); !got.CoverageKnown || got.ActiveForwards != 0 || got.ActivityVersion != 3 {
				t.Fatalf("detached mirror retained activity: %+v", got)
			}

			result := <-ledger.results
			if result.StatusCode != http.StatusOK || result.Crashed || result.StatusDiff || result.SchemaDiff || result.BodyDiff || result.ComparisonIncomplete {
				t.Fatalf("mirror must finish after source cancellation: %+v", result)
			}
			select {
			case init := <-bridge.received:
				if init.Instance != "shadow" || init.Port != 3000 {
					t.Fatalf("mirror target: %+v", init)
				}
				for _, header := range init.Headers {
					if http.CanonicalHeaderKey(header.Name) == "Authorization" {
						t.Fatal("mirror forwarded source credentials")
					}
				}
			default:
				t.Fatal("mirror never reached vmmd")
			}
			if len(backend.parks) != 1 || backend.parks[0].ctxErr != nil {
				t.Fatalf("mirror cleanup: %+v", backend.parks)
			}
		})
	}
}

type mirrorDetachedWireServer struct {
	vmmdpb.UnimplementedVmmdServer
	received chan *vmmdpb.ForwardHTTPRequestInit
}

func (s *mirrorDetachedWireServer) ForwardHTTPStream(stream grpc.BidiStreamingServer[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse]) error {
	request, err := stream.Recv()
	if err != nil {
		return err
	}
	s.received <- request.GetInit()
	if err := stream.Send(&vmmdpb.ForwardHTTPStreamResponse{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{Status: http.StatusOK}}}); err != nil {
		return err
	}
	return stream.Send(&vmmdpb.ForwardHTTPStreamResponse{Frame: &vmmdpb.ForwardHTTPStreamResponse_BodyChunk{BodyChunk: []byte(`{"ok":true}`)}})
}

func TestBuildMirrorRequestPreservesRequestURI(t *testing.T) {
	for _, uri := range []string{"/echo?a=1&b=%2F&b=two+words", "/a%2Fb?q=%26", "/echo?", "/"} {
		t.Run(uri, func(t *testing.T) {
			source := httptest.NewRequest(http.MethodGet, "http://example.test"+uri, nil)
			mirror, err := (&Handler{}).buildMirrorRequest(context.Background(), MirrorRuleRow{}, source, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := mirror.URL.RequestURI(); got != uri {
				t.Fatalf("mirror request URI = %q, want %q", got, uri)
			}
		})
	}
}

func TestDefaultMirrorTransportPreservesRequestURI(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.RequestURI)
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, uri := range []string{"/a%2Fb?q=%26&x=1&x=2", "/echo?"} {
		request, err := (&Handler{}).buildMirrorRequest(context.Background(), MirrorRuleRow{}, httptest.NewRequest(http.MethodGet, uri, nil), nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := NewDefaultMirrorRoundTripper(upstream.Client()).RoundTripMirror(context.Background(), target, request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || string(body) != uri {
			t.Fatalf("mirror upstream received %q, want %q (read error %v)", body, uri, err)
		}
	}
}
