package vmmdgrpc

// adr: 20261122140000 — with bridge_binary_http on, an HTTP/1.1 bridge vmmd
// starts serves binary HTTP and vmmd sends it that; HTTP/2 and gRPC bridges,
// and bridges adopted from a predecessor, keep h2c.

import (
	"slices"
	"testing"

	"golang.org/x/net/http2"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

func TestBridgeBinaryHTTPSelection(t *testing.T) {
	for _, tc := range []struct {
		name       string
		on         bool
		protocol   string
		wantBinary bool
	}{
		{name: "switch off keeps h2c", on: false, protocol: "h1", wantBinary: false},
		{name: "switch on sends an HTTP/1.1 bridge binary HTTP", on: true, protocol: "h1", wantBinary: true},
		{name: "switch on keeps an HTTP/2 or gRPC bridge on h2c", on: true, protocol: "h2c", wantBinary: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newStreamBridgeManager(nil)
			m.binaryHTTP.Store(tc.on)
			entry := &streamBridgeEntry{socket: "/nonexistent/bridge.sock", protocol: tc.protocol}
			env := m.startEnv(&vmmdpb.ForwardHTTPRequestInit{Instance: "i-1"}, entry)
			if got := slices.Contains(env, bridgeBinaryHTTPEnv+"=1"); got != tc.wantBinary {
				t.Fatalf("bridge env carries %s=1: %v, want %v", bridgeBinaryHTTPEnv, got, tc.wantBinary)
			}
			if err := m.connectEntry(entry); err != nil {
				t.Fatal(err)
			}
			_, binary := entry.transport.(*streamBridgeBinaryTransport)
			_, h2 := entry.transport.(*http2.Transport)
			if binary != tc.wantBinary || h2 == tc.wantBinary {
				t.Fatalf("transport %T, want binary=%v", entry.transport, tc.wantBinary)
			}
			if entry.client == nil || entry.client.Transport == nil {
				t.Fatal("entry has no client")
			}
		})
	}
}

// A bridge adopted from a predecessor carries no framing in its record, so
// vmmd keeps h2c for it: every bridge serves h2c, whatever its start flags.
func TestAdoptedBridgeKeepsH2C(t *testing.T) {
	m := newStreamBridgeManager(nil)
	m.binaryHTTP.Store(true)
	entry := &streamBridgeEntry{socket: "/nonexistent/bridge.sock", protocol: "h1"}
	if err := m.connectEntry(entry); err != nil {
		t.Fatal(err)
	}
	if _, ok := entry.transport.(*http2.Transport); !ok {
		t.Fatalf("adopted bridge transport %T, want h2c", entry.transport)
	}
}

func TestServerBridgeBinaryHTTPReachesEveryManager(t *testing.T) {
	s := NewWithCPUAndNetAndActivity(nil, nil, "", nil, nil, nil, nil)
	if s.streamBridges.binaryHTTP.Load() {
		t.Fatal("binary HTTP must default to off")
	}
	s.WithBridgeBinaryHTTP(true)
	if !s.streamBridges.binaryHTTP.Load() {
		t.Error("the constructor's manager did not take the switch")
	}
	if !s.newBridgeManager().binaryHTTP.Load() {
		t.Error("a lazily created manager did not take the switch")
	}
}
