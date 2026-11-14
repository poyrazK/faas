package main_test

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/apptaskmux"
)

// Found on the n2 check of ADR-20261106105015: apptaskmux.Forward returns
// only when the peer stream ends, so `gregale postgres connect` hung after
// psql exited and `--proxy` / `gregale app port-forward` hung on Ctrl-C while
// the task VM kept its session open. Cancelling must end the relay.
func TestForwardAppTaskSessionEndsWhenCancelledWhileTheRemoteStaysOpen(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			conn, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	// The guest side: apptaskmux.Serve, which keeps serving until its input
	// (the session's stdin) ends. Nothing here ends it except closeSession.
	toGuestR, toGuestW := io.Pipe()
	fromGuestR, fromGuestW := io.Pipe()
	guestCtx, stopGuest := context.WithCancel(context.Background())
	defer stopGuest()
	go func() {
		_ = apptaskmux.Serve(guestCtx, toGuestR, fromGuestW, func(ctx context.Context) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", echo.Addr().String())
		})
	}()
	closeSession := func() { // what closing the attach WebSocket does to the session
		_ = toGuestW.Close()
		_ = fromGuestR.CloseWithError(io.ErrClosedPipe)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- main.ForwardAppTaskSession(ctx, ln, toGuestW, fromGuestR, closeSession) }()

	conn, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 4)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(conn, reply); err != nil || string(reply) != "ping" {
		t.Fatalf("relay did not carry traffic: %q %v", reply, err)
	}
	_ = conn.Close()

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("relay kept running after cancel while the remote session stayed open")
	}
	if _, err := net.DialTimeout("tcp", ln.Addr().String(), 200*time.Millisecond); err == nil {
		t.Fatal("local listener still accepting after cancel")
	}
}
