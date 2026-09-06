// Tests for the proto <-> fcvm adapters. The handlers themselves are covered
// by bufconn_test.go; this file pins down the small pure functions in proto.go
// that don't need a gRPC server.

package vmmdgrpc

import (
	"context"
	"encoding/base64"
	"net/netip"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
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
		App:      &vmmdpb.AppSpec{BaseKey: "/b", LayerKey: "/l", VcpuCount: 2, MemSizeMib: 256, CpuMillicores: 500},
		Snapshot: &vmmdpb.SnapshotRef{
			VmstatePath:       "/v",
			VmstateStorageKey: "snap/inst-1/vmstate",
			FcVersion:         "1.7.0",
			StorageKey:        "snap/inst-1/mem",
		},
	}
	wr, err := toWakeRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("toWakeRequest: %v", err)
	}
	if wr.Instance != "inst-1" || wr.BaseKey != "/b" || wr.LayerKey != "/l" {
		t.Errorf("flattened fields wrong: %+v", wr)
	}
	if wr.VcpuCount != 2 || wr.MemSizeMiB != 256 || wr.CPUMillicores != 500 {
		t.Errorf("int casts wrong: %+v", wr)
	}
	if wr.Snapshot == nil {
		t.Fatal("Snapshot should be set")
	}
	// #121: both vmstate locators flow through to fcvm.Snapshot so a
	// future regression that drops VmstateStorageKey (e.g. a rename that
	// leaves the proto getter ignored) trips here.
	if wr.Snapshot.VMStatePath != "/v" {
		t.Errorf("vmstate path lost: %+v", wr.Snapshot)
	}
	if wr.Snapshot.VMStateStorageKey != "snap/inst-1/vmstate" {
		t.Errorf("vmstate storage key lost: %+v", wr.Snapshot)
	}
	if wr.Snapshot.FCVersion != "1.7.0" || wr.Snapshot.StorageKey != "snap/inst-1/mem" {
		t.Errorf("snapshot fields wrong: %+v", wr.Snapshot)
	}
}

func TestToWakeRequest_NoSnapshot(t *testing.T) {
	req := &vmmdpb.CreateFromSnapshotRequest{
		Instance: "inst-1",
		App:      &vmmdpb.AppSpec{BaseKey: "/b"},
	}
	wr, err := toWakeRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("toWakeRequest: %v", err)
	}
	if wr.Snapshot != nil {
		t.Errorf("Snapshot should be nil when proto snapshot is nil, got %+v", wr.Snapshot)
	}
}

func TestToWakeRequest_EmptySnapshotStorageKey(t *testing.T) {
	// #96 slice 3 — mem_path is gone from the wire. The empty-storage-key
	// case now signals a snapshot-with-no-blob-locator and the proto
	// decoder drops the Snapshot ref so the Manager's cold-boot branch
	// fires (ADR-005). Bumping FCVersion here is the only thing still
	// meaningful when StorageKey is empty.
	req := &vmmdpb.CreateFromSnapshotRequest{
		Instance: "inst-1",
		App:      &vmmdpb.AppSpec{BaseKey: "/b"},
		Snapshot: &vmmdpb.SnapshotRef{StorageKey: ""},
	}
	wr, err := toWakeRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("toWakeRequest: %v", err)
	}
	if wr.Snapshot != nil {
		t.Errorf("Snapshot must be nil when storage_key empty, got %+v", wr.Snapshot)
	}
}

// TestToWakeRequest_RemoteVmstateShape pins the canonical multi-node shape
// (#121 / ADR-025 axis 2 slice 4): a CreateFromSnapshotRequest with the new
// VmstateStorageKey field populated and the legacy VmstatePath left empty.
// The decoded Snapshot must carry VMStateStorageKey verbatim so vmmd's
// Storage.Get branch is the one taken — default-local sends empty here.
// A regression that drops the conversion (or filters empty paths) trips.
func TestToWakeRequest_RemoteVmstateShape(t *testing.T) {
	const wantKey = "snap/inst-1/vmstate"
	req := &vmmdpb.CreateFromSnapshotRequest{
		Instance: "inst-1",
		App:      &vmmdpb.AppSpec{BaseKey: "/b"},
		Snapshot: &vmmdpb.SnapshotRef{
			VmstateStorageKey: wantKey,
			FcVersion:         "1.7.0",
			StorageKey:        "snap/inst-1/mem",
		},
	}
	wr, err := toWakeRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("toWakeRequest: %v", err)
	}
	if wr.Snapshot == nil {
		t.Fatal("Snapshot should be set")
	}
	if wr.Snapshot.VMStateStorageKey != wantKey {
		t.Errorf("VMStateStorageKey = %q, want %q", wr.Snapshot.VMStateStorageKey, wantKey)
	}
	if wr.Snapshot.VMStatePath != "" {
		t.Errorf("VMStatePath should be empty for the remote shape, got %q", wr.Snapshot.VMStatePath)
	}
	if !wr.Snapshot.Usable("1.7.0") {
		t.Error("decoded remote shape should be Usable — predicate regression")
	}
}

func TestToWakeRequest_MissingInstance(t *testing.T) {
	req := &vmmdpb.CreateFromSnapshotRequest{App: &vmmdpb.AppSpec{}}
	if _, err := toWakeRequest(context.Background(), req); err == nil {
		t.Error("missing instance must error")
	}
}

func TestToWakeRequest_MissingApp(t *testing.T) {
	req := &vmmdpb.CreateFromSnapshotRequest{Instance: "i"}
	if _, err := toWakeRequest(context.Background(), req); err == nil {
		t.Error("missing app must error")
	}
}

func TestToColdBootRequest_Happy(t *testing.T) {
	req := &vmmdpb.CreateColdBootRequest{
		Instance: "inst-2",
		App:      &vmmdpb.AppSpec{BaseKey: "/b", LayerKey: "/l", VcpuCount: 4, MemSizeMib: 512},
	}
	wr, err := toColdBootRequest(context.Background(), req)
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
	if _, err := toColdBootRequest(context.Background(), req); err == nil {
		t.Error("missing instance must error")
	}
}

func TestToColdBootRequest_MissingApp(t *testing.T) {
	req := &vmmdpb.CreateColdBootRequest{Instance: "i"}
	if _, err := toColdBootRequest(context.Background(), req); err == nil {
		t.Error("missing app must error")
	}
}

// TestToColdBootRequest_BuildSpecExportDir pins the builder-VM
// contract (spec §4.5, ADR-003): a non-nil BuildSpec with an
// ExportDir must land in fcvm.WakeRequest.ExportDir so the Manager
// records it and Destroy runs the build-aware teardown. A nil Build
// or empty ExportDir must map to "" (plain-Destroy contract).
func TestToColdBootRequest_BuildSpecExportDir(t *testing.T) {
	newReq := func() *vmmdpb.CreateColdBootRequest {
		return &vmmdpb.CreateColdBootRequest{
			Instance: "inst-build",
			App:      &vmmdpb.AppSpec{BaseKey: "/b", LayerKey: "/l"},
		}
	}
	t.Run("builder export dir", func(t *testing.T) {
		req := newReq()
		req.Build = &vmmdpb.BuildSpec{ExportDir: "/var/lib/faas/build-out/b1", TimeoutSec: 1800}
		wr, err := toColdBootRequest(context.Background(), req)
		if err != nil {
			t.Fatalf("toColdBootRequest: %v", err)
		}
		if wr.ExportDir != "/var/lib/faas/build-out/b1" {
			t.Errorf("ExportDir = %q, want /var/lib/faas/build-out/b1", wr.ExportDir)
		}
		if wr.BuildTimeoutSec != 1800 {
			t.Errorf("BuildTimeoutSec = %d, want 1800", wr.BuildTimeoutSec)
		}
	})
	t.Run("nil build", func(t *testing.T) {
		wr, err := toColdBootRequest(context.Background(), newReq())
		if err != nil {
			t.Fatalf("toColdBootRequest: %v", err)
		}
		if wr.ExportDir != "" {
			t.Errorf("ExportDir = %q, want empty for app VM", wr.ExportDir)
		}
	})
	t.Run("empty export dir", func(t *testing.T) {
		req := newReq()
		req.Build = &vmmdpb.BuildSpec{}
		wr, err := toColdBootRequest(context.Background(), req)
		if err != nil {
			t.Fatalf("toColdBootRequest: %v", err)
		}
		if wr.ExportDir != "" {
			t.Errorf("ExportDir = %q, want empty", wr.ExportDir)
		}
	})
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

func TestSealedFromProto(t *testing.T) {
	// Empty input → nil output (the Manager treats nil and empty
	// equivalently: no StageSecretsEnv call).
	if got := sealedFromProto(nil); got != nil {
		t.Errorf("nil input: got %+v, want nil", got)
	}
	if got := sealedFromProto([]*vmmdpb.SealedSecret{}); got != nil {
		t.Errorf("empty input: got %+v, want nil", got)
	}

	pbs := []*vmmdpb.SealedSecret{
		{Key: "A", Ciphertext: []byte{0x01, 0x02}},
		{Key: "B", Ciphertext: []byte{0x03, 0x04, 0x05}},
	}
	got := sealedFromProto(pbs)
	if len(got) != 2 {
		t.Fatalf("len=%d want 2", len(got))
	}
	if got[0].Key != "A" || string(got[0].Ciphertext) != string([]byte{0x01, 0x02}) {
		t.Errorf("entry 0 wrong: %+v", got[0])
	}
	if got[1].Key != "B" || string(got[1].Ciphertext) != string([]byte{0x03, 0x04, 0x05}) {
		t.Errorf("entry 1 wrong: %+v", got[1])
	}
}

func TestToWakeRequest_ForwardsSealedEnv(t *testing.T) {
	req := &vmmdpb.CreateFromSnapshotRequest{
		Instance: "inst-1",
		App: &vmmdpb.AppSpec{
			BaseKey: "/b",
			SealedEnv: []*vmmdpb.SealedSecret{
				{Key: "STRIPE_KEY", Ciphertext: []byte("ciphertext-1")},
				{Key: "DB_URL", Ciphertext: []byte("ciphertext-2")},
			},
		},
		Snapshot: &vmmdpb.SnapshotRef{StorageKey: "snap/inst-1/mem"},
	}
	wr, err := toWakeRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("toWakeRequest: %v", err)
	}
	if len(wr.SealedEnvEntries) != 2 {
		t.Fatalf("SealedEnvEntries len=%d, want 2", len(wr.SealedEnvEntries))
	}
	if wr.SealedEnvEntries[0].Key != "STRIPE_KEY" || string(wr.SealedEnvEntries[0].Ciphertext) != "ciphertext-1" {
		t.Errorf("entry 0 wrong: %+v", wr.SealedEnvEntries[0])
	}
}

func TestToColdBootRequest_ForwardsSealedEnv(t *testing.T) {
	req := &vmmdpb.CreateColdBootRequest{
		Instance: "inst-2",
		App: &vmmdpb.AppSpec{
			BaseKey: "/b",
			SealedEnv: []*vmmdpb.SealedSecret{
				{Key: "X", Ciphertext: []byte("ct")},
			},
		},
	}
	wr, err := toColdBootRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("toColdBootRequest: %v", err)
	}
	if len(wr.SealedEnvEntries) != 1 || wr.SealedEnvEntries[0].Key != "X" {
		t.Errorf("SealedEnvEntries wrong: %+v", wr.SealedEnvEntries)
	}
}

// TestToWakeRequest_ForwardsPort pins issue #460 / ADR-053 (PR-C): the
// per-deployment override port on AppSpec must reach fcvm.WakeRequest.Port
// so vmmd's forwarder can dial the override port. The server-side default
// for port=0 lives in buildBridgeScript (netns.AppPort), NOT here — the
// adapter just copies the wire value verbatim.
func TestToWakeRequest_ForwardsPort(t *testing.T) {
	tests := []struct {
		name string
		port uint32
		want int
	}{
		{"zero is zero (legacy default handled in buildBridgeScript)", 0, 0},
		{"override port 9090", 9090, 9090},
		{"override port 3000", 3000, 3000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &vmmdpb.CreateFromSnapshotRequest{
				Instance: "inst-1",
				App:      &vmmdpb.AppSpec{BaseKey: "/b", Port: tt.port},
				Snapshot: &vmmdpb.SnapshotRef{StorageKey: "snap/inst-1/mem"},
			}
			wr, err := toWakeRequest(context.Background(), req)
			if err != nil {
				t.Fatalf("toWakeRequest: %v", err)
			}
			if wr.Port != tt.want {
				t.Errorf("Port = %d, want %d", wr.Port, tt.want)
			}
		})
	}
}

// TestToColdBootRequest_ForwardsPort mirrors the wake variant on the
// cold-boot adapter. The very first boot of a deploy happens here; if
// it lost the port, the runner would bind :8080 and the forwarder
// would dial :9090 → 503 on every cold-boot even when the snapshot
// path was unchanged.
func TestToColdBootRequest_ForwardsPort(t *testing.T) {
	tests := []struct {
		name string
		port uint32
		want int
	}{
		{"zero is zero", 0, 0},
		{"override port 9090", 9090, 9090},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &vmmdpb.CreateColdBootRequest{
				Instance: "inst-2",
				App:      &vmmdpb.AppSpec{BaseKey: "/b", Port: tt.port},
			}
			wr, err := toColdBootRequest(context.Background(), req)
			if err != nil {
				t.Fatalf("toColdBootRequest: %v", err)
			}
			if wr.Port != tt.want {
				t.Errorf("Port = %d, want %d", wr.Port, tt.want)
			}
		})
	}
}

// TestToWakeRequest_ForwardsHealthcheckPath pins issue #460 /
// ADR-053, ADR-057 (PR-D): the per-deployment override readiness
// probe path on AppSpec must reach fcvm.WakeRequest.HealthcheckPath
// so vmmd's waitReady can pick the HTTP probe on the cold-boot +
// restore paths. Empty path keeps the legacy TCP-accept on :8080
// (zero regression risk for pre-PR-D callers).
func TestToWakeRequest_ForwardsHealthcheckPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{"empty is empty (legacy TCP-accept on :8080)", "", ""},
		{"override path /healthz", "/healthz", "/healthz"},
		{"override path /readyz", "/readyz", "/readyz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &vmmdpb.CreateFromSnapshotRequest{
				Instance: "inst-1",
				App:      &vmmdpb.AppSpec{BaseKey: "/b", HealthcheckPath: tt.path},
				Snapshot: &vmmdpb.SnapshotRef{StorageKey: "snap/inst-1/mem"},
			}
			wr, err := toWakeRequest(context.Background(), req)
			if err != nil {
				t.Fatalf("toWakeRequest: %v", err)
			}
			if wr.HealthcheckPath != tt.want {
				t.Errorf("HealthcheckPath = %q, want %q", wr.HealthcheckPath, tt.want)
			}
		})
	}
}

// TestToColdBootRequest_ForwardsHealthcheckPath mirrors the wake
// variant on the cold-boot adapter. The very first boot of a deploy
// happens here; if it lost the path, the cold-boot probe would fall
// through to the legacy TCP-accept and the customer's `connectDB()`
// window would surface as a 503-during-wake even when the snapshot
// path was unchanged.
func TestToColdBootRequest_ForwardsHealthcheckPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{"empty is empty", "", ""},
		{"override path /healthz", "/healthz", "/healthz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &vmmdpb.CreateColdBootRequest{
				Instance: "inst-2",
				App:      &vmmdpb.AppSpec{BaseKey: "/b", HealthcheckPath: tt.path},
			}
			wr, err := toColdBootRequest(context.Background(), req)
			if err != nil {
				t.Fatalf("toColdBootRequest: %v", err)
			}
			if wr.HealthcheckPath != tt.want {
				t.Errorf("HealthcheckPath = %q, want %q", wr.HealthcheckPath, tt.want)
			}
		})
	}
}

// TestSidecarsFromProto pins the wire → fcvm.WorkloadSpec flatten
// (issue #463 / ADR-069 / PR-B). nil in / nil out matches the
// sibling helpers (sealedFromProto / apiEnvFromProto) so the
// Manager's nil/empty equivalence survives the wire.
//
// Two-sidecar case mirrors the AC #2 cap (1 init + 1 sidecar).
// Each field on the wire has a 1:1 mirror on WorkloadSpec —
// DriveID maps from SidecarSpec.drive_slot (the FC Drive.DriveID
// the host mounts in the jail chroot), Name/Type from the wire
// verbatim, RamMB and Port as ints.
func TestSidecarsFromProto(t *testing.T) {
	if got := sidecarsFromProto(nil); got != nil {
		t.Errorf("nil input: got %+v, want nil", got)
	}
	if got := sidecarsFromProto([]*vmmdpb.SidecarSpec{}); got != nil {
		t.Errorf("empty input: got %+v, want nil", got)
	}

	pbs := []*vmmdpb.SidecarSpec{
		{
			Name: "migrator", Image: "ghcr.io/org/m@sha256:00", Type: "init",
			RamMb: 64, CpuMillicores: 250, Port: 9091, Essential: true,
			StorageKey: "apps/foo/00000000-0000-0000-0000-aaaaaaaa-migrator.ext4",
			DriveSlot:  "layer-sidecar-0",
			SealedEnv:  []*vmmdpb.SealedSecret{{Key: "TOKEN", Ciphertext: []byte("age-ciphertext")}},
			DependsOn:  []*vmmdpb.WorkloadDependency{{Name: "main", Condition: "started"}},
		},
		{
			Name: "scraper", Image: "ghcr.io/org/s@sha256:01", Type: "sidecar",
			RamMb: 128, Port: 9092, Essential: false,
			StorageKey: "apps/foo/00000000-0000-0000-0000-aaaaaaaa-scraper.ext4",
			DriveSlot:  "layer-sidecar-1",
		},
	}
	got := sidecarsFromProto(pbs)
	if len(got) != 2 {
		t.Fatalf("len=%d want 2", len(got))
	}
	if got[0].Name != "migrator" || got[0].Type != "init" {
		t.Errorf("entry 0 name/type: got %+v", got[0])
	}
	if got[0].Image != "ghcr.io/org/m@sha256:00" {
		t.Errorf("entry 0 Image = %q, want digest ref", got[0].Image)
	}
	if got[0].RamMB != 64 || got[0].CPUMillicores != 250 || got[0].Port != 9091 || !got[0].Essential {
		t.Errorf("entry 0 ram/port/essential: got %+v", got[0])
	}
	if got[0].DriveID != "layer-sidecar-0" {
		t.Errorf("entry 0 DriveID = %q, want layer-sidecar-0", got[0].DriveID)
	}
	if got[0].StorageKey != "apps/foo/00000000-0000-0000-0000-aaaaaaaa-migrator.ext4" {
		t.Errorf("entry 0 StorageKey wrong: got %q", got[0].StorageKey)
	}
	if len(got[0].SealedEnv) != 1 || got[0].SealedEnv[0].Key != "TOKEN" || string(got[0].SealedEnv[0].Ciphertext) != "age-ciphertext" {
		t.Errorf("entry 0 sealed env wrong: got %+v", got[0].SealedEnv)
	}
	if len(got[0].DependsOn) != 1 || got[0].DependsOn[0].Name != "main" || got[0].DependsOn[0].Condition != api.WorkloadDependencyStarted {
		t.Errorf("entry 0 dependencies wrong: got %+v", got[0].DependsOn)
	}
	if got[1].Name != "scraper" || got[1].Type != "sidecar" {
		t.Errorf("entry 1 name/type: got %+v", got[1])
	}
	if got[1].Essential {
		t.Errorf("entry 1 essential = true, want false")
	}
	if got[1].DriveID != "layer-sidecar-1" {
		t.Errorf("entry 1 DriveID = %q, want layer-sidecar-1", got[1].DriveID)
	}
}

// TestToWakeRequest_WithSidecars asserts the AppSpec.sidecars wire
// field is threaded onto WakeRequest.Sidecars verbatim (issue
// #463 / ADR-069 / PR-B). The Manager's nil-vs-empty equivalence
// is the load-bearing property here — empty wire (legacy pre-
// PR-B caller) leaves wr.Sidecars == nil and the single-workload
// path runs unchanged.
func TestToWakeRequest_WithSidecars(t *testing.T) {
	req := &vmmdpb.CreateFromSnapshotRequest{
		Instance: "inst-s",
		App: &vmmdpb.AppSpec{
			BaseKey: "/b", LayerKey: "/l",
			VcpuCount: 2, MemSizeMib: 256,
			Sidecars: []*vmmdpb.SidecarSpec{
				{Name: "migrator", Image: "ghcr.io/m@sha256:00", Type: "init",
					RamMb: 64, DriveSlot: "layer-sidecar-0",
					StorageKey: "apps/foo/d-migrator.ext4", Essential: true},
				{Name: "scraper", Image: "ghcr.io/s@sha256:01", Type: "sidecar",
					RamMb: 128, DriveSlot: "layer-sidecar-1",
					StorageKey: "apps/foo/d-scraper.ext4"},
			},
		},
	}
	wr, err := toWakeRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("toWakeRequest: %v", err)
	}
	if len(wr.Sidecars) != 2 {
		t.Fatalf("Sidecars len = %d, want 2", len(wr.Sidecars))
	}
	if wr.Sidecars[0].Name != "migrator" || !wr.Sidecars[0].Essential {
		t.Errorf("entry 0: got %+v", wr.Sidecars[0])
	}
	if wr.Sidecars[1].Name != "scraper" || wr.Sidecars[1].Essential {
		t.Errorf("entry 1: got %+v", wr.Sidecars[1])
	}
}

// TestToWakeRequest_NoSidecars keeps the legacy single-workload
// path loud: when AppSpec.sidecars is empty, wr.Sidecars must be
// nil (NOT an empty slice) so the Manager's nil-equivalence check
// takes the legacy branch.
func TestToWakeRequest_NoSidecars(t *testing.T) {
	req := &vmmdpb.CreateFromSnapshotRequest{
		Instance: "inst-0",
		App:      &vmmdpb.AppSpec{BaseKey: "/b", LayerKey: "/l"},
	}
	wr, err := toWakeRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("toWakeRequest: %v", err)
	}
	if wr.Sidecars != nil {
		t.Errorf("wr.Sidecars = %+v, want nil for legacy single-workload", wr.Sidecars)
	}
}

// TestToColdBootRequest_WithSidecars mirrors TestToWakeRequest
// for the cold-boot path. Deploy's first boot must stage the
// same drives + cgroups as every subsequent wake — otherwise
// the very first request lands under a different cgroup tree
// and throttle counters fire wrong labels.
func TestToColdBootRequest_WithSidecars(t *testing.T) {
	req := &vmmdpb.CreateColdBootRequest{
		Instance: "inst-c",
		App: &vmmdpb.AppSpec{
			BaseKey: "/b", LayerKey: "/l",
			Sidecars: []*vmmdpb.SidecarSpec{
				{Name: "migrator", Type: "init", DriveSlot: "layer-sidecar-0",
					StorageKey: "apps/foo/d-migrator.ext4"},
			},
		},
	}
	wr, err := toColdBootRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("toColdBootRequest: %v", err)
	}
	if len(wr.Sidecars) != 1 {
		t.Fatalf("Sidecars len = %d, want 1", len(wr.Sidecars))
	}
	if wr.Sidecars[0].Name != "migrator" || wr.Sidecars[0].DriveID != "layer-sidecar-0" {
		t.Errorf("entry 0: got %+v", wr.Sidecars[0])
	}
}

// TestCharacterizationToStruct_OpenAPIDoc (ADR-122 §D2) pins the
// vmmdgrpc-side wire shape for the OpenAPIDoc field. The proto.go
// path mirrors pkg/api/characterization_test.go::TestCharacterizationReport_JSONRoundTrip
// via a structpb.MapValue. The string-literal key
// `"openapi_doc"` is the load-bearing contract — sched/vmmclient.go
// reads against the same key.
func TestCharacterizationToStruct_OpenAPIDoc(t *testing.T) {
	r := api.CharacterizationReport{
		ObservedClass:       "http",
		ObservedPort:        8080,
		ExitCode:            0,
		OutboundCount:       3,
		OpenAPIDoc:          []byte(`{"openapi":"3.1.0","info":{"title":"captured"}}`),
		OpenAPIDocTruncated: true,
	}
	s, ok := characterizationToStruct(r)
	if !ok {
		t.Fatal("characterizationToStruct returned !ok")
	}
	m := s.AsMap()
	// The OpenAPIDoc field is non-empty so the proto.go path
	// materialises it on the map. structpb.NewStruct stores
	// []byte as the raw bytes; the wire body that gRPC sends
	// base64-encodes it (google.protobuf.Value's string_value),
	// but the AsMap() view exposes the raw bytes for type
	// assertions on the byte slice.
	docRaw, ok := m["openapi_doc"]
	if !ok {
		t.Fatal("openapi_doc key missing from structpb map (regression: proto.go forgot to mirror the new field)")
	}
	// structpb.NewStruct interprets a []byte as a base64-decoded
	// string at the wire boundary. The AsMap() view here is the
	// type returned by value.AsInterface() — for a string-typed
	// Value, this is the raw string. We accept either []byte or
	// string (the proto.go path stores the []byte; the gRPC
	// transport re-encodes as base64 at the wire).
	switch v := docRaw.(type) {
	case string:
		// base64-encoded form (the gRPC wire shape).
		decoded, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			t.Fatalf("openapi_doc base64 decode: %v", err)
		}
		if string(decoded) != `{"openapi":"3.1.0","info":{"title":"captured"}}` {
			t.Errorf("openapi_doc decoded: got %q, want %q", string(decoded), `{"openapi":"3.1.0","info":{"title":"captured"}}`)
		}
	case []byte:
		if string(v) != `{"openapi":"3.1.0","info":{"title":"captured"}}` {
			t.Errorf("openapi_doc raw: got %q, want %q", string(v), `{"openapi":"3.1.0","info":{"title":"captured"}}`)
		}
	default:
		t.Fatalf("openapi_doc: unexpected type %T", v)
	}
	if got, want := m["openapi_doc_truncated"], true; got != want {
		t.Errorf("openapi_doc_truncated: got %v, want %v", got, want)
	}
}

// TestCharacterizationToStruct_OpenAPIDocAbsent (ADR-122 §D2) pins
// the absence contract: a zero-value OpenAPIDoc must NOT surface
// a key on the structpb map (the proto.go path uses `if len(...) > 0`
// to gate the assignment). Sched/vmmclient.go relies on the absence
// to mean "no doc captured".
func TestCharacterizationToStruct_OpenAPIDocAbsent(t *testing.T) {
	r := api.CharacterizationReport{
		ObservedClass: "http",
		ObservedPort:  8080,
		ExitCode:      0,
	}
	s, ok := characterizationToStruct(r)
	if !ok {
		t.Fatal("characterizationToStruct returned !ok")
	}
	m := s.AsMap()
	if _, ok := m["openapi_doc"]; ok {
		t.Errorf("openapi_doc key should be absent when the struct is empty (regression: proto.go is unconditionally materialising the field)")
	}
	// The truncation flag is a bool — the proto.go path always
	// materialises it (zero value = false). Verify the wire still
	// carries the key with the right value.
	if got, want := m["openapi_doc_truncated"], false; got != want {
		t.Errorf("openapi_doc_truncated: got %v, want %v", got, want)
	}
}
