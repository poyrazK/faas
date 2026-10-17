//go:build unix

package wire_test

import (
	"net"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

// A successor's handed-off listener accepts nothing until it is ready; the
// connection waits in the shared queue for whichever generation accepts.
func TestHandoffListenerAcceptsOnlyOnceReady(t *testing.T) {
	src, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = src.Close() }()
	f, err := src.(*net.TCPListener).File()
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	defer wire.UseHandoffReadyForTest(ready)()
	defer wire.UseHandoffListenersForTest([]int{int(f.Fd())})()

	l, err := wire.ListenInherited("tcp", src.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := l.Accept(); err == nil {
			accepted <- c
		}
	}()
	client, err := net.Dial("tcp", src.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	select {
	case c := <-accepted:
		_ = c.Close()
		t.Fatal("successor accepted before it was ready")
	case <-time.After(200 * time.Millisecond):
	}
	close(ready)
	select {
	case c := <-accepted:
		_ = c.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("successor did not accept once ready")
	}
}

// Closing a successor that never became ready ends its Accept.
func TestHandoffListenerCloseBeforeReady(t *testing.T) {
	src, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = src.Close() }()
	f, err := src.(*net.TCPListener).File()
	if err != nil {
		t.Fatal(err)
	}
	defer wire.UseHandoffReadyForTest(make(chan struct{}))()
	defer wire.UseHandoffListenersForTest([]int{int(f.Fd())})()
	l, err := wire.ListenInherited("tcp", src.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := l.Accept(); done <- err }()
	_ = l.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Accept succeeded on a closed listener")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Accept still blocked after Close")
	}
}
