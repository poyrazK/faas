package udpwire

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"runtime"
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
