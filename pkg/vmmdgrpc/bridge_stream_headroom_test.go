package vmmdgrpc

// adr: 20261026120000 — with bridge_stream_headroom on, a full bridge
// connection opens another instead of queueing requests, and bridges start
// with a guest pool sized to their stream cap.

import (
	"slices"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

func TestBridgeStreamHeadroom(t *testing.T) {
	cases := []struct {
		name       string
		on         bool
		wantStrict bool
		wantEnv    bool
	}{
		{name: "switch off keeps one strict connection", on: false, wantStrict: true, wantEnv: false},
		{name: "switch on opens more connections", on: true, wantStrict: false, wantEnv: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newStreamBridgeManager(nil)
			m.headroom.Store(tc.on)
			entry := &streamBridgeEntry{socket: "/nonexistent/bridge.sock"}
			if err := m.connectEntry(entry); err != nil {
				t.Fatal(err)
			}
			if got := entry.transport.StrictMaxConcurrentStreams; got != tc.wantStrict {
				t.Errorf("StrictMaxConcurrentStreams = %v, want %v", got, tc.wantStrict)
			}
			env := m.bridgeEnv(&vmmdpb.ForwardHTTPRequestInit{Instance: "i-1"}, nil)
			if got := slices.Contains(env, bridgeGuestHeadroomEnv+"=1"); got != tc.wantEnv {
				t.Errorf("bridge env carries %s=1: %v, want %v (env %v)", bridgeGuestHeadroomEnv, got, tc.wantEnv, env)
			}
		})
	}
}

func TestServerBridgeStreamHeadroomReachesEveryManager(t *testing.T) {
	s := NewWithCPUAndNetAndActivity(nil, nil, "", nil, nil, nil, nil)
	if s.streamBridges.headroom.Load() {
		t.Fatal("headroom must default to off")
	}
	s.WithBridgeStreamHeadroom(true)
	if !s.streamBridges.headroom.Load() {
		t.Error("the constructor's manager did not take the switch")
	}
	if !s.newBridgeManager().headroom.Load() {
		t.Error("a lazily created manager did not take the switch")
	}
}
