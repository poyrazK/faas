// Tests for the proto <-> fcvm adapters. The handlers themselves are covered
// by bufconn_test.go; this file pins down the small pure functions in proto.go
// that don't need a gRPC server.

package vmmdgrpc

import (
	"net/netip"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/netns"
)

func TestAddrOrEmpty(t *testing.T) {
	if got := addrOrEmpty(netip.Addr{}); got != "" {
		t.Errorf("invalid addr: got %q, want empty", got)
	}
	if got := addrOrEmpty(netip.MustParseAddr("10.0.0.1")); got != "10.0.0.1" {
		t.Errorf("valid addr: got %q, want 10.0.0.1", got)
	}
}

func TestWakeMethodFrom(t *testing.T) {
	if got := wakeMethodFrom(fcvm.WakeRestore); got != vmmdpb.WakeMethod_WAKE_RESTORE {
		t.Errorf("WakeRestore mapped to %v, want WAKE_RESTORE", got)
	}
	if got := wakeMethodFrom(fcvm.WakeColdBoot); got != vmmdpb.WakeMethod_WAKE_COLD_BOOT {
		t.Errorf("WakeColdBoot mapped to %v, want WAKE_COLD_BOOT", got)
	}
	if got := wakeMethodFrom(fcvm.WakeMethod(99)); got != vmmdpb.WakeMethod_WAKE_COLD_BOOT {
		t.Errorf("unknown method mapped to %v, want default WAKE_COLD_BOOT", got)
	}
}

func TestToWakeRequest_Happy(t *testing.T) {
	req := &vmmdpb.CreateFromSnapshotRequest{
		Instance: "inst-1",
		App:      &vmmdpb.AppSpec{BasePath: "/b", LayerPath: "/l", VcpuCount: 2, MemSizeMib: 256},
		Snapshot: &vmmdpb.SnapshotRef{MemPath: "/m", VmstatePath: "/v", FcVersion: "1.7.0"},
	}
	wr, err := toWakeRequest(req)
	if err != nil {
		t.Fatalf("toWakeRequest: %v", err)
	}
	if wr.Instance != "inst-1" || wr.BasePath != "/b" || wr.LayerPath != "/l" {
		t.Errorf("flattened fields wrong: %+v", wr)
	}
	if wr.VcpuCount != 2 || wr.MemSizeMiB != 256 {
		t.Errorf("int casts wrong: %+v", wr)
	}
	if wr.Snapshot == nil {
		t.Fatal("Snapshot should be set")
	}
	if wr.Snapshot.MemPath != "/m" || wr.Snapshot.VMStatePath != "/v" || wr.Snapshot.FCVersion != "1.7.0" {
		t.Errorf("snapshot fields wrong: %+v", wr.Snapshot)
	}
}

func TestToWakeRequest_NoSnapshot(t *testing.T) {
	req := &vmmdpb.CreateFromSnapshotRequest{
		Instance: "inst-1",
		App:      &vmmdpb.AppSpec{BasePath: "/b"},
	}
	wr, err := toWakeRequest(req)
	if err != nil {
		t.Fatalf("toWakeRequest: %v", err)
	}
	if wr.Snapshot != nil {
		t.Errorf("Snapshot should be nil when proto snapshot is nil, got %+v", wr.Snapshot)
	}
}

func TestToWakeRequest_EmptySnapshotMemPath(t *testing.T) {
	req := &vmmdpb.CreateFromSnapshotRequest{
		Instance: "inst-1",
		App:      &vmmdpb.AppSpec{BasePath: "/b"},
		Snapshot: &vmmdpb.SnapshotRef{MemPath: ""},
	}
	wr, err := toWakeRequest(req)
	if err != nil {
		t.Fatalf("toWakeRequest: %v", err)
	}
	if wr.Snapshot != nil {
		t.Errorf("Snapshot must be nil when MemPath empty, got %+v", wr.Snapshot)
	}
}

func TestToWakeRequest_MissingInstance(t *testing.T) {
	req := &vmmdpb.CreateFromSnapshotRequest{App: &vmmdpb.AppSpec{}}
	if _, err := toWakeRequest(req); err == nil {
		t.Error("missing instance must error")
	}
}

func TestToWakeRequest_MissingApp(t *testing.T) {
	req := &vmmdpb.CreateFromSnapshotRequest{Instance: "i"}
	if _, err := toWakeRequest(req); err == nil {
		t.Error("missing app must error")
	}
}

func TestToColdBootRequest_Happy(t *testing.T) {
	req := &vmmdpb.CreateColdBootRequest{
		Instance: "inst-2",
		App:      &vmmdpb.AppSpec{BasePath: "/b", LayerPath: "/l", VcpuCount: 4, MemSizeMib: 512},
	}
	wr, err := toColdBootRequest(req)
	if err != nil {
		t.Fatalf("toColdBootRequest: %v", err)
	}
	if wr.Snapshot != nil {
		t.Error("cold boot must not produce a Snapshot")
	}
	if wr.Instance != "inst-2" || wr.VcpuCount != 4 || wr.MemSizeMiB != 512 {
		t.Errorf("fields wrong: %+v", wr)
	}
}

func TestToColdBootRequest_MissingInstance(t *testing.T) {
	req := &vmmdpb.CreateColdBootRequest{App: &vmmdpb.AppSpec{}}
	if _, err := toColdBootRequest(req); err == nil {
		t.Error("missing instance must error")
	}
}

func TestToColdBootRequest_MissingApp(t *testing.T) {
	req := &vmmdpb.CreateColdBootRequest{Instance: "i"}
	if _, err := toColdBootRequest(req); err == nil {
		t.Error("missing app must error")
	}
}

func TestWakeResponseFromInstance(t *testing.T) {
	ip := netip.MustParseAddr("10.0.0.1")
	inst := &fcvm.Instance{
		Lease:  fcvm.Lease{UID: 20001, HostIP: ip},
		Net:    netns.Config{Netns: "ns1", VethHost: "vh", VethPeer: "vp"},
		Method: fcvm.WakeRestore,
	}
	resp := wakeResponseFromInstance("inst-x", fcvm.WakeRequest{}, inst, vmmdpb.WakeMethod_WAKE_RESTORE)
	if resp.Instance != "inst-x" || resp.LeaseUid != 20001 {
		t.Errorf("flat fields wrong: %+v", resp)
	}
	if resp.HostIp != "10.0.0.1" || resp.Netns != "ns1" || resp.VethHost != "vh" || resp.VethPeer != "vp" {
		t.Errorf("net fields wrong: %+v", resp)
	}
	if resp.Method != vmmdpb.WakeMethod_WAKE_RESTORE {
		t.Errorf("method = %v", resp.Method)
	}
	if resp.RequestedMethod != vmmdpb.WakeMethod_WAKE_RESTORE {
		t.Errorf("requested = %v", resp.RequestedMethod)
	}
}

func TestWakeResponseFromInstance_BadIP(t *testing.T) {
	// Inst with zero HostIP — addrOrEmpty must produce "" not a literal.
	inst := &fcvm.Instance{Lease: fcvm.Lease{UID: 20001}, Method: fcvm.WakeColdBoot}
	resp := wakeResponseFromInstance("i", fcvm.WakeRequest{}, inst, vmmdpb.WakeMethod_WAKE_COLD_BOOT)
	if resp.HostIp != "" {
		t.Errorf("HostIp = %q, want empty", resp.HostIp)
	}
}
