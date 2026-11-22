package vmmdhop

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// A send blocked on a full TCP window must not stop the response from being
// read: vmmd's handler answers 16 MiB without reading a 16 MiB upload, so
// each side's write waits for the other to read.
func TestBlockedSendDoesNotBlockRecv(t *testing.T) {
	const size = 16 << 20
	v := startVmmd(t, &fakeVmmd{answerEarly: size}, true)
	c := v.client(t, "gatewayd-internal.faas")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := NewStream[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse](ctx, c, forwardMethod)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(&vmmdpb.ForwardHTTPStreamRequest{Frame: &vmmdpb.ForwardHTTPStreamRequest_Init{Init: &vmmdpb.ForwardHTTPRequestInit{Instance: "i-1", Method: "POST"}}}); err != nil {
		t.Fatal(err)
	}
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		chunk := make([]byte, 1<<20)
		for range size / len(chunk) {
			if s.Send(&vmmdpb.ForwardHTTPStreamRequest{Frame: &vmmdpb.ForwardHTTPStreamRequest_BodyChunk{BodyChunk: chunk}}) != nil {
				return
			}
		}
		_ = s.CloseSend()
	}()
	got := 0
	for {
		m, err := s.Recv()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.Fatalf("Recv: %v", err)
			}
			break
		}
		got += len(m.GetBodyChunk())
	}
	if got != size {
		t.Fatalf("read %d response bytes, want %d", got, size)
	}
	select {
	case <-sent:
	case <-time.After(10 * time.Second):
		t.Fatal("the blocked send never returned after the call ended")
	}
}

// A connection vmmd ends between exchanges carries a GOAWAY. A client that
// claimed it before the GOAWAY arrived sends its exchange anyway; vmmd never
// read it, so the client resends it on a new connection and the call
// succeeds instead of failing late in Recv.
func TestGoAwayResendsUnreadExchange(t *testing.T) {
	v := startVmmd(t, &fakeVmmd{}, true)
	v.hop.hop.idleTimeout = 100 * time.Millisecond
	c := v.client(t, "gatewayd-internal.faas")
	var results sync.Map
	c.Result = func(r string) {
		n, _ := results.LoadOrStore(r, new(atomic.Int64))
		n.(*atomic.Int64).Add(1)
	}
	count := func(r string) int64 {
		if n, ok := results.Load(r); ok {
			return n.(*atomic.Int64).Load()
		}
		return 0
	}

	if _, body, err := forward(t, context.Background(), c, "one"); err != nil || body != "one" {
		t.Fatalf("first call: body %q, err %v", body, err)
	}
	// Claim the pooled connection as a call would, then let vmmd's idle
	// timeout send its GOAWAY while the client is not watching.
	cc, reused, err := c.get(context.Background())
	if err != nil || !reused {
		t.Fatalf("pooled connection: reused %v, err %v", reused, err)
	}
	time.Sleep(400 * time.Millisecond)
	c.mu.Lock()
	cc.idleFrom = c.now()
	c.idle = append(c.idle, cc)
	c.mu.Unlock()

	if _, body, err := forward(t, context.Background(), c, "two"); err != nil || body != "two" {
		t.Fatalf("call on the turned-away connection: body %q, err %v", body, err)
	}
	if got := count("retried"); got != 1 {
		t.Fatalf("retried = %d, want 1", got)
	}
	if got := v.fake.calls.Load(); got != 2 {
		t.Fatalf("handler ran %d times, want 2 (the turned-away exchange must not run twice)", got)
	}
}

// An idle pooled connection that receives vmmd's GOAWAY is never reused.
func TestIdleGoAwayRetiresPooledConnection(t *testing.T) {
	v := startVmmd(t, &fakeVmmd{}, true)
	v.hop.hop.idleTimeout = 100 * time.Millisecond
	c := v.client(t, "gatewayd-internal.faas")
	if _, _, err := forward(t, context.Background(), c, "one"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	if _, reused, err := c.get(context.Background()); err != nil || reused {
		t.Fatalf("after GOAWAY: reused %v, err %v; want a fresh connection", reused, err)
	}
}

// A draining server starts no connection, and Wait returns once the ones it
// had end.
func TestServerWaitAfterDrain(t *testing.T) {
	v := startVmmd(t, &fakeVmmd{}, true)
	c := v.client(t, "gatewayd-internal.faas")
	if _, _, err := forward(t, context.Background(), c, "one"); err != nil {
		t.Fatal(err)
	}
	srv := v.hop.hop
	srv.Drain()
	done := make(chan struct{})
	go func() { srv.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return after Drain with only idle connections")
	}
	if srv.track() {
		t.Fatal("a draining server accepted a new connection")
	}
}

// Review finding 1: a temporary accept error (EMFILE) must not stop the
// router, or gRPC's Accept would block forever.
func TestAcceptLoopRetriesTemporaryErrors(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	flaky := &flakyListener{Listener: inner, failures: 3}
	l := NewListener(context.Background(), flaky, &Server{idle: map[*tls.Conn]struct{}{}})
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		c, err := net.Dial("tcp", inner.Addr().String())
		if err == nil {
			_, _ = c.Write([]byte("not tls"))
			defer c.Close()
			time.Sleep(time.Second)
		}
	}()
	accepted := make(chan error, 1)
	go func() {
		c, err := l.Accept()
		if c != nil {
			_ = c.Close()
		}
		accepted <- err
	}()
	select {
	case err := <-accepted:
		if err != nil {
			t.Fatalf("Accept after temporary errors: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Accept never returned: the router stopped on a temporary error")
	}
}

func TestTemporaryAcceptError(t *testing.T) {
	emfile := &net.OpError{Op: "accept", Net: "tcp", Err: &osSyscallError{errno: syscall.EMFILE}}
	if !temporaryAcceptError(emfile) {
		t.Fatal("EMFILE is temporary")
	}
	if temporaryAcceptError(net.ErrClosed) {
		t.Fatal("a closed listener is not temporary")
	}
	if temporaryAcceptError(errors.New("boom")) {
		t.Fatal("an unknown error is not temporary")
	}
}

type flakyListener struct {
	net.Listener
	mu       sync.Mutex
	failures int
}

func (f *flakyListener) Accept() (net.Conn, error) {
	f.mu.Lock()
	if f.failures > 0 {
		f.failures--
		f.mu.Unlock()
		return nil, &net.OpError{Op: "accept", Net: "tcp", Err: &osSyscallError{errno: syscall.EMFILE}}
	}
	f.mu.Unlock()
	return f.Listener.Accept()
}

// osSyscallError reports Temporary as os.SyscallError does for its errno.
type osSyscallError struct{ errno syscall.Errno }

func (e *osSyscallError) Error() string   { return e.errno.Error() }
func (e *osSyscallError) Temporary() bool { return e.errno.Temporary() }
func (e *osSyscallError) Timeout() bool   { return e.errno.Timeout() }
