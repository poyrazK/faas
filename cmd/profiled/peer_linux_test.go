//go:build linux

package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestProfileUnixPeerAuthentication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	peer, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	conn, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	uid := uint32(os.Getuid())
	if !profilePeerUID(conn, uid) {
		t.Fatal("authenticated peer rejected")
	}
	if profilePeerUID(conn, uid+1) {
		t.Fatal("another UID accepted")
	}
}
