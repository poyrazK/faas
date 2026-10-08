// adr: 570
package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reqbudget"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

type deadlineForwardServer struct {
	vmmdpb.UnimplementedVmmdServer
	status      int
	finished    chan error
	headerDelay time.Duration
}

func (s *deadlineForwardServer) ForwardHTTPStream(stream grpc.BidiStreamingServer[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse]) error {
	defer func() { s.finished <- stream.Context().Err() }()
	if _, err := stream.Recv(); err != nil {
		return err
	}
	if err := stream.Send(&vmmdpb.ForwardHTTPStreamResponse{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{Status: int32(s.status)}}}); err != nil {
		return err
	}
	if err := stream.Send(&vmmdpb.ForwardHTTPStreamResponse{Frame: &vmmdpb.ForwardHTTPStreamResponse_BodyChunk{BodyChunk: []byte("partial")}}); err != nil {
		return err
	}
	timer := time.NewTimer(300 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-stream.Context().Done():
		return stream.Context().Err()
	case <-timer.C:
		return stream.Send(&vmmdpb.ForwardHTTPStreamResponse{Frame: &vmmdpb.ForwardHTTPStreamResponse_BodyChunk{BodyChunk: []byte("-complete")}})
	}
}

func (s *deadlineForwardServer) ForwardRawStream(stream grpc.BidiStreamingServer[vmmdpb.ForwardRawRequest, vmmdpb.ForwardRawResponse]) error {
	defer func() { s.finished <- stream.Context().Err() }()
	if _, err := stream.Recv(); err != nil {
		return err
	}
	if s.headerDelay > 0 {
		timer := time.NewTimer(s.headerDelay)
		defer timer.Stop()
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-timer.C:
		}
	}
	var headers []*vmmdpb.Header
	if s.status == http.StatusSwitchingProtocols {
		headers = []*vmmdpb.Header{{Name: "Connection", Value: "Upgrade"}, {Name: "Upgrade", Value: "websocket"}}
	}
	if err := stream.Send(&vmmdpb.ForwardRawResponse{Frame: &vmmdpb.ForwardRawResponse_Init{Init: &vmmdpb.ForwardRawResponseInit{Status: int32(s.status), Headers: headers}}}); err != nil {
		return err
	}
	if err := stream.Send(&vmmdpb.ForwardRawResponse{Frame: &vmmdpb.ForwardRawResponse_BodyChunk{BodyChunk: []byte("partial")}}); err != nil {
		return err
	}
	timer := time.NewTimer(300 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-stream.Context().Done():
		return stream.Context().Err()
	case <-timer.C:
		return stream.Send(&vmmdpb.ForwardRawResponse{Frame: &vmmdpb.ForwardRawResponse_BodyChunk{BodyChunk: []byte("-complete")}})
	}
}

func newDeadlineForwardClient(t *testing.T, fixture vmmdpb.VmmdServer) vmmdpb.VmmdClient {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(server, fixture)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := wire.DialContext(context.Background(), "unix:///deadline-fixture", nil, grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return vmmdpb.NewVmmdClient(conn)
}

type flushDeadlineWriter struct{ http.ResponseWriter }

func (w flushDeadlineWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w flushDeadlineWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.ResponseWriter.(http.Flusher).Flush()
	return n, err
}

// The gRPC wire has its own timeout metadata. An in-memory stream fake cannot
// prove either ordinary body cancellation or successful handshake detachment.
func TestForwarderDeadlineThroughRealGRPC(t *testing.T) {
	for _, tc := range []struct {
		name, protocol string
		streaming      bool
		status         int
		wantComplete   bool
	}{
		{"ordinary-http1", "http1", false, http.StatusOK, false},
		{"ordinary-http2", "http2", false, http.StatusOK, false},
		{"explicit-stream", "http1", true, http.StatusOK, true},
		{"grpc", "grpc", false, http.StatusOK, true},
		{"failed-stream", "http1", true, http.StatusServiceUnavailable, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := &deadlineForwardServer{status: tc.status, finished: make(chan error, 1)}
			nodes := singleClientLookup{cli: newDeadlineForwardClient(t, fixture)}
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			decisions := make(chan trafficDecisionSnapshot, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx, cancel, _ := reqbudget.WithStarted(withTrafficDecision(r.Context(), false), time.Now(), 150*time.Millisecond, api.RequestBudgetMax, "forward", "GET:/slow")
				defer cancel()
				defer func() { decisions <- trafficDecisionEvidence(ctx, tc.status, true) }()
				r = r.WithContext(ctx)
				r.Header.Set("x-faas-protocol", tc.protocol)
				if tc.streaming {
					r.Header.Set("x-faas-stream", "true")
				}
				fwdOnceWithEvents(flushDeadlineWriter{w}, r, nodes, log, Target{NodeID: "node-1"}, nil)
			}))
			t.Cleanup(srv.Close)
			client := srv.Client()
			client.Timeout = 2 * time.Second
			resp, err := client.Get(srv.URL + "/slow")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, readErr := io.ReadAll(resp.Body)
			if tc.wantComplete {
				if readErr != nil || string(body) != "partial-complete" {
					t.Fatalf("body=%q error=%v; want complete after handshake budget", body, readErr)
				}
			} else if readErr == nil || string(body) != "partial" {
				t.Fatalf("body=%q error=%v; want visible truncation on total expiry", body, readErr)
			}
			select {
			case evidence := <-decisions:
				want := "deadline"
				if tc.wantComplete {
					want = "edge_response" // Direct transport fixture has no proxy-attempt owner.
				}
				if evidence.streamDetached != tc.wantComplete || evidence.outcome != want {
					t.Fatalf("wire detachment evidence=%+v, want outcome=%s", evidence, want)
				}
			case <-time.After(time.Second):
				t.Fatal("forwarder retained its decision after response cleanup")
			}
			select {
			case serverErr := <-fixture.finished:
				if !tc.wantComplete && !errors.Is(serverErr, context.Canceled) && !errors.Is(serverErr, context.DeadlineExceeded) {
					t.Fatalf("upstream cleanup error=%v; want cancellation", serverErr)
				}
			case <-time.After(time.Second):
				t.Fatal("upstream RPC retained the expired request")
			}
		})
	}
}

func TestRawForwarderDeadlineThroughRealGRPC(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		status                           int
		headerDelay                      time.Duration
		upgrade, streaming, wantComplete bool
	}{
		{"successful-upgrade", http.StatusSwitchingProtocols, 0, true, false, true},
		{"ordinary-refusal-body", http.StatusOK, 0, true, false, false},
		{"failed-declared-upgrade", http.StatusOK, 0, true, true, false},
		{"ordinary-raw-body", http.StatusOK, 0, false, false, false},
		{"declared-raw-stream", http.StatusOK, 0, false, true, true},
		{"expired-handshake", http.StatusSwitchingProtocols, 300 * time.Millisecond, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := &deadlineForwardServer{status: tc.status, headerDelay: tc.headerDelay, finished: make(chan error, 1)}
			client := newDeadlineForwardClient(t, fixture)
			decisions := make(chan trafficDecisionSnapshot, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx, cancel, _ := reqbudget.WithStarted(withTrafficDecision(r.Context(), false), time.Now(), 150*time.Millisecond, api.RequestBudgetMax, "forward", "GET:/socket")
				defer cancel()
				defer func() { decisions <- trafficDecisionEvidence(ctx, tc.status, true) }()
				r = r.WithContext(ctx)
				if tc.streaming {
					r.Header.Set("x-faas-stream", "true")
				}
				rawStreamOnceWithEvents(flushDeadlineWriter{w}, r, client, slog.New(slog.NewTextHandler(io.Discard, nil)), Target{NodeID: "node-1"}, nil, nil)
			}))
			t.Cleanup(srv.Close)
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/socket", nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.upgrade {
				request.Header.Set("Connection", "Upgrade")
				request.Header.Set("Upgrade", "websocket")
			}
			httpClient := srv.Client()
			httpClient.Timeout = 2 * time.Second
			response, err := httpClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			body, readErr := io.ReadAll(response.Body)
			if tc.headerDelay > 0 {
				rec := httptest.NewRecorder()
				rec.Code = response.StatusCode
				for name, values := range response.Header {
					rec.Header()[name] = values
				}
				_, _ = rec.Body.Write(body)
				assertTotalTimeout(t, rec)
			} else if tc.wantComplete {
				if response.StatusCode != tc.status || readErr != nil || string(body) != "partial-complete" {
					t.Fatalf("status=%d body=%q error=%v; want complete session", response.StatusCode, body, readErr)
				}
			} else if readErr == nil || string(body) != "partial" {
				t.Fatalf("body=%q error=%v; want visible truncation on total expiry", body, readErr)
			}
			select {
			case evidence := <-decisions:
				want := "deadline"
				if tc.wantComplete {
					want = "edge_response"
				}
				// Closing an upgraded socket can cancel net/http's request root
				// after the complete body; that verdict still excludes a deadline.
				completeSocketClose := tc.wantComplete && tc.upgrade && evidence.outcome == "canceled"
				if (evidence.outcome != want && !completeSocketClose) || evidence.streamDetached != tc.wantComplete {
					t.Fatalf("raw detachment evidence=%+v, want outcome=%s", evidence, want)
				}
			case <-time.After(time.Second):
				t.Fatal("raw forwarder retained its decision")
			}
			select {
			case <-fixture.finished:
			case <-time.After(time.Second):
				t.Fatal("raw RPC retained the completed exchange")
			}
		})
	}
}
