package main

// adr: 080 Raw Upgrade traffic must retain its response framing and stream
// bidirectionally without waiting for the guest to close the connection.

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestBridgeUpgradeHeadAndBidirectionalFrames(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "vmmd-raw-bridge")
	if err := buildBridgeForTest(binPath); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	// Binary frame includes CR/LF and a non-UTF8 byte: header normalization
	// must never touch the raw body bytes. The client frame is masked text.
	serverFrame := []byte{0x82, 4, 0, '\r', '\n', 0xff}
	clientFrame := []byte{0x81, 0x85, 1, 2, 3, 4, 'h' ^ 1, 'e' ^ 2, 'l' ^ 3, 'l' ^ 4, 'o' ^ 1}
	closeFrame := []byte{0x88, 0}
	guestDone := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			guestDone <- err
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		reader := bufio.NewReader(conn)
		request, err := http.ReadRequest(reader)
		if err != nil {
			guestDone <- fmt.Errorf("guest request: %w", err)
			return
		}
		if request.Header.Get("Upgrade") != "websocket" {
			guestDone <- fmt.Errorf("guest request did not carry websocket Upgrade")
			return
		}
		if _, err := conn.Write(append([]byte("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n\r\n"), serverFrame...)); err != nil {
			guestDone <- err
			return
		}
		got := make([]byte, len(clientFrame))
		if _, err := io.ReadFull(reader, got); err != nil {
			guestDone <- err
			return
		}
		if !bytes.Equal(got, clientFrame) {
			guestDone <- fmt.Errorf("guest received %x, want %x", got, clientFrame)
			return
		}
		_, err = conn.Write(closeFrame)
		guestDone <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, binPath, host, port)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = stdin.Close(); _ = command.Wait() })
	requestBytes := "GET /ws HTTP/1.1\r\nHost: audit.example\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n"
	if _, err := io.WriteString(stdin, requestBytes); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(stdout)
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatalf("read bridge response while guest remains open: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusSwitchingProtocols || response.Header.Get("Sec-WebSocket-Accept") != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("upgrade response = %+v", response)
	}
	got := make([]byte, len(serverFrame))
	if _, err := io.ReadFull(reader, got); err != nil || !bytes.Equal(got, serverFrame) {
		t.Fatalf("server frame = %x, %v; want %x", got, err, serverFrame)
	}
	if _, err := stdin.Write(clientFrame); err != nil {
		t.Fatal(err)
	}
	got = make([]byte, len(closeFrame))
	if _, err := io.ReadFull(reader, got); err != nil || !bytes.Equal(got, closeFrame) {
		t.Fatalf("close frame = %x, %v; want %x", got, err, closeFrame)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("bridge exit: %v, stderr=%s", err, stderr.String())
	}
	if err := <-guestDone; err != nil {
		t.Fatal(err)
	}
}
