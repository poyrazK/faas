package vmmdgrpc

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type recordingResponseSender struct {
	chunks [][]byte
	err    error
}

func (s *recordingResponseSender) Send(frame *vmmdpb.ForwardHTTPStreamResponse) error {
	s.chunks = append(s.chunks, frame.GetBodyChunk())
	return s.err
}

// Every chunk keeps its own bytes although the read buffer is reused, and a
// body larger than one buffer arrives whole and in order.
func TestSendForwardResponseBodyStreamsEveryChunk(t *testing.T) {
	body := strings.Repeat("a", 8*1024) + strings.Repeat("b", 8*1024) + "tail"
	sender := &recordingResponseSender{}
	if err := sendForwardResponseBody(sender, strings.NewReader(body)); err != nil {
		t.Fatalf("send = %v, want nil at EOF", err)
	}
	if len(sender.chunks) != 3 {
		t.Fatalf("sent %d chunks, want 3 of at most 8 KiB", len(sender.chunks))
	}
	if got := string(bytes.Join(sender.chunks, nil)); got != body {
		t.Fatalf("received %d bytes that differ from the %d-byte body", len(got), len(body))
	}
	if sender.chunks[0][0] != 'a' || sender.chunks[1][0] != 'b' {
		t.Fatal("a later read overwrote an earlier chunk")
	}
}

func TestSendForwardResponseBodyReportsFailures(t *testing.T) {
	readErr := errors.New("guest reset")
	err := sendForwardResponseBody(&recordingResponseSender{}, io.MultiReader(strings.NewReader("x"), iotest.ErrReader(readErr)))
	if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "read body: guest reset") {
		t.Fatalf("read failure = %v, want Internal read body", err)
	}
	err = sendForwardResponseBody(&recordingResponseSender{err: errors.New("gateway gone")}, strings.NewReader("x"))
	if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "send body chunk: gateway gone") {
		t.Fatalf("send failure = %v, want Internal send body chunk", err)
	}
}

func BenchmarkSendForwardResponseBodySmall(b *testing.B) {
	sender := &recordingResponseSender{}
	b.ReportAllocs()
	for b.Loop() {
		sender.chunks = sender.chunks[:0]
		_ = sendForwardResponseBody(sender, strings.NewReader("hello, world"))
	}
}
