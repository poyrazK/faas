package fcvm

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func validColdSpec() ColdBootSpec {
	return ColdBootSpec{
		KernelKey:  "/srv/fc/base/vmlinux-6.1",
		BaseKey:    "/srv/fc/base/runner-node22.ext4",
		LayerKey:   "/srv/fc/apps/app/layer-1.ext4",
		VcpuCount:  2,
		MemSizeMiB: 256,
		Tap:        "tap0",
	}
}

func TestColdBootConfigTwoDrives(t *testing.T) {
	cfg := BuildColdBootConfig(validColdSpec(), 0)
	if len(cfg.Drives) != 2 {
		t.Fatalf("want 2 drives (two-drive scheme §4.6), got %d", len(cfg.Drives))
	}
	base, layer := cfg.Drives[0], cfg.Drives[1]
	if base.DriveID != DriveBase || !base.IsRootDevice || !base.IsReadOnly {
		t.Errorf("drive0 must be the read-only root base, got %+v", base)
	}
	if layer.DriveID != DriveLayer || layer.IsRootDevice || layer.IsReadOnly {
		t.Errorf("drive1 must be the writable non-root layer, got %+v", layer)
	}
}

// adr: 171 — an execution VM has no tenant-facing interface. Vsock remains
// attached by BuildColdBootConfig for the post-restore execution protocol.
func TestColdBootConfigNetworklessOmitsInterfaces(t *testing.T) {
	spec := validColdSpec()
	spec.Tap = ""
	spec.Networkless = true
	cfg := BuildColdBootConfig(spec, 0)
	if len(cfg.NetworkInterfaces) != 0 {
		t.Fatalf("networkless config interfaces = %#v, want none", cfg.NetworkInterfaces)
	}
	if cfg.VsockDevice == nil {
		t.Fatal("networkless execution config must retain vsock")
	}
	if strings.Contains(cfg.BootSource.BootArgs, " ip=") || strings.Contains(cfg.BootSource.BootArgs, "eth0") {
		t.Fatalf("networkless boot args contain tenant networking: %q", cfg.BootSource.BootArgs)
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("networkless cold-boot spec rejected: %v", err)
	}
}

// adr: 171 — a networkless execution guest must not reference a netns that
// was never created, while ordinary app wakes keep the --netns argument.
func TestJailerCommandNetworklessOmitsNetns(t *testing.T) {
	argv := JailerCommand(JailerSpec{Instance: "exec-1", UID: 20001, GID: 20001, Netns: "fc-exec-1", Networkless: true})
	for _, arg := range argv {
		if arg == "--netns" || arg == "/run/netns/fc-exec-1" {
			t.Fatalf("networkless jailer command contains netns argument: %v", argv)
		}
	}
	ordinary := JailerCommand(JailerSpec{Instance: "app-1", UID: 20001, GID: 20001, Netns: "fc-app-1"})
	joined := strings.Join(ordinary, " ")
	if !strings.Contains(joined, "--netns /run/netns/fc-app-1") {
		t.Fatalf("ordinary jailer command lost netns argument: %v", ordinary)
	}
}

// TestColdBootConfig_SidecarTopology (issue #463 / ADR-069 / PR-B
// AC #6) pins the drive topology for the sidecar Workloads
// branch of BuildColdBootConfig. The contract:
//
//	drive0 = shared read-only base rootfs (DriveBase, IsRootDevice=true)
//	drive1 = main workload layer (DriveLayerMain, RW, non-root)
//	drive2..N = sidecar layers (DriveSidecarPrefix+idx, RO, non-root)
//
// where N = len(Workloads). The main drive MUST stay RW (the
// customer's container writes to /tmp, installs pip packages,
// etc.); sidecars MUST be RO (no second writable layer a
// sidecar could use to escape quota accounting).
//
// A regression that flips the IsReadOnly assignment at
// config.go:218 (e.g. flipping the i!=0 condition, or
// dropping the Workloads branch) is silent in production —
// the VM still boots, the snapshot path still works, and the
// disk-quota accounting drifts in a way the customer notices
// only at the next bill. The unit test pins the wire shape so
// the regression shows up at `go test`.
func TestColdBootConfig_SidecarTopology(t *testing.T) {
	spec := ColdBootSpec{
		KernelKey:  "/srv/fc/base/vmlinux-6.1",
		BaseKey:    "/srv/fc/base/runner-node22.ext4",
		VcpuCount:  2,
		MemSizeMiB: 256,
		Tap:        "tap0",
		Workloads: []WorkloadSpec{
			{Name: "main", Type: "main", StorageKey: "/tmp/main.ext4", RamMB: 256, Port: 8080},
			{Name: "migrator", Type: "init", StorageKey: "/tmp/sc0.ext4", RamMB: 64},
			{Name: "scraper", Type: "sidecar", StorageKey: "/tmp/sc1.ext4", RamMB: 32, Port: 9090},
		},
	}
	cfg := BuildColdBootConfig(spec, 0)
	if len(cfg.Drives) != 4 {
		t.Fatalf("drive count = %d, want 4 (base + main + 2 sidecars)", len(cfg.Drives))
	}
	// drive0: shared read-only base rootfs.
	if d := cfg.Drives[0]; d.DriveID != DriveBase || !d.IsRootDevice || !d.IsReadOnly || d.PathOnHost != spec.BaseKey {
		t.Errorf("drive0 must be the RO root base, got %+v", d)
	}
	// drive1: main workload RW layer.
	if d := cfg.Drives[1]; d.DriveID != DriveLayerMain || d.IsRootDevice || d.IsReadOnly || d.PathOnHost != "/tmp/main.ext4" {
		t.Errorf("drive1 must be the main workload RW non-root layer, got %+v", d)
	}
	// drive2..N: sidecar RO layers, in spec order with
	// DriveSidecarPrefix+idx.
	for i, want := range []struct {
		id   string
		path string
	}{
		{"layer-sidecar-0", "/tmp/sc0.ext4"},
		{"layer-sidecar-1", "/tmp/sc1.ext4"},
	} {
		d := cfg.Drives[2+i]
		if d.IsRootDevice || d.IsReadOnly == false {
			t.Errorf("sidecar drive %d must be RO non-root, got %+v", 2+i, d)
		}
		if d.DriveID != want.id {
			t.Errorf("sidecar drive %d DriveID = %q, want %q", 2+i, d.DriveID, want.id)
		}
		if d.PathOnHost != want.path {
			t.Errorf("sidecar drive %d PathOnHost = %q, want %q", 2+i, d.PathOnHost, want.path)
		}
	}
}

// TestColdBootConfig_OneSidecarTopology pins the drive-count
// invariant for the 1-sidecar case (PR-B AC #6 secondary
// sub-test). The drive count MUST be 3 (base + main + 1
// sidecar); a regression that drops the sidecar loop's drive
// append would land here as 2 drives, not 3.
func TestColdBootConfig_OneSidecarTopology(t *testing.T) {
	spec := ColdBootSpec{
		KernelKey:  "/srv/fc/base/vmlinux-6.1",
		BaseKey:    "/srv/fc/base/runner-node22.ext4",
		VcpuCount:  2,
		MemSizeMiB: 256,
		Tap:        "tap0",
		Workloads: []WorkloadSpec{
			{Name: "main", Type: "main", StorageKey: "/tmp/main.ext4", RamMB: 256, Port: 8080},
			{Name: "migrator", Type: "init", StorageKey: "/tmp/sc0.ext4", RamMB: 64},
		},
	}
	cfg := BuildColdBootConfig(spec, 0)
	if len(cfg.Drives) != 3 {
		t.Fatalf("drive count = %d, want 3 (base + main + 1 sidecar)", len(cfg.Drives))
	}
	if d := cfg.Drives[2]; d.IsReadOnly == false || d.IsRootDevice {
		t.Errorf("sidecar drive must be RO non-root, got %+v", d)
	}
}

func TestColdBootConfigVirtioRngAlwaysOn(t *testing.T) {
	cfg := BuildColdBootConfig(validColdSpec(), 0)
	if cfg.Entropy == nil {
		t.Error("entropy (virtio-rng) must always be attached (spec §11)")
	}
}

func TestColdBootConfigMarshalsToFirecrackerSchema(t *testing.T) {
	cfg := BuildColdBootConfig(validColdSpec(), 0)
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	js := string(b)
	// Field names must match the Firecracker API exactly.
	for _, key := range []string{
		`"boot-source"`, `"kernel_image_path"`, `"boot_args"`,
		`"drives"`, `"drive_id"`, `"path_on_host"`, `"is_root_device"`, `"is_read_only"`,
		`"machine-config"`, `"vcpu_count"`, `"mem_size_mib"`, `"smt"`,
		`"network-interfaces"`, `"iface_id"`, `"host_dev_name"`, `"entropy"`,
	} {
		if !strings.Contains(js, key) {
			t.Errorf("marshalled config missing Firecracker key %s\n%s", key, js)
		}
	}
}

func TestColdBootBootArgsDisableConsole(t *testing.T) {
	cfg := BuildColdBootConfig(validColdSpec(), 0)
	if !strings.Contains(cfg.BootSource.BootArgs, "console=ttyS0,115200n8") {
		t.Errorf("boot args should expose the serial console: %q", cfg.BootSource.BootArgs)
	}
}

func TestColdBootBootArgsConfigureIdenticalInnerWorld(t *testing.T) {
	// Every VM gets the same guest IP via kernel autoconfig (ADR-009).
	cfg := BuildColdBootConfig(validColdSpec(), 0)
	if !strings.Contains(cfg.BootSource.BootArgs, "ip=10.0.0.2::10.0.0.1:255.255.255.252::eth0:off") {
		t.Errorf("boot args should carry the identical-inner-world ip= autoconfig: %q", cfg.BootSource.BootArgs)
	}
	if !strings.Contains(cfg.BootSource.BootArgs, "init=/sbin/init") {
		t.Errorf("boot args should exec guest-init as PID1: %q", cfg.BootSource.BootArgs)
	}
}

func TestColdSpecValidate(t *testing.T) {
	if err := validColdSpec().Validate(); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}
	tests := map[string]func(*ColdBootSpec){
		"no kernel": func(s *ColdBootSpec) { s.KernelKey = "" },
		"no base":   func(s *ColdBootSpec) { s.BaseKey = "" },
		"no layer":  func(s *ColdBootSpec) { s.LayerKey = "" },
		"zero vcpu": func(s *ColdBootSpec) { s.VcpuCount = 0 },
		"zero mem":  func(s *ColdBootSpec) { s.MemSizeMiB = 0 },
		"no tap":    func(s *ColdBootSpec) { s.Tap = "" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			s := validColdSpec()
			mutate(&s)
			if err := s.Validate(); err == nil {
				t.Errorf("expected validation error for %q", name)
			}
		})
	}
}

func TestJailerCommandMatchesSpec(t *testing.T) {
	argv := JailerCommand(JailerSpec{
		Instance: "abc", UID: 20007, GID: 20007, Netns: "fc-abc",
		ExecFile: "/usr/local/bin/firecracker",
		Plan:     api.PlanHobby, // issue #301 / ADR-044
	})
	line := strings.Join(argv, " ")
	wants := []string{
		"jailer --id abc", // --id is the instance name verbatim (jailer v1.7 rejects '.' / '/' in --id, so no .scope suffix)
		"--uid 20007 --gid 20007",
		"--exec-file /usr/local/bin/firecracker", // required by jailer; names the chroot dir
		"--chroot-base-dir " + JailChrootBase,
		"--netns /run/netns/fc-abc",
		"--cgroup-version 2",
		"--parent-cgroup " + ParentCgroupFor(api.PlanHobby), // 3-level path (issue #301)
		"--cgroup cpu.weight=" + fmt.Sprintf("%d", api.PlanHobby.CPUWeight()),
		"-- --api-sock api.sock", // firecracker's own argv only — no binary name
	}
	for _, w := range wants {
		if !strings.Contains(line, w) {
			t.Errorf("jailer command missing %q\ngot: %s", w, line)
		}
	}
	// --exec-file is a jailer option (before the `--` separator); nothing but
	// firecracker flags may follow `--` (jailer execs the exec-file itself, so a
	// stray "firecracker" positional there would become a firecracker argument).
	sep, ef := indexOf(argv, "--"), indexOf(argv, "--exec-file")
	if ef < 0 || sep < 0 || ef > sep {
		t.Errorf("--exec-file (%d) must precede the `--` separator (%d)", ef, sep)
	}
	if bare := indexOf(argv, "firecracker"); bare > sep {
		t.Errorf("bare 'firecracker' token at %d follows the `--` separator (%d)", bare, sep)
	}
}

func TestJailerCommandIncludesMemoryFenceAtCreation(t *testing.T) {
	argv := JailerCommand(JailerSpec{
		Instance:       "build-1",
		UID:            20000,
		GID:            20000,
		Netns:          "fc-build-1",
		Plan:           api.PlanScale,
		IsBuilder:      true,
		MemoryMaxBytes: 2048 << 20,
	})
	line := strings.Join(argv, " ")
	if !strings.Contains(line, "--cgroup memory.max=2147483648") {
		t.Fatalf("jailer command missing creation-time memory fence: %s", line)
	}
}

func indexOf(ss []string, target string) int {
	for i, s := range ss {
		if s == target {
			return i
		}
	}
	return -1
}
