package main

// adr: 20261030230157 — the gateway sends its own node to vmmd's local data
// socket, without TLS, only while that vmmd serves it.

import (
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestNewLocalVmmdNeedsTheSwitchAndANodeName(t *testing.T) {
	if newLocalVmmd(false, "fsn-3") != nil || newLocalVmmd(true, " ") != nil {
		t.Fatal("local data socket routing without the switch or a node name")
	}
	l := newLocalVmmd(true, " fsn-3 ")
	if l == nil || l.nodeName != "fsn-3" || l.socket != api.VmmdLocalDataSocket {
		t.Fatalf("newLocalVmmd = %+v, want fsn-3 on %s", l, api.VmmdLocalDataSocket)
	}
}

func TestLocalVmmdRoutesOnlyItsOwnServingNode(t *testing.T) {
	serving := true
	l := &localVmmd{nodeName: "fsn-3", socket: "/run/faas/vmmd-data.sock", serving: func(string) bool { return serving }}
	own := state.ComputeNode{Name: "fsn-3", TargetURL: "tcp://10.10.0.3:50051"}
	other := state.ComputeNode{Name: "fsn-2", TargetURL: "tcp://10.10.0.2:50051"}
	vmmdTLS := &tls.Config{MinVersion: tls.VersionTLS13}

	target, ok := l.target(own)
	if !ok || target != "unix:///run/faas/vmmd-data.sock" {
		t.Fatalf("own node target = %q, %v; want the local data socket", target, ok)
	}
	if got := l.tlsFor(target, vmmdTLS); got != nil {
		t.Fatal("the local data socket was dialed with TLS")
	}
	if _, ok := l.target(other); ok {
		t.Fatal("another node was sent to this node's socket")
	}
	if got := l.tlsFor(other.TargetURL, vmmdTLS); got != vmmdTLS {
		t.Fatal("another node lost mTLS")
	}
	serving = false
	if _, ok := l.target(own); ok {
		t.Fatal("own node routed to a socket its vmmd does not serve")
	}

	var off *localVmmd
	if _, ok := off.target(own); ok || off.tlsFor(own.TargetURL, vmmdTLS) != vmmdTLS {
		t.Fatal("a gateway with the switch off changed routing")
	}
}

func TestIsUnixSocket(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "gwld")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !isUnixSocket(sock) || isUnixSocket(plain) || isUnixSocket(filepath.Join(dir, "missing")) {
		t.Fatal("isUnixSocket must report only a unix socket")
	}
}
