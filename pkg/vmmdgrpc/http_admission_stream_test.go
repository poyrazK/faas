package vmmdgrpc

import (
	"context"
	"errors"
	"io"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// scriptedStream is a BidiStreamingServer whose Recv returns queued messages
// (then io.EOF) and blocks while block is non-nil; Send records the message
// and blocks the same way.
type scriptedStream struct {
	grpc.ServerStream
	ctx     context.Context
	in      []*int
	recvs   atomic.Int32
	sends   atomic.Int32
	sent    []*string
	block   chan struct{}
	release chan struct{} // closed when a blocked call should return
}

func (f *scriptedStream) Context() context.Context { return f.ctx }

func (f *scriptedStream) Recv() (*int, error) {
	f.recvs.Add(1)
	if f.block != nil {
		<-f.release
		return nil, io.ErrUnexpectedEOF
	}
	if len(f.in) == 0 {
		return nil, io.EOF
	}
	m := f.in[0]
	f.in = f.in[1:]
	return m, nil
}

func (f *scriptedStream) Send(m *string) error {
	f.sends.Add(1)
	if f.block != nil {
		<-f.release
		return io.ErrUnexpectedEOF
	}
	f.sent = append(f.sent, m)
	return nil
}

func ptr[T any](v T) *T { return &v }

func TestAdmittedHTTPStreamPassesMessagesInOrder(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &scriptedStream{ctx: ctx, in: []*int{ptr(1), ptr(2)}}
	s := newAdmittedHTTPStream[int, string](f, ctx)

	for want := 1; want <= 2; want++ {
		got, err := s.Recv()
		if err != nil || *got != want {
			t.Fatalf("Recv = %v, %v; want %d", got, err, want)
		}
	}
	if _, err := s.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("Recv after the last message = %v, want io.EOF", err)
	}
	for _, m := range []string{"a", "b", "c"} {
		if err := s.Send(ptr(m)); err != nil {
			t.Fatalf("Send(%q) = %v", m, err)
		}
	}
	if len(f.sent) != 3 || *f.sent[0] != "a" || *f.sent[2] != "c" {
		t.Fatalf("sent %v", f.sent)
	}
	// Nothing is read ahead: three Recv calls reached the stream.
	if n := f.recvs.Load(); n != 3 {
		t.Fatalf("stream Recv called %d times, want 3", n)
	}
}

func TestAdmittedHTTPStreamDrainUnblocksRecvAndSend(t *testing.T) {
	for _, op := range []string{"recv", "send"} {
		t.Run(op, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			f := &scriptedStream{ctx: ctx, block: make(chan struct{}), release: make(chan struct{})}
			defer close(f.release)
			s := newAdmittedHTTPStream[int, string](f, ctx)

			errc := make(chan error, 1)
			go func() {
				if op == "recv" {
					_, err := s.Recv()
					errc <- err
					return
				}
				errc <- s.Send(ptr("x"))
			}()
			// Let the call reach the blocked stream before the drain, so the
			// drain abandons a call the pump already owns.
			reached := func() bool {
				if op == "recv" {
					return f.recvs.Load() > 0
				}
				return f.sends.Load() > 0
			}
			for deadline := time.Now().Add(5 * time.Second); !reached(); {
				if time.Now().After(deadline) {
					t.Fatalf("%s never reached the stream", op)
				}
				runtime.Gosched()
			}
			cancel()
			select {
			case err := <-errc:
				if status.Code(err) != codes.Canceled {
					t.Fatalf("%s after drain = %v, want Canceled", op, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("%s stayed blocked after the drain", op)
			}
			// Later calls fail fast instead of queueing behind the stuck one.
			if _, err := s.Recv(); status.Code(err) != codes.Canceled {
				t.Fatalf("Recv after drain = %v", err)
			}
			if err := s.Send(ptr("y")); status.Code(err) != codes.Canceled {
				t.Fatalf("Send after drain = %v", err)
			}
		})
	}
}

// livePumps counts goroutines running an admittedHTTPStream pump, so other
// tests' goroutines don't skew the leak check.
func livePumps() int {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	n := 0
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "admittedHTTPStream") && (strings.Contains(g, ".recvPump(") || strings.Contains(g, ".sendPump(")) {
			n++
		}
	}
	return n
}

func TestAdmittedHTTPStreamPumpsExitWhenTheStreamEnds(t *testing.T) {
	before := livePumps()
	for i := 0; i < 50; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		f := &scriptedStream{ctx: ctx, in: []*int{ptr(1)}}
		s := newAdmittedHTTPStream[int, string](f, ctx)
		if _, err := s.Recv(); err != nil {
			t.Fatal(err)
		}
		if err := s.Send(ptr("a")); err != nil {
			t.Fatal(err)
		}
		cancel()
	}
	deadline := time.Now().Add(5 * time.Second)
	for livePumps() > before {
		if time.Now().After(deadline) {
			t.Fatalf("%d pump goroutines still running, started with %d: pumps leaked", livePumps(), before)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
