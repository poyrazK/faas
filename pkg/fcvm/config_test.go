package fcvm

import (
	"encoding/json"
	"strings"
	"testing"
)

func validColdSpec() ColdBootSpec {
	return ColdBootSpec{
		KernelPath: "/srv/fc/base/vmlinux-6.1",
		BasePath:   "/srv/fc/base/runner-node22.ext4",
		LayerPath:  "/srv/fc/apps/app/layer-1.ext4",
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
	if !strings.Contains(cfg.BootSource.BootArgs, "console=off") {
		t.Errorf("boot args should disable console (spec §4.4): %q", cfg.BootSource.BootArgs)
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
		"no kernel": func(s *ColdBootSpec) { s.KernelPath = "" },
		"no base":   func(s *ColdBootSpec) { s.BasePath = "" },
		"no layer":  func(s *ColdBootSpec) { s.LayerPath = "" },
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
	})
	line := strings.Join(argv, " ")
	wants := []string{
		"jailer --id abc", // --id is the instance name verbatim (jailer v1.7 rejects '.' / '/' in --id, so no .scope suffix)
		"--uid 20007 --gid 20007",
		"--exec-file /usr/local/bin/firecracker", // required by jailer; names the chroot dir
		"--chroot-base-dir " + JailChrootBase,
		"--netns /run/netns/fc-abc",
		"--cgroup-version 2",
		"--parent-cgroup " + ParentCgroup,
		"--cgroup cpu.weight=256", // mandatory to make jailer create the per-VM child scope
		"-- --api-sock api.sock",  // firecracker's own argv only — no binary name
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

func indexOf(ss []string, target string) int {
	for i, s := range ss {
		if s == target {
			return i
		}
	}
	return -1
}
