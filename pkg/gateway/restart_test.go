//go:build unix

// spec: §4.1
// adr: 20261018072701
// Zero-downtime gateway restarts
// (docs/adr/20261018072701-zero-downtime-daemon-restarts.md).

package gateway

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/drain"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// systemdSocket stands in for faas-gatewayd-internal.socket: a listening
// unix socket that stays open while gatewayd-internal generations come and
// go. Each generation inherits its own duplicate, as from LISTEN_FDS.
func systemdSocket(t *testing.T) (string, func(network, address string) (net.Listener, bool, error)) {
	t.Helper()
	dir, err := os.MkdirTemp("", "gw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "i.sock")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	l.SetUnlinkOnClose(false)
	master, err := l.File()
	_ = l.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = master.Close() })
	return path, func(network, address string) (net.Listener, bool, error) {
		if network != "unix" || address != path {
			return nil, false, nil
		}
		fd, err := syscall.Dup(int(master.Fd()))
		if err != nil {
			return nil, false, err
		}
		file := os.NewFile(uintptr(fd), "inherited")
		defer file.Close()
		inherited, err := net.FileListener(file)
		return inherited, err == nil, err
	}
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestInternalGatewayRestartUnderLoadDropsNothing restarts gatewayd-internal's
// socket server while gatewayd-public's proxy carries GETs and POSTs over the
// production H2C hop. The old generation drains with Stop; requests that
// arrive before the replacement starts wait in the inherited socket.
func TestInternalGatewayRestartUnderLoadDropsNothing(t *testing.T) {
	path, inherited := systemdSocket(t)
	orig := inheritedSynthListener
	inheritedSynthListener = inherited
	t.Cleanup(func() { inheritedSynthListener = orig })

	var served [3]atomic.Int64
	startGeneration := func(generation int) *SynthServer {
		s := NewSynthServer(path, nil, discardLog())
		s.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n, _ := io.Copy(io.Discard, r.Body)
			time.Sleep(2 * time.Millisecond) // keep requests in flight across Stop
			served[generation].Add(1)
			_, _ = fmt.Fprintf(w, "%d:%d", generation, n)
		}))
		if err := s.Start(); err != nil {
			t.Fatal(err)
		}
		return s
	}

	tracker := drain.NewTracker()
	proxy := NewInternalReverseProxy(NewUnixSocketDialer(path), &url.URL{Scheme: "http", Host: "gatewayd-internal"}, discardLog(), true).WithInFlightTracker(tracker)
	public := httptest.NewServer(proxy)
	t.Cleanup(public.Close)

	old := startGeneration(1)
	client := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: 64}}
	var (
		failures atomic.Int64
		firstErr atomic.Value
		stop     = make(chan struct{})
		wg       sync.WaitGroup
	)
	body := strings.Repeat("x", 1024)
	for worker := range 16 {
		wg.Go(func() {
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				var resp *http.Response
				var err error
				want := 0
				if (worker+i)%2 == 0 {
					resp, err = client.Get(public.URL + "/read")
				} else {
					want = len(body)
					resp, err = client.Post(public.URL+"/write", "text/plain", strings.NewReader(body))
				}
				if err != nil {
					failures.Add(1)
					firstErr.CompareAndSwap(nil, err.Error())
					continue
				}
				got, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				_, length, _ := strings.Cut(string(got), ":")
				if resp.StatusCode != http.StatusOK || length != strconv.Itoa(want) {
					failures.Add(1)
					firstErr.CompareAndSwap(nil, fmt.Sprintf("status %d body %q", resp.StatusCode, got))
				}
			}
		})
	}
	eventually(t, func() bool { return served[1].Load() > 200 })

	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := old.Stop(stopCtx); err != nil {
		t.Fatalf("old generation drain: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("socket path vanished during the restart: %v", err)
	}
	time.Sleep(200 * time.Millisecond) // replacement start-up
	replacement := startGeneration(2)
	t.Cleanup(func() { _ = replacement.Stop(context.Background()) })
	eventually(t, func() bool { return served[2].Load() > 200 })
	close(stop)
	wg.Wait()

	if n := failures.Load(); n != 0 {
		t.Fatalf("%d requests failed across the restart; first: %v", n, firstErr.Load())
	}
}

// TestInternalProxyEndsEventStreamOnShutdown: an SSE stream through
// gatewayd-public ends cleanly when shutdown begins (EventSource reconnects),
// while another long-lived stream keeps its drain grace.
func TestInternalProxyEndsEventStreamOnShutdown(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType := "text/event-stream; charset=utf-8"
		if r.URL.Path == "/ndjson" {
			contentType = "application/x-ndjson"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set(api.StreamingStatusHeader, string(api.StreamingStatusStreaming))
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: 1\n\n")
		http.NewResponseController(w).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(upstream.Close)
	target, _ := url.Parse(upstream.URL)
	tracker := drain.NewTracker()
	proxy := NewInternalReverseProxy(NewTCPDialer(target.Host), target, discardLog(), false).WithInFlightTracker(tracker)
	public := httptest.NewServer(proxy)
	t.Cleanup(public.Close)

	sse := openStream(t, public.URL+"/events")
	ndjson := openStream(t, public.URL+"/ndjson")
	tracker.BeginShutdown()

	if err := sse.waitEnd(2 * time.Second); err != nil {
		t.Fatalf("SSE stream did not end cleanly on shutdown: %v", err)
	}
	if err := ndjson.waitEnd(300 * time.Millisecond); err == nil || !strings.Contains(err.Error(), "still open") {
		t.Fatalf("non-SSE stream ended on shutdown (err=%v); it keeps the drain grace", err)
	}
}

type openedStream struct {
	resp *http.Response
	done chan error
}

func openStream(t *testing.T, target string) *openedStream {
	t.Helper()
	resp, err := http.Get(target) //nolint:noctx // test client
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	reader := bufio.NewReader(resp.Body)
	if line, err := reader.ReadString('\n'); err != nil || line != "data: 1\n" {
		t.Fatalf("first event = %q, %v", line, err)
	}
	s := &openedStream{resp: resp, done: make(chan error, 1)}
	go func() {
		_, err := io.Copy(io.Discard, reader)
		s.done <- err
	}()
	return s
}

// waitEnd returns nil for a clean end, the read error for an abort, or a
// "still open" error when the stream outlives wait.
func (s *openedStream) waitEnd(wait time.Duration) error {
	select {
	case err := <-s.done:
		return err
	case <-time.After(wait):
		return fmt.Errorf("stream still open after %v", wait)
	}
}

// TestInternalProxyClosesUpgradeOnShutdown: an upgraded WebSocket tunnel
// through gatewayd-public closes when shutdown begins.
func TestInternalProxyClosesUpgradeOnShutdown(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = rw.Flush()
		_, _ = io.Copy(io.Discard, rw) // hold the tunnel open
	}))
	t.Cleanup(upstream.Close)
	target, _ := url.Parse(upstream.URL)
	tracker := drain.NewTracker()
	proxy := NewInternalReverseProxy(NewTCPDialer(target.Host), target, discardLog(), false).WithInFlightTracker(tracker)
	public := httptest.NewServer(proxy)
	t.Cleanup(public.Close)

	conn := upgradeThrough(t, public.Listener.Addr().String())
	tracker.BeginShutdown()
	if err := waitClosed(conn, 2*time.Second); err != nil {
		t.Fatalf("upgraded tunnel survived shutdown: %v", err)
	}
}

func upgradeThrough(t *testing.T, addr string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_, _ = io.WriteString(conn, "GET /socket HTTP/1.1\r\nHost: app.example\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("upgrade response: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade status = %d, want 101", resp.StatusCode)
	}
	return conn
}

func waitClosed(conn net.Conn, wait time.Duration) error {
	_ = conn.SetReadDeadline(time.Now().Add(wait))
	buf := make([]byte, 1)
	for {
		_, err := conn.Read(buf)
		if err == nil {
			continue
		}
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return fmt.Errorf("still open after %v", wait)
		}
		return nil
	}
}

// heldHTTPStream sends a response head and one chunk, then holds the stream
// open until the forwarder cancels it, as a guest serving SSE would.
type heldHTTPStream struct {
	ctx    context.Context
	frames []*vmmdpb.ForwardHTTPStreamResponse
}

func (s *heldHTTPStream) Send(*vmmdpb.ForwardHTTPStreamRequest) error { return nil }
func (s *heldHTTPStream) Recv() (*vmmdpb.ForwardHTTPStreamResponse, error) {
	if len(s.frames) > 0 {
		f := s.frames[0]
		s.frames = s.frames[1:]
		return f, nil
	}
	<-s.ctx.Done()
	return nil, status.Error(codes.Canceled, s.ctx.Err().Error())
}
func (s *heldHTTPStream) CloseSend() error             { return nil }
func (s *heldHTTPStream) Context() context.Context     { return s.ctx }
func (s *heldHTTPStream) Header() (metadata.MD, error) { return nil, nil }
func (s *heldHTTPStream) Trailer() metadata.MD         { return nil }
func (s *heldHTTPStream) SendMsg(any) error            { return nil }
func (s *heldHTTPStream) RecvMsg(any) error            { return nil }

type heldHTTPStreamClient struct {
	vmmdpb.VmmdClient
	contentType string
}

func (c *heldHTTPStreamClient) ForwardHTTPStream(ctx context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse], error) {
	return &heldHTTPStream{ctx: ctx, frames: []*vmmdpb.ForwardHTTPStreamResponse{
		{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{
			Status:  http.StatusOK,
			Headers: []*vmmdpb.Header{{Name: "Content-Type", Value: c.contentType}},
		}}},
		{Frame: &vmmdpb.ForwardHTTPStreamResponse_BodyChunk{BodyChunk: []byte("data: 1\n\n")}},
	}}, nil
}

// TestForwarderEndsEventStreamOnShutdown: gatewayd-internal's stream
// forwarder ends a guest's SSE response cleanly when shutdown begins and
// leaves other streamed responses to the drain grace.
func TestForwarderEndsEventStreamOnShutdown(t *testing.T) {
	tracker := drain.NewTracker()
	serve := func(contentType string) string {
		nodes := singleClientLookup{cli: &heldHTTPStreamClient{contentType: contentType}}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := drain.WithShutdownNotice(r.Context(), tracker)
			cli, _, _ := nodes.ClientFor(ctx, "n1")
			fwdStreamOnceWithEvents(flushEachWrite{w}, r.WithContext(ctx), cli, discardLog(), Target{NodeID: "n1"}, nil)
		}))
		t.Cleanup(srv.Close)
		return srv.URL
	}
	sse := openStream(t, serve("text/event-stream"))
	ndjson := openStream(t, serve("application/x-ndjson"))
	tracker.BeginShutdown()

	if err := sse.waitEnd(2 * time.Second); err != nil {
		t.Fatalf("SSE stream did not end cleanly on shutdown: %v", err)
	}
	if err := ndjson.waitEnd(300 * time.Millisecond); err == nil || !strings.Contains(err.Error(), "still open") {
		t.Fatalf("non-SSE stream ended on shutdown (err=%v); it keeps the drain grace", err)
	}
}

// flushEachWrite stands in for the Handler's statusRecorder, which flushes
// streamed chunks to the client.
type flushEachWrite struct{ http.ResponseWriter }

func (w flushEachWrite) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	http.NewResponseController(w.ResponseWriter).Flush()
	return n, err
}

func (w flushEachWrite) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// heldRawStream answers the upgrade and then holds the session open.
type heldRawStream struct {
	ctx  context.Context
	sent bool
}

func (s *heldRawStream) Send(*vmmdpb.ForwardRawRequest) error { return nil }
func (s *heldRawStream) Recv() (*vmmdpb.ForwardRawResponse, error) {
	if !s.sent {
		s.sent = true
		return &vmmdpb.ForwardRawResponse{Frame: &vmmdpb.ForwardRawResponse_Init{Init: &vmmdpb.ForwardRawResponseInit{
			Status:  http.StatusSwitchingProtocols,
			Headers: []*vmmdpb.Header{{Name: "Upgrade", Value: "websocket"}, {Name: "Connection", Value: "Upgrade"}},
		}}}, nil
	}
	<-s.ctx.Done()
	return nil, status.Error(codes.Canceled, s.ctx.Err().Error())
}
func (s *heldRawStream) CloseSend() error             { return nil }
func (s *heldRawStream) Context() context.Context     { return s.ctx }
func (s *heldRawStream) Header() (metadata.MD, error) { return nil, nil }
func (s *heldRawStream) Trailer() metadata.MD         { return nil }
func (s *heldRawStream) SendMsg(any) error            { return nil }
func (s *heldRawStream) RecvMsg(any) error            { return nil }

type heldRawStreamClient struct{ vmmdpb.VmmdClient }

func (heldRawStreamClient) ForwardRawStream(ctx context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardRawRequest, vmmdpb.ForwardRawResponse], error) {
	return &heldRawStream{ctx: ctx}, nil
}

// TestRawForwarderClosesUpgradeOnShutdown: gatewayd-internal closes an
// upgraded WebSocket session when shutdown begins.
func TestRawForwarderClosesUpgradeOnShutdown(t *testing.T) {
	tracker := drain.NewTracker()
	proxy := ForwardingRawReverseProxyWithEventsAndDrain(singleClientLookup{cli: heldRawStreamClient{}}, discardLog(), nil, nil, tracker)
	srv := httptest.NewServer(proxy(Target{NodeID: "n1", InstanceID: "i1", Port: 8080}))
	t.Cleanup(srv.Close)

	conn := upgradeThrough(t, srv.Listener.Addr().String())
	tracker.BeginShutdown()
	if err := waitClosed(conn, 2*time.Second); err != nil {
		t.Fatalf("upgraded session survived shutdown: %v", err)
	}
	outcome, err := tracker.Drain(context.Background(), 2*time.Second)
	if err != nil || outcome != drain.OutcomeClean {
		t.Fatalf("drain after closing the session = %s, %v", outcome, err)
	}
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestMakeBodyReplayable(t *testing.T) {
	small := strings.Repeat("s", 10)
	large := strings.Repeat("l", int(api.InternalHopReplayableBodyBytes)+1)
	tests := []struct {
		name          string
		body          string
		contentLength int64
		wantNilBody   bool
		wantReplay    bool
		wantErr       bool
	}{
		{name: "empty body is dropped so the request can resend", contentLength: 0, wantNilBody: true},
		{name: "small known body becomes replayable", body: small, contentLength: int64(len(small)), wantReplay: true},
		{name: "large body keeps streaming", body: large, contentLength: int64(len(large))},
		{name: "chunked body keeps streaming", body: small, contentLength: -1},
		{name: "short body is an error", body: small, contentLength: int64(len(small)) + 5, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body))
			req.ContentLength = tt.contentLength
			err := makeBodyReplayable(req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if (req.Body == nil) != tt.wantNilBody {
				t.Fatalf("body nil = %v, want %v", req.Body == nil, tt.wantNilBody)
			}
			if (req.GetBody != nil) != tt.wantReplay {
				t.Fatalf("GetBody set = %v, want %v", req.GetBody != nil, tt.wantReplay)
			}
			if !tt.wantReplay {
				return
			}
			for range 2 {
				body, _ := req.GetBody()
				got, _ := io.ReadAll(body)
				if string(got) != tt.body {
					t.Fatalf("replayed body = %q, want %q", got, tt.body)
				}
			}
		})
	}
}
