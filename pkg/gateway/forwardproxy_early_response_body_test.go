package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// closedAfterSender accepts `accept` chunks, then refuses every Send the way
// a finished ForwardHTTPStream does once the guest has answered.
type closedAfterSender struct {
	accept int
	sent   int
}

func (s *closedAfterSender) Send(*vmmdpb.ForwardHTTPStreamRequest) error {
	if s.sent >= s.accept {
		return io.EOF
	}
	s.sent++
	return nil
}

func (s *closedAfterSender) CloseSend() error { return nil }

type countingReader struct {
	r    io.Reader
	read int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += int64(n)
	return n, err
}

// A guest that answers (413, or ignoring the body) before reading the whole
// upload must not leave the rest unread: the gateway's server would close the
// connection with unread data and the hop in front would drop the response.
func TestPumpForwardRequestBodyDiscardsUploadAfterGuestAnswered(t *testing.T) {
	const size = 4 << 20
	body := &countingReader{r: bytes.NewReader(make([]byte, size))}
	result := make(chan error, 1)
	pumpForwardRequestBody(context.Background(), body, &closedAfterSender{accept: 2}, func() {}, result, func() bool { return true })
	if err := <-result; !errors.Is(err, io.EOF) {
		t.Fatalf("pump result = %v, want the stream's send error", err)
	}
	if body.read != size {
		t.Fatalf("pump read %d of %d body bytes; the rest stayed unread", body.read, size)
	}
}

func TestDiscardForwardRequestBodyStopsAtCap(t *testing.T) {
	const limit = 64 << 10
	body := &countingReader{r: bytes.NewReader(make([]byte, 1<<20))}
	discardForwardRequestBody(body, make([]byte, 8<<10), limit)
	if body.read > limit+8<<10 {
		t.Fatalf("discard read %d bytes past the cap %d", body.read, limit)
	}
}

// gRPC passes no discard: its status trailers must not wait for the
// client's stream, so the pump stops at the send error as before. The same
// holds when the discard's read deadline cannot be armed.
func TestPumpForwardRequestBodyWithoutDiscardStopsAtSendError(t *testing.T) {
	for _, discard := range []func() bool{nil, func() bool { return false }} {
		body := &countingReader{r: bytes.NewReader(make([]byte, 1<<20))}
		result := make(chan error, 1)
		pumpForwardRequestBody(context.Background(), body, &closedAfterSender{}, func() {}, result, discard)
		<-result
		if body.read > 8<<10 {
			t.Fatalf("pump read %d bytes after the stream ended", body.read)
		}
	}
}

// blockingBody blocks every Read until it is closed.
type blockingBody struct{ closed chan struct{} }

func (b *blockingBody) Read([]byte) (int, error) { <-b.closed; return 0, io.ErrClosedPipe }
func (b *blockingBody) Close() error {
	select {
	case <-b.closed:
	default:
		close(b.closed)
	}
	return nil
}

// The discard honours cancellation: a client that stalls its upload after the
// guest answered cannot hold the request past the session context.
func TestPumpForwardRequestBodyDiscardEndsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	first := true
	body := readerFunc(func(p []byte) (int, error) {
		if first {
			first = false
			return copy(p, "x"), nil
		}
		<-ctx.Done()
		return 0, io.ErrClosedPipe
	})
	result := make(chan error, 1)
	go pumpForwardRequestBody(ctx, struct {
		io.Reader
		io.Closer
	}{body, &blockingBody{closed: make(chan struct{})}}, &closedAfterSender{}, func() {}, result, func() bool { return true })
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-result:
	case <-time.After(2 * time.Second):
		t.Fatal("discard did not end on cancellation")
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }
