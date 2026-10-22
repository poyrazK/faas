package apptaskproto

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

type closeCountingConn struct {
	net.Conn
	closes atomic.Int32
}

func (c *closeCountingConn) Close() error {
	c.closes.Add(1)
	return nil
}

// TestCloseOnCancelStopIsFinal: a session that returns normally runs stop
// and then cancels its context. Once stop has returned, the cancel must not
// reach the connection; its owner closes it. Before stop waited for the
// watcher, an unscheduled watcher saw both cases ready and closed the
// connection about half the time.
func TestCloseOnCancelStopIsFinal(t *testing.T) {
	conn := &closeCountingConn{}
	for i := 0; i < 2000; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		stop := closeOnCancel(ctx, conn)
		stop()
		cancel()
	}
	if n := conn.closes.Load(); n != 0 {
		t.Fatalf("connection closed %d times after stop", n)
	}
}

func TestCloseOnCancelClosesOnCancellation(t *testing.T) {
	conn := &closeCountingConn{}
	ctx, cancel := context.WithCancel(context.Background())
	stop := closeOnCancel(ctx, conn)
	defer stop()
	cancel()
	// A session blocked in a read is released by the close.
	deadline := time.Now().Add(5 * time.Second)
	for conn.closes.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("cancellation did not close the connection")
		}
		time.Sleep(time.Millisecond)
	}
}
