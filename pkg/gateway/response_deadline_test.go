// adr: 375
package gateway

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/reqbudget"
	pkgtrace "github.com/onebox-faas/faas/pkg/trace"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"google.golang.org/grpc"
)

const slowReaderBodySize = 64 << 20 // exceeds transport windows, below Pro's cap

type deadlineFloodServer struct {
	vmmdpb.UnimplementedVmmdServer
	finished chan error
	sent     atomic.Int64
}

func (s *deadlineFloodServer) ForwardRawStream(stream grpc.BidiStreamingServer[vmmdpb.ForwardRawRequest, vmmdpb.ForwardRawResponse]) error {
	defer func() { s.finished <- stream.Context().Err() }()
	if _, err := stream.Recv(); err != nil {
		return err
	}
	if err := stream.Send(&vmmdpb.ForwardRawResponse{Frame: &vmmdpb.ForwardRawResponse_Init{Init: &vmmdpb.ForwardRawResponseInit{Status: http.StatusOK}}}); err != nil {
		return err
	}
	chunk := make([]byte, 64<<10)
	for s.sent.Load() < slowReaderBodySize {
		if err := stream.Send(&vmmdpb.ForwardRawResponse{Frame: &vmmdpb.ForwardRawResponse_BodyChunk{BodyChunk: chunk}}); err != nil {
			return err
		}
		s.sent.Add(int64(len(chunk)))
	}
	return nil
}

func (s *deadlineFloodServer) ForwardHTTPStream(stream grpc.BidiStreamingServer[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse]) error {
	defer func() { s.finished <- stream.Context().Err() }()
	if _, err := stream.Recv(); err != nil {
		return err
	}
	init := &vmmdpb.ForwardHTTPResponseInit{Status: http.StatusOK, Headers: []*vmmdpb.Header{
		{Name: trafficSecurityHeader, Value: trafficSecurityRealtime},
		{Name: trafficResponseDeadlineHeader, Value: "1"},
		{Name: trafficResponseSessionHeader, Value: "long-lived"},
		{Name: api.StreamingStatusHeader, Value: string(api.StreamingStatusStreaming)},
	}}
	if err := stream.Send(&vmmdpb.ForwardHTTPStreamResponse{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: init}}); err != nil {
		return err
	}
	chunk := make([]byte, 64<<10)
	for s.sent.Load() < slowReaderBodySize {
		if err := stream.Send(&vmmdpb.ForwardHTTPStreamResponse{Frame: &vmmdpb.ForwardHTTPStreamResponse_BodyChunk{BodyChunk: chunk}}); err != nil {
			return err
		}
		s.sent.Add(int64(len(chunk)))
	}
	return nil
}

// A local socket/window can hide the blocked-write bug with a small body.
// Keep the connection open without consuming its body, then require both
// gateway handler ownership and the real upstream RPC to end before closing it.
func TestTotalDeadlineReleasesSlowResponseReader(t *testing.T) {
	for _, path := range []string{"direct-forwarder", "public-compute", "public-upgrade-refusal"} {
		for _, h2 := range []bool{false, true} {
			if h2 && path == "public-upgrade-refusal" {
				continue // HTTP/1 Upgrade is a separate transport contract.
			}
			t.Run(path+"/h2="+strconv.FormatBool(h2), func(t *testing.T) {
				fixture := &deadlineFloodServer{finished: make(chan error, 1)}
				nodes := singleClientLookup{cli: newDeadlineForwardClient(t, fixture)}
				log := slog.New(slog.NewTextHandler(io.Discard, nil))
				forward := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					fwdOnceWithEvents(w, r, nodes, log, Target{NodeID: "node"}, nil)
				})
				var handler http.Handler
				host := "app.test"
				if path == "direct-forwarder" {
					handler = reqbudget.MiddlewareConfig{Default: 250 * time.Millisecond, Max: time.Second}.Middleware(forward)
				} else {
					h, backend, _ := newTestHandler(t)
					backend.setLegacyHot()
					setTotalBudget(h, backend.app, 250)
					h.proxyByNode = func(Target) http.Handler { return forward }
					if path == "public-upgrade-refusal" {
						backend.app.WebSocketEnabled = true
						h.rawByNode = func(Target) http.Handler {
							return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
								rawStreamOnceWithEvents(w, r, nodes.cli, log, Target{NodeID: "node"}, nil, nil)
							})
						}
					}
					host = backend.host
					compute := httptest.NewUnstartedServer(TrustedTrafficIngress(h))
					compute.Config.Protocols = new(http.Protocols)
					compute.Config.Protocols.SetHTTP1(true)
					compute.Config.Protocols.SetUnencryptedHTTP2(true)
					compute.Start()
					t.Cleanup(compute.Close)
					proxy := NewInternalReverseProxy(&stubDialer{server: compute}, &url.URL{Scheme: "http", Host: "compute"}, log, h2)
					handler = httpsec.Static(RequestIDMiddleware(reqbudget.MiddlewareConfig{Default: 5 * time.Second, Max: 5 * time.Second}.Middleware(
						otelhttp.NewHandler(pkgtrace.WithTraceIDHeader(proxy), "public"))))
				}
				done := make(chan struct{})
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					defer close(done)
					handler.ServeHTTP(w, r)
				}))
				server.EnableHTTP2 = h2
				if h2 {
					server.StartTLS()
				} else {
					server.Start()
				}
				t.Cleanup(server.Close)
				started := time.Now()
				resp, closeClient := openSlowResponseReader(t, server, host, h2, path == "public-upgrade-refusal")
				defer func() { closeClient(); _ = resp.Body.Close() }()
				if resp.StatusCode != http.StatusOK || h2 && resp.ProtoMajor != 2 {
					t.Fatalf("response status/protocol = %d/%s", resp.StatusCode, resp.Proto)
				}
				if path != "direct-forwarder" && (resp.Header.Get(trafficResponseDeadlineHeader) != "" || resp.Header.Get(trafficResponseSessionHeader) != "") {
					t.Fatal("private response controls leaked to the customer")
				}
				select {
				case <-done:
					if elapsed := time.Since(started); elapsed > time.Second {
						t.Fatalf("response ownership retained for %s", elapsed)
					}
				case <-time.After(time.Second):
					t.Fatal("expired response retained a blocked downstream writer")
				}
				select {
				case err := <-fixture.finished:
					if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
						t.Fatalf("upstream ended without cancellation: %v", err)
					}
				case <-time.After(time.Second):
					t.Fatal("expired response retained its upstream RPC")
				}
				if sent := fixture.sent.Load(); sent <= 0 || sent >= slowReaderBodySize {
					t.Fatalf("fixture did not hit transport backpressure: sent=%d", sent)
				}
			})
		}
	}
}

func openSlowResponseReader(t *testing.T, server *httptest.Server, host string, h2, upgrade bool) (*http.Response, func()) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, server.URL+"/large", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = host
	if upgrade {
		request.Header.Set("Connection", "Upgrade")
		request.Header.Set("Upgrade", "websocket")
	}
	if h2 {
		client := server.Client()
		client.Timeout = 3 * time.Second
		resp, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return resp, func() { _ = resp.Body.Close(); client.CloseIdleConnections() }
	}
	conn, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetReadBuffer(1024)
	}
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if err := request.Write(conn); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), request)
	if err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	return resp, func() { _ = conn.Close(); _ = resp.Body.Close() }
}

type backpressureResponseBody struct {
	read   atomic.Int64
	closed atomic.Bool
}

func (b *backpressureResponseBody) Read(p []byte) (int, error) {
	if b.closed.Load() {
		return 0, io.ErrClosedPipe
	}
	remaining := slowReaderBodySize - b.read.Load()
	if remaining <= 0 {
		return 0, io.EOF
	}
	n := min(len(p), int(remaining))
	clear(p[:n])
	b.read.Add(int64(n))
	return n, nil
}

func (b *backpressureResponseBody) Close() error { b.closed.Store(true); return nil }

func TestResponseCopyCancellationReleasesSlowWriter(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run("h2="+strconv.FormatBool(h2), func(t *testing.T) {
			body := &backpressureResponseBody{}
			cancelRequest := make(chan context.CancelFunc, 1)
			done := make(chan error, 1)
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx, cancel := context.WithCancel(r.Context()) // no deadline
				defer cancel()
				cancelRequest <- cancel
				_, err := copyResponseBody(ctx, w, body)
				done <- err
			}))
			server.EnableHTTP2 = h2
			if h2 {
				server.StartTLS()
			} else {
				server.Start()
			}
			t.Cleanup(server.Close)
			resp, closeClient := openSlowResponseReader(t, server, "app.test", h2, false)
			defer func() { closeClient(); _ = resp.Body.Close() }()
			cancel := <-cancelRequest
			defer cancel()
			time.Sleep(150 * time.Millisecond) // fill the socket/window while the client stays open
			select {
			case err := <-done:
				t.Fatalf("fixture completed before cancellation: %v", err)
			default:
			}
			if read := body.read.Load(); read <= 0 || read >= slowReaderBodySize {
				t.Fatalf("fixture did not encounter response backpressure: read=%d", read)
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) || !body.closed.Load() {
					t.Fatalf("cancellation error=%v, source closed=%v", err, body.closed.Load())
				}
			case <-time.After(time.Second):
				t.Fatal("body copier retained its downstream writer after cancellation")
			}
		})
	}
}

func TestResponseDeadlineTransportCannotExtendPublicBudget(t *testing.T) {
	public, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	publicDeadline, _ := public.Deadline()
	for _, tc := range []struct {
		name   string
		values []string
		error  bool
		tight  bool
	}{
		{"absent", nil, false, false},
		{"later", []string{strconv.FormatInt(publicDeadline.Add(time.Hour).UnixNano(), 10)}, false, false},
		{"earlier", []string{strconv.FormatInt(publicDeadline.Add(-500*time.Millisecond).UnixNano(), 10)}, false, true},
		{"invalid", []string{"never"}, true, false},
		{"duplicate", []string{"1", "2"}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{Header: make(http.Header)}
			for _, value := range tc.values {
				resp.Header.Add(trafficResponseDeadlineHeader, value)
			}
			ctx, stop, err := responseBodyContext(context.WithoutCancel(public), public, resp)
			if (err != nil) != tc.error {
				t.Fatalf("error=%v, want error=%v", err, tc.error)
			}
			if err != nil {
				return
			}
			defer stop()
			deadline, _ := ctx.Deadline()
			if deadline.After(publicDeadline) || tc.tight != deadline.Before(publicDeadline) || resp.Header.Get(trafficResponseDeadlineHeader) != "" {
				t.Fatalf("deadline=%s public=%s, private=%q", deadline, publicDeadline, resp.Header.Get(trafficResponseDeadlineHeader))
			}
		})
	}
}

func TestInternalReverseProxyInvalidComputeDeadlineRefusesBeforeCommit(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		for _, values := range [][]string{{"invalid"}, {"1", "2"}} {
			t.Run(strconv.FormatBool(upgrade)+"/"+strings.Join(values, ","), func(t *testing.T) {
				compute := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					for _, value := range values {
						w.Header().Add(trafficResponseDeadlineHeader, value)
					}
					_, _ = io.WriteString(w, "must not reach customer")
				}))
				defer compute.Close()
				proxy := NewInternalReverseProxy(&stubDialer{server: compute}, &url.URL{Scheme: "http", Host: "compute"}, slog.New(slog.NewTextHandler(io.Discard, nil)), false)
				request := httptest.NewRequest(http.MethodGet, "http://app.test/", nil)
				if upgrade {
					request.Header.Set("Connection", "Upgrade")
					request.Header.Set("Upgrade", "websocket")
				}
				rec := httptest.NewRecorder()
				proxy.ServeHTTP(rec, request)
				if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "must not reach") || rec.Header().Get(trafficResponseDeadlineHeader) != "" {
					t.Fatalf("unverified response status=%d body=%s headers=%v", rec.Code, rec.Body, rec.Header())
				}
			})
		}
	}
}

func TestLegacyResponseStripsLateTrafficControlTrailers(t *testing.T) {
	resp := &http.Response{Header: make(http.Header), Body: io.NopCloser(strings.NewReader("body"))}
	defer resp.Body.Close()
	resp.Header.Set("Trailer", trafficResponseSessionHeader+", "+TrafficPolicyRevisionHeader+", grpc-status")
	stripGuestEvidenceResponseHeaders(resp)
	// H2 allocates/populates the trailer map after the initial headers. The
	// filter must inspect the response's current map when body reading ends.
	resp.Trailer = http.Header{trafficResponseSessionHeader: []string{"long-lived"}, TrafficPolicyRevisionHeader: []string{"forged"}, "Grpc-Status": []string{"0"}}
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatal(err)
	}
	if resp.Trailer.Get(trafficResponseSessionHeader) != "" || resp.Trailer.Get(TrafficPolicyRevisionHeader) != "" || resp.Header.Get("Trailer") != "grpc-status" || resp.Trailer.Get("Grpc-Status") != "0" {
		t.Fatalf("control trailer leaked or gRPC trailer lost: %v / %v", resp.Header, resp.Trailer)
	}
}

func TestPublicResponseRetainsPolicyHeaderButRejectsPolicyTrailer(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		for _, announced := range []bool{false, true} {
			t.Run("h2="+strconv.FormatBool(h2)+"/announced="+strconv.FormatBool(announced), func(t *testing.T) {
				compute := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set(TrafficPolicyRevisionHeader, "traffic-v1:trusted")
					w.Header().Set("Trailer", "grpc-status")
					if announced {
						w.Header().Add("Trailer", TrafficPolicyRevisionHeader)
					}
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte("body"))
					name := http.TrailerPrefix + TrafficPolicyRevisionHeader
					if announced {
						name = TrafficPolicyRevisionHeader
					}
					w.Header().Set(name, "forged-trailer")
					w.Header().Set("Grpc-Status", "0")
				}))
				compute.Config.Protocols = new(http.Protocols)
				compute.Config.Protocols.SetHTTP1(true)
				compute.Config.Protocols.SetUnencryptedHTTP2(true)
				compute.Start()
				defer compute.Close()
				proxy := NewInternalReverseProxy(&stubDialer{server: compute}, &url.URL{Scheme: "http", Host: "compute"}, slog.New(slog.NewTextHandler(io.Discard, nil)), h2)
				public := httptest.NewServer(proxy)
				defer public.Close()
				resp, err := public.Client().Get(public.URL)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				if err != nil || string(body) != "body" || resp.Header.Get(TrafficPolicyRevisionHeader) != "traffic-v1:trusted" || resp.Trailer.Get(TrafficPolicyRevisionHeader) != "" || resp.Trailer.Get("Grpc-Status") != "0" {
					t.Fatalf("policy evidence header/trailer=%v/%v body=%q err=%v", resp.Header, resp.Trailer, body, err)
				}
			})
		}
	}
}

func TestResponseSessionAndDeadlineControlsArePlatformOwned(t *testing.T) {
	for _, implicit := range []bool{false, true} {
		for _, longLived := range []bool{false, true} {
			t.Run(strconv.FormatBool(implicit)+"/long="+strconv.FormatBool(longLived), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				rec := httptest.NewRecorder()
				writer := &statusRecorder{ResponseWriter: rec,
					trafficPolicyRevision:    "verified",
					trafficResponseContext:   func() context.Context { return ctx },
					trafficResponseLongLived: func(int) bool { return longLived },
					trafficStreamingStatus:   api.StreamingStatusFlagDisabled,
				}
				defer writer.stopTrafficResponse()
				for _, name := range []string{trafficResponseDeadlineHeader, trafficResponseSessionHeader, api.StreamingStatusHeader, TrafficPolicyRevisionHeader} {
					writer.Header().Set(name, "guest forgery")
					writer.installHeaderOps([]EdgeRuleHeaderOp{{Name: name, Value: "rule forgery", Action: "set"}})
				}
				if implicit {
					_, _ = writer.Write([]byte("body"))
				} else {
					writer.WriteHeader(http.StatusOK)
				}
				if isLongLivedResponse(rec.Code, rec.Header()) != longLived || rec.Header().Get(api.StreamingStatusHeader) != string(api.StreamingStatusFlagDisabled) {
					t.Fatal("application or rule changed the platform's response session")
				}
				if rec.Header().Get(TrafficPolicyRevisionHeader) != "verified" {
					t.Fatal("source/rule replaced effective policy proof")
				}
				deadline, _ := ctx.Deadline()
				want := ""
				if !longLived {
					want = strconv.FormatInt(deadline.UnixNano(), 10)
				}
				if got := rec.Header().Get(trafficResponseDeadlineHeader); got != want {
					t.Fatalf("deadline=%q, want %q", got, want)
				}
			})
		}
	}
	for _, code := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		header := make(http.Header)
		header.Set(api.StreamingStatusHeader, string(api.StreamingStatusStreaming))
		header.Set("Content-Type", "application/grpc")
		if isLongLivedResponse(code, header) {
			t.Fatal("guest streaming status/content-type detached the request budget")
		}
		header.Set(trafficResponseSessionHeader, "long-lived")
		if code >= http.StatusBadRequest && isLongLivedResponse(code, header) {
			t.Fatal("unsuccessful handshake detached its request budget")
		}
	}
	for _, name := range []string{trafficResponseSessionHeader, trafficResponseDeadlineHeader, api.StreamingStatusHeader} {
		for _, key := range []string{name, strings.ToLower(name), http.TrailerPrefix + name} {
			dst := make(http.Header)
			forwardedResponseHeader(t.Context(), dst, key, "forged")
			if len(dst) != 0 {
				t.Fatalf("guest control %s passed forwarding boundary", key)
			}
		}
	}
}
