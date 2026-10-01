package udpwire

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBridgePreservesUDPBoundariesAndCancels(t *testing.T) {
	guest, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer guest.Close()
	conn, err := net.DialUDP("udp4", nil, guest.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	requestReader, requestWriter := io.Pipe()
	replyReader, replyWriter := io.Pipe()
	defer requestWriter.Close()
	defer replyReader.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Bridge(ctx, conn, requestReader, replyWriter) }()
	messages := [][]byte{nil, []byte("first"), {0, 255, 1, 0}, bytes.Repeat([]byte{42}, 8192)}
	if runtime.GOOS == "linux" {
		messages = append(messages, bytes.Repeat([]byte{42}, api.UDPDatagramMaxBytes))
	}
	for _, want := range messages {
		t.Logf("round trip %d-byte datagram", len(want))
		if err := Write(requestWriter, want); err != nil {
			t.Fatal(err)
		}
		if err := guest.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatal(err)
		}
		buffer := make([]byte, api.UDPDatagramMaxBytes)
		n, peer, err := guest.ReadFromUDP(buffer)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(buffer[:n], want) {
			t.Fatalf("guest message mismatch: got %d want %d", n, len(want))
		}
		if _, _, err := guest.WriteMsgUDP(buffer[:n], nil, peer); err != nil {
			t.Fatal(err)
		}
		got, err := Read(replyReader)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("reply mismatch: got %d want %d", len(got), len(want))
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bridge failed to cancel blocked pipe and UDP reads")
	}
}

func TestBridgeRejectsTruncatedPipeMessage(t *testing.T) {
	guest, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer guest.Close()
	conn, err := net.DialUDP("udp4", nil, guest.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	replies, output := io.Pipe()
	defer replies.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Bridge(ctx, conn, reader, output) }()
	if _, err := writer.Write([]byte{0, 0, 0, 2, 42}); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	select {
	case err := <-done:
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("truncated pipe: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("bridge stuck after truncated record")
	}
	if err := guest.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var buffer [16]byte
	if n, _, err := guest.ReadFromUDP(buffer[:]); err == nil {
		t.Fatalf("partial datagram reached guest: %d bytes", n)
	}
}

type cancellationInput struct {
	*bytes.Reader
	reads  atomic.Int32
	closed atomic.Bool
}

func (r *cancellationInput) Read(p []byte) (int, error) { r.reads.Add(1); return r.Reader.Read(p) }
func (r *cancellationInput) Close() error               { r.closed.Store(true); return nil }

type cancellationOutput struct {
	bytes.Buffer
	closed atomic.Bool
}

func (w *cancellationOutput) Close() error { w.closed.Store(true); return nil }

func TestBridgePreCanceledContextDoesNotReadOrForward(t *testing.T) {
	guest, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer guest.Close()
	conn, err := net.DialUDP("udp4", nil, guest.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	var framed bytes.Buffer
	if err := Write(&framed, []byte("must not forward")); err != nil {
		t.Fatal(err)
	}
	input := &cancellationInput{Reader: bytes.NewReader(framed.Bytes())}
	output := &cancellationOutput{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := Bridge(ctx, conn, input, output); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled bridge: %v", err)
	}
	if input.reads.Load() != 0 || !input.closed.Load() || !output.closed.Load() {
		t.Fatalf("canceled bridge consumed input or retained pipes: reads=%d input closed=%t output closed=%t", input.reads.Load(), input.closed.Load(), output.closed.Load())
	}
	if _, err := conn.Write([]byte("closed")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("bridge retained UDP socket: %v", err)
	}
	if err := guest.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var buffer [32]byte
	if n, _, err := guest.ReadFromUDP(buffer[:]); err == nil {
		t.Fatalf("pre-canceled bridge forwarded %d bytes", n)
	}
}

type blockedReplyOutput struct {
	*io.PipeWriter
	started chan struct{}
	once    sync.Once
}

func (w *blockedReplyOutput) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.started) })
	return w.PipeWriter.Write(p)
}

func TestBridgeCancellationUnblocksReplyWrite(t *testing.T) {
	guest, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer guest.Close()
	conn, err := net.DialUDP("udp4", nil, guest.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	input, inputPeer := io.Pipe()
	defer inputPeer.Close()
	outputPeer, outputPipe := io.Pipe()
	defer outputPeer.Close()
	output := &blockedReplyOutput{PipeWriter: outputPipe, started: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Bridge(ctx, conn, input, output) }()
	if _, err := guest.WriteToUDP([]byte("blocked reply"), conn.LocalAddr().(*net.UDPAddr)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-output.started:
	case <-time.After(time.Second):
		t.Fatal("reply did not reach unread pipe")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("blocked write cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("bridge did not join blocked reply writer")
	}
	if _, err := inputPeer.Write([]byte{0}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("bridge retained input pipe: %v", err)
	}
	if _, err := conn.Write([]byte("closed")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("bridge retained UDP socket: %v", err)
	}
}
