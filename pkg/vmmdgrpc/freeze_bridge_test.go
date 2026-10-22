// adr: 1151 — a freeze closes the instance's stream bridge and the thaw starts
// it again beside the resume, so the first request after a thaw does not
// spawn it on its own path.
package vmmdgrpc

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// Production-us 2026-10-21 (rc.288, frozen tier on for a test): the first
// request after a thaw spent 16-64 ms in the gateway's internal proxy, against
// a hot request's 21 ms end to end, because the freeze had dropped the bridge
// and nothing started it before that request.
func TestThawStartsTheBridgeTheFreezeClosed(t *testing.T) {
	bridgePath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(streamBridgePathEnv, bridgePath)
	var starts atomic.Int32
	s := New(&freezeStubVMM{}, nil, "1.17.0", nil)
	s.streamBridges = newTestStreamBridgeManager(t, func() { starts.Add(1) })
	req := &vmmdpb.ForwardHTTPRequestInit{Instance: "vm-a", Port: 9090, AppProtocol: "http2"}
	// The restore's prewarm, then the requests the instance served.
	lease, err := s.streamBridges.acquire(t.Context(), req, "fc-vm-a")
	if err != nil {
		t.Fatal(err)
	}
	lease.release()

	if _, err := s.FreezeInstance(t.Context(), &vmmdpb.FreezeInstanceRequest{Instance: "vm-a"}); err != nil {
		t.Fatalf("FreezeInstance: %v", err)
	}
	if n := bridgeEntries(s.streamBridges, "vm-a"); n != 0 {
		t.Fatalf("bridges of the frozen instance = %d, want none", n)
	}
	if _, err := s.ThawInstance(t.Context(), &vmmdpb.ThawInstanceRequest{Instance: "vm-a"}); err != nil {
		t.Fatalf("ThawInstance: %v", err)
	}
	waitBridges(t, func() bool { return starts.Load() == 2 })
	// The first request joins the bridge the thaw started.
	lease, err = s.streamBridges.acquire(t.Context(), req, "fc-vm-a")
	if err != nil {
		t.Fatal(err)
	}
	lease.release()
	if got := starts.Load(); got != 2 {
		t.Fatalf("bridge starts = %d, want the thaw's bridge reused by the first request", got)
	}

	// A parked instance is gone: nothing starts its bridge again.
	s.forgetForwarding(t.Context(), "vm-a")
	if n := s.streamBridges.resume(context.Background(), "vm-a"); n != 0 || bridgeEntries(s.streamBridges, "vm-a") != 0 {
		t.Fatalf("resume after park started %d bridges", n)
	}
}

// A thaw that fails drops the bridge it started.
func TestFailedThawDropsTheBridge(t *testing.T) {
	bridgePath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(streamBridgePathEnv, bridgePath)
	vmm := &freezeStubVMM{}
	s := New(vmm, nil, "1.17.0", nil)
	s.streamBridges = newTestStreamBridgeManager(t, func() {})
	lease, err := s.streamBridges.acquire(t.Context(), &vmmdpb.ForwardHTTPRequestInit{Instance: "vm-a", Port: 9090}, "fc-vm-a")
	if err != nil {
		t.Fatal(err)
	}
	lease.release()
	if _, err := s.FreezeInstance(t.Context(), &vmmdpb.FreezeInstanceRequest{Instance: "vm-a"}); err != nil {
		t.Fatal(err)
	}
	vmm.thawErr = context.DeadlineExceeded
	if _, err := s.ThawInstance(t.Context(), &vmmdpb.ThawInstanceRequest{Instance: "vm-a"}); err == nil {
		t.Fatal("ThawInstance succeeded")
	}
	waitBridges(t, func() bool { return bridgeEntries(s.streamBridges, "vm-a") == 0 })
	if n := s.streamBridges.resume(context.Background(), "vm-a"); n != 0 {
		t.Fatalf("resume after a failed thaw started %d bridges", n)
	}
}

func bridgeEntries(m *streamBridgeManager, instance string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, entry := range m.entries {
		if entry.instance == instance && !entry.closed {
			n++
		}
	}
	return n
}

func waitBridges(t *testing.T, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("timed out waiting for the bridge manager")
}
