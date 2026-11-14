package main

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestReplicaSessionsEndBeforeAPromotion(t *testing.T) {
	var r replicaSessions
	open, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	if !r.add(open) {
		t.Fatal("a standby refused a session")
	}
	done, peerDone := net.Pipe()
	defer func() { _ = peerDone.Close() }()
	if !r.add(done) {
		t.Fatal("a standby refused a second session")
	}
	r.remove(done)

	r.end()
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := peer.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("an open replica session survived the promotion: read = %v, want EOF", err)
	}
	if err := done.SetDeadline(time.Now()); err != nil {
		t.Fatalf("a session that had already ended was closed again: %v", err)
	}
	late, latePeer := net.Pipe()
	defer func() { _ = late.Close(); _ = latePeer.Close() }()
	if r.add(late) {
		t.Fatal("a promoting standby took a new session")
	}

	// A promotion that failed leaves a standby, which serves reads again.
	r.resume()
	if !r.add(late) {
		t.Fatal("a standby whose promotion failed refused a session")
	}
}
