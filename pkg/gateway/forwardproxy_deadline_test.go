// adr: 375
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
	if err := stream.Send(&vmmdpb.ForwardRawResponse{Frame: &vmmdpb.ForwardRawResponse_Init{Init: &vmmdpb.ForwardRawResponseInit{Status: int32(s.status)}}}); err != nil {
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
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx, cancel, _ := reqbudget.WithStarted(r.Context(), time.Now(), 150*time.Millisecond, api.RequestBudgetMax, "forward", "GET:/slow")
				defer cancel()
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
		name        string
		status      int
		headerDelay time.Duration
		wantAbort   bool
	}{
		{"successful-upgrade", http.StatusSwitchingProtocols, 0, false},
		{"ordinary-refusal-body", http.StatusOK, 0, true},
		{"expired-handshake", http.StatusSwitchingProtocols, 300 * time.Millisecond, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := &deadlineForwardServer{status: tc.status, headerDelay: tc.headerDelay, finished: make(chan error, 1)}
			client := newDeadlineForwardClient(t, fixture)
			r := httptest.NewRequest(http.MethodGet, "http://app.test/socket", nil)
			ctx, cancel, _ := reqbudget.WithStarted(r.Context(), time.Now(), 150*time.Millisecond, api.RequestBudgetMax, "forward", "GET:/socket")
			defer cancel()
			r = r.WithContext(ctx)
			r.Header.Set("Connection", "Upgrade")
			r.Header.Set("Upgrade", "websocket")
			rec := httptest.NewRecorder()
			var aborted bool
			func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						recoveredErr, ok := recovered.(error)
						if !ok || !errors.Is(recoveredErr, http.ErrAbortHandler) {
							panic(recovered)
						}
						aborted = true
					}
				}()
				rawStreamOnceWithEvents(rec, r, client, slog.New(slog.NewTextHandler(io.Discard, nil)), Target{NodeID: "node-1"}, nil, nil)
			}()
			if aborted != tc.wantAbort {
				t.Fatalf("aborted=%v, want %v", aborted, tc.wantAbort)
			}
			if tc.headerDelay > 0 {
				assertTotalTimeout(t, rec)
			} else if tc.wantAbort {
				if rec.Body.String() != "partial" {
					t.Fatalf("ordinary refusal body=%q, want partial", rec.Body.String())
				}
			} else if rec.Code != http.StatusSwitchingProtocols || rec.Body.String() != "partial-complete" {
				t.Fatalf("upgrade status=%d body=%q; want established session after handshake deadline", rec.Code, rec.Body.String())
			}
			select {
			case <-fixture.finished:
			case <-time.After(time.Second):
				t.Fatal("raw RPC retained the completed exchange")
			}
		})
	}
}
