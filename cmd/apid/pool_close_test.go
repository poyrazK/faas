package main

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestClosePoolAfterCancel_ReleasesContextBoundHolders(t *testing.T) {
	pool := pgtest.Open(t)
	ctx, cancel := context.WithCancel(context.Background())

	held := make(chan struct{})
	go func() {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			t.Errorf("acquire: %v", err)
			close(held)
			return
		}
		close(held)
		<-ctx.Done() // a subscriber holds its LISTEN connection until cancelled
		conn.Release()
	}()
	<-held

	done := make(chan struct{})
	go func() { closePoolAfterCancel(cancel, pool); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		cancel() // let the holder go so pgtest's cleanup can close the pool
		t.Fatal("closePoolAfterCancel blocked with a context-bound holder outstanding; " +
			"apid would hang inside its own cleanup and never report its startup error")
	}
}

// The behaviour this replaces: closing first blocks on the holder for as
// long as nobody cancels. Bounded so the test documents the hang rather
// than reproducing it.
func TestPoolClose_BlocksOnAHeldConnection(t *testing.T) {
	pool := pgtest.Open(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	held := make(chan struct{})
	released := make(chan struct{})
	go func() {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			t.Errorf("acquire: %v", err)
			close(held)
			return
		}
		close(held)
		<-ctx.Done()
		conn.Release()
		close(released)
	}()
	<-held

	closed := make(chan struct{})
	go func() { pool.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("pool.Close returned with a connection still held; pgxpool semantics changed and " +
			"closePoolAfterCancel's ordering may no longer matter")
	case <-time.After(500 * time.Millisecond):
		// blocked, as documented — now let it finish
	}
	cancel()
	<-released
	<-closed
}
